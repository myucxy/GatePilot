package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/myucxy/gatepilot/agent/internal/adapter"
)

const (
	version         = "0.1.0"
	defaultTrayAddr = "127.0.0.1:18731"
)

type runOptions struct {
	CLIType     string
	ToolType    string
	CommandArgs []string
	CommandLine string
	Decision    string
	Payload     string
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "version", "--version", "-v":
		fmt.Printf("GatePilot gp %s\n", version)
	case "codex":
		runTool("codex", "codex", os.Args[2:])
	case "claude":
		runTool("claude", "claude_code", os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown gp command %q\n", os.Args[1])
		printUsage()
		os.Exit(2)
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "usage: gp codex [args...] | gp claude [args...]")
}

func runTool(toolType string, cliType string, args []string) {
	executable := configuredExecutable(toolType)
	commandArgs := append([]string{executable}, args...)
	options := runOptions{
		CLIType:     adapter.NormalizeCLIType(cliType),
		ToolType:    toolType,
		CommandArgs: commandArgs,
		CommandLine: commandLineForDisplay(commandArgs),
	}
	runManagedTerminal(options)
}

func runManagedTerminal(options runOptions) {
	restoreTitle := maintainTerminalTitle(terminalTitleForCLI(options.CLIType))
	defer restoreTitle()
	ensureTrayRunning()

	sessionID := "local_session_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	wd := currentWorkingDir()
	cliAdapter := adapter.ForCLI(options.CLIType)
	detector := newInteractiveApprovalDetector(sessionID, wd, options, cliAdapter)
	command, err := startInteractiveCommand(interactiveCommandOptions{
		Args:     options.CommandArgs,
		Dir:      wd,
		OnOutput: detector.onOutput,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "start managed terminal failed: %v\n", err)
		os.Exit(1)
	}
	detector.setWriter(command.Input)
	localHost, _ := startLocalSessionHost(sessionID, command.Input)
	_ = upsertLocalSession(localSessionRecord{
		SessionID:           sessionID,
		CLIType:             options.CLIType,
		CommandLineRedacted: options.CommandLine,
		WorkingDir:          wd,
		WorkingDirHash:      "sha256:" + sha256String(wd),
		Status:              "running",
		StartedAt:           time.Now().UTC().Format(time.RFC3339),
		LastOutputSummary:   "本地 AI CLI 已启动",
		ControlAddr:         localHostAddress(localHost),
	})

	exitCode, err := command.Wait()
	if localHost != nil {
		_ = localHost.Close()
	}
	exitStatus := "completed"
	summary := "本地 AI CLI 已结束"
	if err != nil {
		exitStatus = "failed"
		summary = err.Error()
		if exitCode == 0 {
			exitCode = 1
		}
	}
	_ = upsertLocalSession(localSessionRecord{
		SessionID:         sessionID,
		Status:            exitStatus,
		EndedAt:           time.Now().UTC().Format(time.RFC3339),
		LastOutputSummary: summary,
		PendingApprovals:  0,
		ControlAddr:       "",
	})
	if err != nil {
		os.Exit(exitCode)
	}
}

func terminalTitleForCLI(cliType string) string {
	switch adapter.NormalizeCLIType(cliType) {
	case "codex":
		return "GatePilot:codex"
	case "claude_code":
		return "GatePilot:claude"
	default:
		return ""
	}
}

type interactiveApprovalDetector struct {
	mu             sync.Mutex
	sessionID      string
	workingDir     string
	options        runOptions
	cliAdapter     adapter.CLIAdapter
	writer         io.Writer
	sequence       int64
	outputSequence int64
	visible        strings.Builder
	lineBuffer     strings.Builder
	recentLines    []string
	pending        bool
}

func newInteractiveApprovalDetector(sessionID string, workingDir string, options runOptions, cliAdapter adapter.CLIAdapter) *interactiveApprovalDetector {
	return &interactiveApprovalDetector{sessionID: sessionID, workingDir: workingDir, options: options, cliAdapter: cliAdapter}
}

func (d *interactiveApprovalDetector) setWriter(writer io.Writer) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.writer = writer
}

func (d *interactiveApprovalDetector) onOutput(chunk []byte) {
	text := cleanTerminalText(string(chunk))
	if strings.TrimSpace(text) == "" {
		return
	}
	d.mu.Lock()
	d.outputSequence++
	outputSequence := d.outputSequence
	d.appendVisible(text)
	d.appendLines(text)
	d.sequence++
	snapshot := adapter.TerminalSnapshot{
		SessionID:   d.sessionID,
		SequenceNo:  d.sequence,
		VisibleText: d.visible.String(),
		CursorLine:  d.currentLine(),
		RecentLines: append([]string{}, d.recentLines...),
	}
	events := d.cliAdapter.Detect(snapshot)
	shouldHandle := len(events) > 0 && !d.pending
	var event adapter.DetectedEvent
	if shouldHandle {
		d.pending = true
		event = events[0]
	}
	d.mu.Unlock()

	if outputSequence <= 200 {
		_ = appendLocalOutput(localOutputRecord{
			SessionID:       d.sessionID,
			SequenceNo:      outputSequence,
			StreamType:      "stdout",
			ContentRedacted: localHistoryOutputContent(text),
			ContentHash:     "sha256:" + sha256String(text),
			CreatedAt:       time.Now().UTC().Format(time.RFC3339),
		})
	}
	if shouldHandle {
		go d.handleApproval(event)
	}
}

func (d *interactiveApprovalDetector) appendVisible(text string) {
	d.visible.WriteString(text)
	value := d.visible.String()
	const maxVisible = 16000
	if len(value) > maxVisible {
		d.visible.Reset()
		d.visible.WriteString(value[len(value)-maxVisible:])
	}
}

func (d *interactiveApprovalDetector) appendLines(text string) {
	for _, r := range text {
		switch r {
		case '\n':
			line := strings.TrimSpace(d.lineBuffer.String())
			d.lineBuffer.Reset()
			if line != "" {
				d.recentLines = append(d.recentLines, line)
				if len(d.recentLines) > 12 {
					d.recentLines = d.recentLines[len(d.recentLines)-12:]
				}
			}
		default:
			d.lineBuffer.WriteRune(r)
		}
	}
}

func (d *interactiveApprovalDetector) currentLine() string {
	if d.lineBuffer.Len() > 0 {
		return strings.TrimSpace(d.lineBuffer.String())
	}
	if len(d.recentLines) > 0 {
		return d.recentLines[len(d.recentLines)-1]
	}
	return ""
}

func (d *interactiveApprovalDetector) handleApproval(event adapter.DetectedEvent) {
	approvalID := "local_approval_" + sha256String(d.sessionID + ":" + event.PromptText)[:16]
	createdAt := time.Now().UTC().Format(time.RFC3339)
	approval := localApproval{
		ApprovalID:    approvalID,
		TenantID:      "local",
		DeviceID:      hostname(),
		SessionID:     d.sessionID,
		CLIType:       d.cliAdapter.Type(),
		EventType:     event.EventType,
		RiskLevel:     event.RiskLevel,
		PromptText:    event.PromptText,
		ContextBefore: event.ContextBefore,
		ExpiresAt:     time.Now().UTC().Add(5 * time.Minute).Format(time.RFC3339),
	}
	_ = upsertLocalApproval(localApprovalRecord{
		ApprovalID:    approvalID,
		SessionID:     d.sessionID,
		CLIType:       d.cliAdapter.Type(),
		EventType:     event.EventType,
		RiskLevel:     event.RiskLevel,
		PromptText:    event.PromptText,
		ContextBefore: event.ContextBefore,
		Status:        "waiting_decision",
		CreatedAt:     createdAt,
	})
	_ = upsertLocalSession(localSessionRecord{
		SessionID:         d.sessionID,
		Status:            "waiting_approval",
		LastOutputSummary: event.PromptText,
		PendingApprovals:  1,
	})

	decisionType := strings.TrimSpace(d.options.Decision)
	payload := d.options.Payload
	var err error
	if decisionType == "" {
		decisionType, payload, err = requestTrayDecision(approval, d.workingDir)
	}
	if err != nil {
		d.finishApprovalPending()
		return
	}
	decisionInput, err := d.cliAdapter.BuildDecisionInput(adapter.ApprovalEvent{
		EventType:     event.EventType,
		PromptText:    event.PromptText,
		ContextBefore: event.ContextBefore,
	}, adapter.Decision{Type: decisionType, Payload: payload})
	if err != nil {
		d.finishApprovalPending()
		return
	}

	d.mu.Lock()
	writer := d.writer
	snapshot := adapter.TerminalSnapshot{
		SessionID:   d.sessionID,
		SequenceNo:  d.sequence,
		VisibleText: d.visible.String(),
		CursorLine:  d.currentLine(),
		RecentLines: append([]string{}, d.recentLines...),
	}
	d.mu.Unlock()
	if writer == nil || !d.cliAdapter.IsPromptStillActive(snapshot, adapter.ApprovalEvent{EventType: event.EventType, PromptText: event.PromptText, ContextBefore: event.ContextBefore}) {
		d.finishApprovalPending()
		return
	}
	n, writeErr := writer.Write(decisionInput)
	result := "written"
	if writeErr != nil {
		result = "write_failed"
	}
	decidedAt := time.Now().UTC().Format(time.RFC3339)
	_ = appendLocalDecision(localDecisionRecord{
		ApprovalID:      approvalID,
		SessionID:       d.sessionID,
		DecisionType:    decisionType,
		PayloadRedacted: payload,
		BytesWritten:    n,
		Result:          result,
		CreatedAt:       decidedAt,
	})
	_ = upsertLocalApproval(localApprovalRecord{
		ApprovalID: approvalID,
		SessionID:  d.sessionID,
		Status:     "delivered",
		DecidedAt:  decidedAt,
	})
	_ = upsertLocalSession(localSessionRecord{
		SessionID:         d.sessionID,
		Status:            "running",
		LastOutputSummary: "approval " + decisionType + " delivered",
		PendingApprovals:  0,
	})
	d.finishApprovalPending()
}

func (d *interactiveApprovalDetector) finishApprovalPending() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.pending = false
}

type localApproval struct {
	ApprovalID    string `json:"approval_id"`
	TenantID      string `json:"tenant_id"`
	DeviceID      string `json:"device_id"`
	SessionID     string `json:"session_id"`
	CLIType       string `json:"cli_type"`
	EventType     string `json:"event_type"`
	RiskLevel     string `json:"risk_level"`
	PromptText    string `json:"prompt_text"`
	ContextBefore string `json:"context_before"`
	ExpiresAt     string `json:"expires_at"`
}

type trayApprovalRequest struct {
	Approval   localApproval `json:"approval"`
	WorkingDir string        `json:"working_dir"`
	Summary    string        `json:"summary"`
}

type trayDecisionResponse struct {
	DecisionType string `json:"decision_type"`
	Payload      string `json:"payload"`
	Result       string `json:"result"`
}

func requestTrayDecision(approval localApproval, workingDir string) (string, string, error) {
	body, err := json.Marshal(trayApprovalRequest{Approval: approval, WorkingDir: workingDir, Summary: approval.PromptText})
	if err != nil {
		return "", "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+trayListenAddress()+"/api/local/approvals/confirm", bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("%s: %s", resp.Status, string(respBody))
	}
	var decoded struct {
		Data trayDecisionResponse `json:"data"`
	}
	if err := json.Unmarshal(respBody, &decoded); err != nil {
		return "", "", err
	}
	if decoded.Data.DecisionType == "" {
		return "", "", errors.New("tray decision missing")
	}
	return decoded.Data.DecisionType, decoded.Data.Payload, nil
}

func ensureTrayRunning() {
	client := http.Client{Timeout: 500 * time.Millisecond}
	if trayHealthy(client) {
		return
	}
	exe, args, err := findLocalRuntimeCommand()
	if err != nil {
		return
	}
	cmd := exec.Command(exe, args...)
	applyHiddenWindow(cmd)
	if err := cmd.Start(); err != nil {
		return
	}
	for i := 0; i < 60; i++ {
		time.Sleep(250 * time.Millisecond)
		if trayHealthy(client) {
			return
		}
	}
}

func trayHealthy(client http.Client) bool {
	resp, err := client.Get("http://" + trayListenAddress() + "/healthz")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func findLocalRuntimeCommand() (string, []string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", nil, err
	}
	selfDir := filepath.Dir(self)
	desktopName := "gatepilot-agent-desktop"
	agentName := "gatepilot-agent"
	if runtime.GOOS == "windows" {
		desktopName += ".exe"
		agentName += ".exe"
	}
	desktopCandidates := []string{
		filepath.Join(selfDir, desktopName),
		filepath.Join(selfDir, "..", "gatepilot-agent-windows-amd64", desktopName),
	}
	for _, candidate := range desktopCandidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil, nil
		}
	}
	agentCandidates := []string{
		filepath.Join(selfDir, agentName),
		filepath.Join(selfDir, "..", "gatepilot-agent-windows-amd64", agentName),
	}
	for _, candidate := range agentCandidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, []string{"tray"}, nil
		}
	}
	return "", nil, fmt.Errorf("%s not found next to gp", desktopName)
}

func trayListenAddress() string {
	return getenv("GATEPILOT_AGENT_TRAY_ADDR", defaultTrayAddr)
}

func cleanTerminalText(value string) string {
	var out strings.Builder
	for i := 0; i < len(value); i++ {
		b := value[i]
		if b == '\x1b' {
			i = skipANSISequence(value, i)
			continue
		}
		switch b {
		case '\r':
			out.WriteByte('\n')
		case '\n', '\t':
			out.WriteByte(b)
		default:
			if b >= 32 {
				out.WriteByte(b)
			}
		}
	}
	return out.String()
}

func skipANSISequence(value string, escIndex int) int {
	next := escIndex + 1
	if next >= len(value) {
		return escIndex
	}
	switch value[next] {
	case '[':
		return skipCSISequence(value, next+1)
	case ']':
		return skipStringTerminatedSequence(value, next+1)
	case 'P', '^', '_', 'X':
		return skipStringTerminatedSequence(value, next+1)
	default:
		return next
	}
}

func skipCSISequence(value string, start int) int {
	for i := start; i < len(value); i++ {
		b := value[i]
		if b >= 0x40 && b <= 0x7e {
			return i
		}
	}
	return len(value) - 1
}

func skipStringTerminatedSequence(value string, start int) int {
	for i := start; i < len(value); i++ {
		switch value[i] {
		case '\a':
			return i
		case '\x1b':
			if i+1 < len(value) && value[i+1] == '\\' {
				return i + 1
			}
		}
	}
	return len(value) - 1
}

func commandLineForDisplay(args []string) string {
	if len(args) == 0 {
		return ""
	}
	parts := make([]string, 0, len(args))
	for _, arg := range args {
		if strings.ContainsAny(arg, " \t\"") {
			parts = append(parts, strconv.Quote(arg))
		} else {
			parts = append(parts, arg)
		}
	}
	return strings.Join(parts, " ")
}

func currentWorkingDir() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}

func hostname() string {
	name, err := os.Hostname()
	if err != nil || strings.TrimSpace(name) == "" {
		return "unknown"
	}
	return name
}

func sha256String(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func getenv(key string, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
