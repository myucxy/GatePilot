package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/getlantern/systray"
	"github.com/gorilla/websocket"
	"github.com/myucxy/gatepilot/agent/internal/adapter"
	"github.com/myucxy/gatepilot/agent/internal/localqueue"
)

const (
	version         = "0.1.0-dev"
	protocolVersion = "2026-04-01"
)

type agentConfig struct {
	DeviceID    string `json:"device_id"`
	DeviceToken string `json:"device_token"`
	ServerURL   string `json:"server_url"`
	ServerWSURL string `json:"server_ws_url"`
}

var deliveryDecisionWriter io.Writer = io.Discard
var configureStartupRegistration = setWindowsStartOnLogin

type runCLIOptions struct {
	CLIType     string
	CommandLine string
	CommandArgs []string
	LocalOnly   bool
	Decision    string
	Payload     string
	Popup       bool
}

func main() {
	if len(os.Args) == 1 {
		runAgentTray([]string{"--open-settings"})
		return
	}

	command := os.Args[1]

	// M0 只保留稳定命令入口，后续 register/run/daemon 会在这里扩展子命令。
	switch command {
	case "version":
		printVersion()
	case "register":
		registerDevice(os.Args[2:])
	case "create-session":
		createSession(os.Args[2:])
	case "detect-approval":
		detectApproval(os.Args[2:])
	case "ack-decision":
		ackDecision(os.Args[2:])
	case "supersede-approval":
		supersedeApproval(os.Args[2:])
	case "connect":
		connectAgent(os.Args[2:])
	case "local-ui":
		runLocalUI(os.Args[2:])
	case "tray":
		runAgentTray(os.Args[2:])
	case "history":
		printLocalHistory(os.Args[2:])
	case "reply":
		replyLocalSession(os.Args[2:])
	case "settings":
		configureAgentSettings(os.Args[2:])
	case "open-settings":
		openLocalSettingsUI()
	case "open-history":
		openLocalHistoryUI()
	case "status":
		printAgentStatus()
	case "login":
		loginAgentDesktop(os.Args[2:])
	case "logout":
		logoutAgentDesktop()
	case "offline":
		enableAgentOfflineMode()
	case "flush-queue":
		flushQueue(os.Args[2:])
	case "run":
		runManagedCLI(os.Args[2:])
	case "codex":
		runAIToolShortcut("codex", os.Args[2:])
	case "claude":
		runAIToolShortcut("claude", os.Args[2:])
	case "install-gp":
		installGPCommand(os.Args[2:])
	case "run-fake":
		runFakeCLI()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", command)
		os.Exit(2)
	}
}

func runManagedCLI(args []string) {
	options := parseRunCLIOptions(args)
	options.CLIType = adapter.NormalizeCLIType(options.CLIType)
	if shouldUseTerminalPassthrough(options) {
		runTerminalPassthroughCLI(options)
		return
	}
	cliAdapter := adapter.ForCLI(options.CLIType)
	localSessionID := "local_session_" + fmt.Sprintf("%d", time.Now().UnixNano())

	cmd := managedCLICommand(options)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var localHost *localSessionHost
	if options.LocalOnly {
		localHost, err = startLocalSessionHost(localSessionID, stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "local session control warning: %v\n", err)
		}
		wd := currentWorkingDir()
		_ = upsertLocalSession(localSessionRecord{
			SessionID:           localSessionID,
			CLIType:             options.CLIType,
			CommandLineRedacted: options.CommandLine,
			WorkingDir:          wd,
			WorkingDirHash:      "sha256:" + sha256String(wd),
			Status:              "running",
			StartedAt:           time.Now().UTC().Format(time.RFC3339),
			LastOutputSummary:   "local CLI session started",
			ControlAddr:         localHostAddress(localHost),
		})
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	event, outputText, remainingStdout, err := detectApprovalFromReaderWithRemainder(stdout, cliAdapter)
	if err != nil {
		_ = cmd.Process.Kill()
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if options.LocalOnly {
		approvalID := "local_approval_" + sha256String(localSessionID + ":" + event.PromptText)[:16]
		_ = appendLocalOutput(localOutputRecord{
			SessionID:       localSessionID,
			SequenceNo:      1,
			StreamType:      "stdout",
			ContentRedacted: localHistoryOutputContent(outputText),
			ContentHash:     "sha256:" + sha256String(outputText),
			CreatedAt:       time.Now().UTC().Format(time.RFC3339),
		})
		_ = upsertLocalApproval(localApprovalRecord{
			ApprovalID:    approvalID,
			SessionID:     localSessionID,
			CLIType:       options.CLIType,
			EventType:     event.EventType,
			RiskLevel:     event.RiskLevel,
			PromptText:    event.PromptText,
			ContextBefore: event.ContextBefore,
			Status:        "waiting_decision",
			CreatedAt:     time.Now().UTC().Format(time.RFC3339),
		})
		_ = upsertLocalSession(localSessionRecord{
			SessionID:         localSessionID,
			Status:            "waiting_approval",
			LastOutputSummary: event.PromptText,
			PendingApprovals:  1,
			ControlAddr:       localHostAddress(localHost),
		})
		ackResult, bytesWritten, decisionType, decisionPayload, err := confirmLocalApproval(stdin, cliAdapter, event, localUIOptions{
			DecisionType: options.Decision,
			Payload:      options.Payload,
			Popup:        options.Popup,
		}, os.Stdin, os.Stdout)
		if err != nil {
			_ = cmd.Process.Kill()
			fmt.Fprintf(os.Stderr, "local confirmation failed: %v\n", err)
			os.Exit(1)
		}
		decidedAt := time.Now().UTC().Format(time.RFC3339)
		_ = appendLocalDecision(localDecisionRecord{
			ApprovalID:      approvalID,
			SessionID:       localSessionID,
			DecisionType:    decisionType,
			PayloadRedacted: decisionPayload,
			BytesWritten:    bytesWritten,
			Result:          ackResult,
			CreatedAt:       decidedAt,
		})
		_ = upsertLocalApproval(localApprovalRecord{
			ApprovalID: approvalID,
			SessionID:  localSessionID,
			Status:     "delivered",
			DecidedAt:  decidedAt,
		})
		_ = upsertLocalSession(localSessionRecord{
			SessionID:         localSessionID,
			Status:            "running",
			LastOutputSummary: "approval " + decisionType + " delivered",
			PendingApprovals:  0,
			ControlAddr:       localHostAddress(localHost),
		})
		if remainingOutput, readErr := io.ReadAll(remainingStdout); readErr == nil && len(remainingOutput) > 0 {
			fmt.Print(string(remainingOutput))
			_ = appendLocalOutput(localOutputRecord{
				SessionID:       localSessionID,
				SequenceNo:      2,
				StreamType:      "stdout",
				ContentRedacted: localHistoryOutputContent(string(remainingOutput)),
				ContentHash:     "sha256:" + sha256String(string(remainingOutput)),
				CreatedAt:       time.Now().UTC().Format(time.RFC3339),
			})
		}
		waitErr := cmd.Wait()
		if localHost != nil {
			_ = localHost.Close()
		}
		exitStatus := "completed"
		summary := "local CLI completed"
		if waitErr != nil {
			exitStatus = "failed"
			summary = waitErr.Error()
		}
		_ = stdin.Close()
		_ = upsertLocalSession(localSessionRecord{
			SessionID:         localSessionID,
			Status:            exitStatus,
			EndedAt:           time.Now().UTC().Format(time.RFC3339),
			LastOutputSummary: summary,
			PendingApprovals:  0,
			ControlAddr:       "",
		})
		if waitErr != nil {
			fmt.Fprintf(os.Stderr, "local CLI exited after decision: %v\n", waitErr)
			os.Exit(1)
		}
		fmt.Println(mustJSON(map[string]any{
			"session_id":    localSessionID,
			"approval_id":   approvalID,
			"type":          "local_only.completed",
			"ack_result":    ackResult,
			"bytes_written": bytesWritten,
		}))
		return
	}

	config, err := loadAgentConfig()
	if err != nil || config.DeviceID == "" {
		_ = cmd.Process.Kill()
		fmt.Fprintln(os.Stderr, "agent is not registered; run register first or set GATEPILOT_AGENT_CONFIG")
		os.Exit(2)
	}

	sessionBody := mustMarshal(map[string]any{
		"device_id":             config.DeviceID,
		"cli_type":              options.CLIType,
		"command_line_redacted": options.CommandLine,
		"working_dir_hash":      "sha256:local",
		"last_output_summary":   "fake CLI session started",
	})
	sessionResp, err := postJSONWithToken(config.ServerURL+"/api/v1/agent/sessions", sessionBody, config.DeviceToken)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create managed session failed: %v\n", err)
		os.Exit(1)
	}
	sessionID := responseDataString(sessionResp, "session_id")
	if err := appendOutputChunk(config.ServerURL, config.DeviceID, config.DeviceToken, sessionID, 1, "stdout", outputText); err != nil {
		fmt.Fprintf(os.Stderr, "append managed output failed: %v\n", err)
		os.Exit(1)
	}

	queuedEvent := localqueue.ApprovalEvent{
		EventID:          "evt_" + fmt.Sprintf("%d", time.Now().UnixNano()),
		DeviceID:         config.DeviceID,
		SessionID:        sessionID,
		CLIType:          options.CLIType,
		EventType:        event.EventType,
		RiskLevel:        event.RiskLevel,
		PromptText:       event.PromptText,
		ContextBefore:    event.ContextBefore,
		IdempotencyKey:   approvalIdempotencyKey(config.DeviceID, sessionID, options.CLIType, event.PromptText, event.ContextBefore),
		SuggestedActions: event.SuggestedActions,
		ExpiresInSeconds: 300,
		CreatedAt:        time.Now().UTC(),
	}
	approvalResp, err := postQueuedApproval(config.ServerURL, config.DeviceToken, queuedEvent)
	if err != nil {
		if queueErr := enqueueApprovalForRetry(queuedEvent); queueErr != nil {
			fmt.Fprintf(os.Stderr, "create managed approval failed: %v; queue failed: %v\n", err, queueErr)
			os.Exit(1)
		}
		fmt.Println(mustJSON(map[string]any{
			"session_id": sessionID,
			"event_id":   queuedEvent.EventID,
			"queued":     true,
		}))
		return
	}

	deliveryDecisionWriter = stdin
	defer func() {
		deliveryDecisionWriter = io.Discard
	}()
	fmt.Println(mustJSON(map[string]any{
		"session_id":  sessionID,
		"approval_id": responseDataString(approvalResp, "approval_id"),
		"status":      "waiting_decision",
	}))
	connectAgent([]string{"--device-id", config.DeviceID, "--wait-delivery"})
	_ = stdin.Close()
	if err := cmd.Wait(); err != nil {
		fmt.Fprintf(os.Stderr, "managed CLI exited after decision: %v\n", err)
		os.Exit(1)
	}
	exitCode := 0
	if err := updateSessionStatus(config.ServerURL, config.DeviceID, config.DeviceToken, sessionID, "completed", &exitCode, "fake CLI completed"); err != nil {
		fmt.Fprintf(os.Stderr, "update managed session failed: %v\n", err)
		os.Exit(1)
	}
}

func parseRunCLIOptions(args []string) runCLIOptions {
	options := runCLIOptions{
		CLIType:     "custom",
		CommandLine: "gatepilot fake",
	}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--cli-type":
			if i+1 < len(args) {
				options.CLIType = args[i+1]
				i++
			}
		case "--local-only":
			options.LocalOnly = true
		case "--popup":
			options.Popup = true
		case "--decision":
			if i+1 < len(args) {
				options.Decision = args[i+1]
				i++
			}
		case "--payload":
			if i+1 < len(args) {
				options.Payload = args[i+1]
				i++
			}
		case "--":
			if i+1 < len(args) {
				options.CommandArgs = append([]string{}, args[i+1:]...)
				options.CommandLine = commandLineForDisplay(options.CommandArgs)
			}
			i = len(args)
		}
	}
	return options
}

func managedCLICommand(options runCLIOptions) *exec.Cmd {
	args := options.CommandArgs
	if len(args) == 0 || isFakeCLICommand(args[0]) {
		return exec.Command(os.Args[0], "run-fake")
	}
	return exec.Command(args[0], args[1:]...)
}

func isFakeCLICommand(name string) bool {
	base := strings.ToLower(strings.TrimSuffix(filepath.Base(name), filepath.Ext(name)))
	switch base {
	case "", "fake-ai-cli", "gatepilot-fake-ai-cli", "run-fake":
		return true
	default:
		return false
	}
}

func commandLineForDisplay(args []string) string {
	if len(args) == 0 {
		return "gatepilot fake"
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

func runAIToolShortcut(toolType string, args []string) {
	executable := toolType
	cliType := toolType
	if toolType == "claude" {
		cliType = "claude_code"
	}
	settings, err := loadAgentLocalSettings()
	if err == nil {
		for _, cfg := range configuredAITools(settings) {
			if cfg.ToolType == toolType && strings.TrimSpace(cfg.ExecutablePath) != "" {
				executable = cfg.ExecutablePath
				break
			}
		}
	}
	runArgs := []string{"--local-only", "--cli-type", cliType, "--", executable}
	runArgs = append(runArgs, args...)
	runManagedCLI(runArgs)
}

func shouldUseTerminalPassthrough(options runCLIOptions) bool {
	if len(options.CommandArgs) == 0 || isFakeCLICommand(options.CommandArgs[0]) {
		return false
	}
	switch adapter.NormalizeCLIType(options.CLIType) {
	case "codex", "claude_code":
		return true
	default:
		return false
	}
}

func runTerminalPassthroughCLI(options runCLIOptions) {
	if len(options.CommandArgs) == 0 {
		fmt.Fprintln(os.Stderr, "missing command")
		os.Exit(2)
	}
	toolType := aiToolTypeForCLI(options.CLIType)
	if toolType != "" {
		if err := ensureAIToolConfigured(toolType, options.CommandArgs[0]); err != nil {
			fmt.Fprintf(os.Stderr, "GatePilot 设置提示: %v\n", err)
		}
	}
	ensureTrayRunning()

	localSessionID := "local_session_" + fmt.Sprintf("%d", time.Now().UnixNano())
	wd := currentWorkingDir()
	_ = upsertLocalSession(localSessionRecord{
		SessionID:           localSessionID,
		CLIType:             options.CLIType,
		CommandLineRedacted: options.CommandLine,
		WorkingDir:          wd,
		WorkingDirHash:      "sha256:" + sha256String(wd),
		Status:              "running",
		StartedAt:           time.Now().UTC().Format(time.RFC3339),
		LastOutputSummary:   "本地 AI CLI 已启动",
	})

	cmd := exec.Command(options.CommandArgs[0], options.CommandArgs[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Dir = wd
	err := cmd.Run()
	exitStatus := "completed"
	summary := "本地 AI CLI 已结束"
	exitCode := 0
	if err != nil {
		exitStatus = "failed"
		summary = err.Error()
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}
	_ = upsertLocalSession(localSessionRecord{
		SessionID:         localSessionID,
		Status:            exitStatus,
		EndedAt:           time.Now().UTC().Format(time.RFC3339),
		LastOutputSummary: summary,
		PendingApprovals:  0,
	})
	if err != nil {
		os.Exit(exitCode)
	}
}

func aiToolTypeForCLI(cliType string) string {
	switch adapter.NormalizeCLIType(cliType) {
	case "codex":
		return "codex"
	case "claude_code":
		return "claude"
	default:
		return ""
	}
}

func ensureAIToolConfigured(toolType string, executable string) error {
	settings, err := loadAgentLocalSettings()
	if err != nil {
		return err
	}
	for _, cfg := range configuredAITools(settings) {
		if cfg.ToolType == toolType {
			return nil
		}
	}
	for _, cfg := range defaultAIToolConfigs() {
		if cfg.ToolType == toolType {
			cfg.ExecutablePath = executable
			settings.AITools = append(settings.AITools, cfg)
			return saveAgentLocalSettingsWithStartup(settings)
		}
	}
	return nil
}

func ensureTrayRunning() {
	client := http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Get("http://" + trayListenAddress() + "/healthz")
	if err == nil {
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return
		}
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(exe, "tray")
	applyHiddenWindow(cmd)
	if err := cmd.Start(); err != nil {
		return
	}
	for i := 0; i < 20; i++ {
		time.Sleep(150 * time.Millisecond)
		resp, err := client.Get("http://" + trayListenAddress() + "/healthz")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
	}
}

func installGPCommand(args []string) {
	target := ""
	binDir := ""
	noPath := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--target":
			if i+1 < len(args) {
				target = args[i+1]
				i++
			}
		case "--bin-dir":
			if i+1 < len(args) {
				binDir = args[i+1]
				i++
			}
		case "--no-path":
			noPath = true
		}
	}
	if target == "" {
		exe, err := os.Executable()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		target = exe
	}
	if binDir == "" {
		localAppData := getenv("LOCALAPPDATA", "")
		if localAppData == "" {
			home, _ := os.UserHomeDir()
			localAppData = filepath.Join(home, ".gatepilot")
		}
		binDir = filepath.Join(localAppData, "GatePilot", "bin")
	}
	if err := os.MkdirAll(binDir, 0700); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	shimPath := filepath.Join(binDir, "gp.cmd")
	shim := "@echo off\r\n\"" + target + "\" %*\r\n"
	if err := os.WriteFile(shimPath, []byte(shim), 0600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	pathUpdated := false
	if runtime.GOOS == "windows" && !noPath {
		if err := addUserPathDirectory(binDir); err != nil {
			fmt.Fprintf(os.Stderr, "warning: add PATH failed: %v\n", err)
		} else {
			pathUpdated = true
		}
	}
	fmt.Println(mustJSON(map[string]any{
		"type":         "gp.installed",
		"shim":         shimPath,
		"target":       target,
		"path_updated": pathUpdated,
		"usage":        "gp codex 或 gp claude",
	}))
}

func addUserPathDirectory(dir string) error {
	if runtime.GOOS != "windows" {
		return nil
	}
	script := `$ErrorActionPreference = "Stop"
$dir = $env:GATEPILOT_GP_BIN
$old = [Environment]::GetEnvironmentVariable("Path", "User")
if ([string]::IsNullOrWhiteSpace($old)) {
  $new = $dir
} else {
  $items = $old -split ';' | Where-Object { -not [string]::IsNullOrWhiteSpace($_) }
  $exists = $false
  foreach ($item in $items) {
    if ([string]::Equals($item.TrimEnd([char]'\'), $dir.TrimEnd([char]'\'), [System.StringComparison]::OrdinalIgnoreCase)) {
      $exists = $true
      break
    }
  }
  if ($exists) {
    $new = $old
  } else {
    $new = ($items + $dir) -join ';'
  }
}
[Environment]::SetEnvironmentVariable("Path", $new, "User")`
	cmd := exec.Command("powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", script)
	cmd.Env = append(os.Environ(), "GATEPILOT_GP_BIN="+dir)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

type agentLocalSettings struct {
	Mode                 string         `json:"mode"`
	StartOnLogin         bool           `json:"start_on_login"`
	NotificationEnabled  bool           `json:"notification_enabled"`
	NotificationStyle    string         `json:"notification_style"`
	HistoryRetentionDays int            `json:"history_retention_days"`
	CaptureOutputMode    string         `json:"capture_output_mode"`
	DefaultCLIType       string         `json:"default_cli_type"`
	ServerURL            string         `json:"server_url"`
	TenantID             string         `json:"tenant_id"`
	DeviceID             string         `json:"device_id"`
	ClientInstanceID     string         `json:"client_instance_id"`
	AITools              []aiToolConfig `json:"ai_tools"`
}

type agentLoginOptions struct {
	ServerURL        string `json:"server_url"`
	TenantID         string `json:"tenant_id"`
	DeviceID         string `json:"device_id"`
	ClientInstanceID string `json:"client_instance_id"`
}

type agentSettingsUpdate struct {
	Mode                 string
	StartOnLogin         *bool
	NotificationEnabled  *bool
	NotificationStyle    string
	HistoryRetentionDays int
	CaptureOutputMode    string
	DefaultCLIType       string
	ServerURL            string
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

type localSessionInputRequest struct {
	Text string `json:"text"`
}

type trayPendingApproval struct {
	Request  trayApprovalRequest
	Response chan trayDecisionResponse
}

type trayState struct {
	mu       sync.Mutex
	settings agentLocalSettings
	pending  *trayPendingApproval
}

func defaultAgentLocalSettings() agentLocalSettings {
	return agentLocalSettings{
		Mode:                 "offline",
		NotificationEnabled:  true,
		NotificationStyle:    "mini_window",
		HistoryRetentionDays: 30,
		CaptureOutputMode:    "summary_only",
		DefaultCLIType:       "custom",
	}
}

func loadAgentLocalSettings() (agentLocalSettings, error) {
	settings := defaultAgentLocalSettings()
	path, err := agentSettingsPath()
	if err != nil {
		return settings, err
	}
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return settings, nil
		}
		return settings, err
	}
	if err := json.Unmarshal(body, &settings); err != nil {
		return settings, err
	}
	return normalizeAgentLocalSettings(settings), nil
}

func saveAgentLocalSettings(settings agentLocalSettings) error {
	path, err := agentSettingsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	settings = normalizeAgentLocalSettings(settings)
	body, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, body, 0600)
}

func saveAgentLocalSettingsWithStartup(settings agentLocalSettings) error {
	previous, _ := loadAgentLocalSettings()
	if err := saveAgentLocalSettings(settings); err != nil {
		return err
	}
	if previous.StartOnLogin == settings.StartOnLogin {
		return nil
	}
	return configureStartupRegistration(settings.StartOnLogin)
}

func normalizeAgentLocalSettings(settings agentLocalSettings) agentLocalSettings {
	defaults := defaultAgentLocalSettings()
	if settings.Mode == "" {
		settings.Mode = defaults.Mode
	}
	switch settings.Mode {
	case "offline", "online":
	default:
		settings.Mode = defaults.Mode
	}
	if settings.NotificationStyle == "" {
		settings.NotificationStyle = defaults.NotificationStyle
	}
	switch settings.NotificationStyle {
	case "none", "toast", "mini_window", "modal_popup":
	default:
		settings.NotificationStyle = defaults.NotificationStyle
	}
	if settings.HistoryRetentionDays <= 0 {
		settings.HistoryRetentionDays = defaults.HistoryRetentionDays
	}
	if settings.CaptureOutputMode == "" {
		settings.CaptureOutputMode = defaults.CaptureOutputMode
	}
	switch settings.CaptureOutputMode {
	case "summary_only", "redacted_recent", "full_local_only":
	default:
		settings.CaptureOutputMode = defaults.CaptureOutputMode
	}
	if settings.DefaultCLIType == "" {
		settings.DefaultCLIType = defaults.DefaultCLIType
	} else {
		settings.DefaultCLIType = adapter.NormalizeCLIType(settings.DefaultCLIType)
	}
	settings.AITools = normalizeAIToolConfigs(settings.AITools)
	return settings
}

func agentSettingsPath() (string, error) {
	if path := os.Getenv("GATEPILOT_AGENT_SETTINGS"); path != "" {
		return path, nil
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "GatePilot", "settings.json"), nil
}

func setWindowsStartOnLogin(enabled bool) error {
	if runtime.GOOS != "windows" {
		return nil
	}
	const runKey = `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`
	const valueName = "GatePilot Agent"
	if !enabled {
		cmd := exec.Command("reg.exe", "delete", runKey, "/v", valueName, "/f")
		output, err := cmd.CombinedOutput()
		if err != nil && !strings.Contains(strings.ToLower(string(output)), "unable to find") {
			return fmt.Errorf("disable start on login: %v: %s", err, strings.TrimSpace(string(output)))
		}
		return nil
	}
	exePath, err := os.Executable()
	if err != nil {
		return err
	}
	commandLine := `"` + exePath + `" tray`
	cmd := exec.Command("reg.exe", "add", runKey, "/v", valueName, "/t", "REG_SZ", "/d", commandLine, "/f")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("enable start on login: %v: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func parseAgentLoginOptions(args []string) agentLoginOptions {
	options := agentLoginOptions{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--server-url":
			if i+1 < len(args) {
				options.ServerURL = args[i+1]
				i++
			}
		case "--tenant-id":
			if i+1 < len(args) {
				options.TenantID = args[i+1]
				i++
			}
		case "--device-id":
			if i+1 < len(args) {
				options.DeviceID = args[i+1]
				i++
			}
		case "--client-instance-id":
			if i+1 < len(args) {
				options.ClientInstanceID = args[i+1]
				i++
			}
		}
	}
	return options
}

func parseAgentSettingsUpdate(args []string) (agentSettingsUpdate, bool, error) {
	update := agentSettingsUpdate{}
	changed := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--mode":
			if i+1 >= len(args) {
				return update, changed, fmt.Errorf("--mode requires a value")
			}
			update.Mode = args[i+1]
			changed = true
			i++
		case "--start-on-login":
			if i+1 >= len(args) {
				return update, changed, fmt.Errorf("--start-on-login requires true or false")
			}
			value, err := parseBoolSetting(args[i+1])
			if err != nil {
				return update, changed, err
			}
			update.StartOnLogin = &value
			changed = true
			i++
		case "--notification-enabled":
			if i+1 >= len(args) {
				return update, changed, fmt.Errorf("--notification-enabled requires true or false")
			}
			value, err := parseBoolSetting(args[i+1])
			if err != nil {
				return update, changed, err
			}
			update.NotificationEnabled = &value
			changed = true
			i++
		case "--notification-style":
			if i+1 >= len(args) {
				return update, changed, fmt.Errorf("--notification-style requires a value")
			}
			update.NotificationStyle = args[i+1]
			changed = true
			i++
		case "--history-retention-days":
			if i+1 >= len(args) {
				return update, changed, fmt.Errorf("--history-retention-days requires a value")
			}
			if _, err := fmt.Sscanf(args[i+1], "%d", &update.HistoryRetentionDays); err != nil {
				return update, changed, fmt.Errorf("invalid --history-retention-days %q", args[i+1])
			}
			changed = true
			i++
		case "--capture-output-mode":
			if i+1 >= len(args) {
				return update, changed, fmt.Errorf("--capture-output-mode requires a value")
			}
			update.CaptureOutputMode = args[i+1]
			changed = true
			i++
		case "--default-cli-type":
			if i+1 >= len(args) {
				return update, changed, fmt.Errorf("--default-cli-type requires a value")
			}
			update.DefaultCLIType = args[i+1]
			changed = true
			i++
		case "--server-url":
			if i+1 >= len(args) {
				return update, changed, fmt.Errorf("--server-url requires a value")
			}
			update.ServerURL = args[i+1]
			changed = true
			i++
		default:
			return update, changed, fmt.Errorf("unknown settings option %q", args[i])
		}
	}
	return update, changed, nil
}

func configureAgentSettings(args []string) {
	update, changed, err := parseAgentSettingsUpdate(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "settings failed: %v\n", err)
		os.Exit(2)
	}
	settings, err := loadAgentLocalSettings()
	if err != nil {
		fmt.Fprintf(os.Stderr, "settings failed: %v\n", err)
		os.Exit(1)
	}
	if changed {
		settings = applyAgentSettingsUpdate(settings, update)
		if err := saveAgentLocalSettingsWithStartup(settings); err != nil {
			fmt.Fprintf(os.Stderr, "settings failed: %v\n", err)
			os.Exit(1)
		}
		settings = normalizeAgentLocalSettings(settings)
	}
	fmt.Println(mustJSON(map[string]any{
		"type":      "agent.settings",
		"settings":  settings,
		"logged_in": agentSettingsLoggedIn(settings),
	}))
}

func applyAgentSettingsUpdate(settings agentLocalSettings, update agentSettingsUpdate) agentLocalSettings {
	if update.Mode != "" {
		settings.Mode = update.Mode
	}
	if update.StartOnLogin != nil {
		settings.StartOnLogin = *update.StartOnLogin
	}
	if update.NotificationEnabled != nil {
		settings.NotificationEnabled = *update.NotificationEnabled
	}
	if update.NotificationStyle != "" {
		settings.NotificationStyle = update.NotificationStyle
	}
	if update.HistoryRetentionDays > 0 {
		settings.HistoryRetentionDays = update.HistoryRetentionDays
	}
	if update.CaptureOutputMode != "" {
		settings.CaptureOutputMode = update.CaptureOutputMode
	}
	if update.DefaultCLIType != "" {
		settings.DefaultCLIType = update.DefaultCLIType
	}
	if update.ServerURL != "" {
		settings.ServerURL = update.ServerURL
	}
	return normalizeAgentLocalSettings(settings)
}

func parseBoolSetting(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on", "enabled":
		return true, nil
	case "0", "false", "no", "off", "disabled":
		return false, nil
	default:
		return false, fmt.Errorf("invalid boolean setting %q", value)
	}
}

func loginAgentDesktop(args []string) {
	settings, err := configureAgentDesktopLogin(parseAgentLoginOptions(args))
	if err != nil {
		fmt.Fprintf(os.Stderr, "login failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(mustJSON(map[string]any{
		"type":      "agent.login_configured",
		"settings":  settings,
		"logged_in": agentSettingsLoggedIn(settings),
	}))
}

func printAgentStatus() {
	settings, err := loadAgentLocalSettings()
	if err != nil {
		fmt.Fprintf(os.Stderr, "status failed: %v\n", err)
		os.Exit(1)
	}
	state := &trayState{settings: settings}
	fmt.Println(mustJSON(map[string]any{
		"type": "agent.status",
		"data": localAgentStatus(state),
	}))
}

func logoutAgentDesktop() {
	settings, err := clearAgentDesktopLogin()
	if err != nil {
		fmt.Fprintf(os.Stderr, "logout failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(mustJSON(map[string]any{
		"type":      "agent.logged_out",
		"settings":  settings,
		"logged_in": false,
	}))
}

func enableAgentOfflineMode() {
	settings, err := setAgentOfflineMode()
	if err != nil {
		fmt.Fprintf(os.Stderr, "offline mode failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(mustJSON(map[string]any{
		"type":      "agent.offline_enabled",
		"settings":  settings,
		"logged_in": agentSettingsLoggedIn(settings),
	}))
}

func configureAgentDesktopLogin(options agentLoginOptions) (agentLocalSettings, error) {
	settings, err := loadAgentLocalSettings()
	if err != nil {
		return settings, err
	}
	config, _ := loadAgentConfig()
	if options.ServerURL == "" {
		options.ServerURL = firstNonEmptyLocal(settings.ServerURL, getenv("GATEPILOT_SERVER_URL", config.ServerURL))
	}
	if options.DeviceID == "" {
		options.DeviceID = firstNonEmptyLocal(settings.DeviceID, config.DeviceID)
	}
	if options.TenantID == "" {
		options.TenantID = settings.TenantID
	}
	if options.ServerURL == "" {
		return settings, fmt.Errorf("--server-url is required")
	}
	if options.DeviceID == "" {
		return settings, fmt.Errorf("--device-id is required")
	}
	if options.TenantID == "" {
		tenantID, err := tenantIDForDevice(options.ServerURL, options.DeviceID, deviceTokenFor(options.DeviceID))
		if err != nil {
			return settings, err
		}
		options.TenantID = tenantID
	}
	if options.ClientInstanceID == "" {
		clientID, err := registerAgentDesktopClient(options.ServerURL, options.TenantID, options.DeviceID)
		if err != nil {
			return settings, err
		}
		options.ClientInstanceID = clientID
	}
	settings.Mode = "online"
	settings.ServerURL = options.ServerURL
	settings.TenantID = options.TenantID
	settings.DeviceID = options.DeviceID
	settings.ClientInstanceID = options.ClientInstanceID
	if err := saveAgentLocalSettingsWithStartup(settings); err != nil {
		return settings, err
	}
	return normalizeAgentLocalSettings(settings), nil
}

func clearAgentDesktopLogin() (agentLocalSettings, error) {
	settings, err := loadAgentLocalSettings()
	if err != nil {
		return settings, err
	}
	settings.Mode = "offline"
	settings.TenantID = ""
	settings.DeviceID = ""
	settings.ClientInstanceID = ""
	if err := saveAgentLocalSettingsWithStartup(settings); err != nil {
		return settings, err
	}
	return normalizeAgentLocalSettings(settings), nil
}

func setAgentOfflineMode() (agentLocalSettings, error) {
	settings, err := loadAgentLocalSettings()
	if err != nil {
		return settings, err
	}
	settings.Mode = "offline"
	if err := saveAgentLocalSettingsWithStartup(settings); err != nil {
		return settings, err
	}
	return normalizeAgentLocalSettings(settings), nil
}

func agentSettingsLoggedIn(settings agentLocalSettings) bool {
	return strings.TrimSpace(settings.ServerURL) != "" &&
		strings.TrimSpace(settings.TenantID) != "" &&
		strings.TrimSpace(settings.DeviceID) != "" &&
		strings.TrimSpace(settings.ClientInstanceID) != ""
}

func localAgentStatus(state *trayState) map[string]any {
	settings := state.currentSettings()
	settingsPath, _ := agentSettingsPath()
	historyPath, _ := localHistoryPath()
	return map[string]any{
		"settings":      settings,
		"logged_in":     agentSettingsLoggedIn(settings),
		"offline":       settings.Mode != "online",
		"settings_path": settingsPath,
		"history_path":  historyPath,
		"tray_addr":     trayListenAddress(),
	}
}

func runAgentTray(args []string) {
	noUI := false
	readyFile := ""
	duration := 0
	openSettings := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--no-ui":
			noUI = true
		case "--open-settings":
			openSettings = true
		case "--ready-file":
			if i+1 < len(args) {
				readyFile = args[i+1]
				i++
			}
		case "--duration-seconds":
			if i+1 < len(args) {
				fmt.Sscanf(args[i+1], "%d", &duration)
				i++
			}
		}
	}

	settings, err := loadAgentLocalSettings()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load settings failed: %v\n", err)
		os.Exit(1)
	}
	state := &trayState{settings: settings}
	server, err := startTrayHTTPServer(state)
	if err != nil {
		fmt.Fprintf(os.Stderr, "start tray server failed: %v\n", err)
		os.Exit(1)
	}
	defer server.Shutdown(context.Background())
	startAIToolMonitor(state)
	if readyFile != "" {
		if err := os.WriteFile(readyFile, []byte("ready"), 0600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	if openSettings {
		go func() {
			time.Sleep(300 * time.Millisecond)
			if err := openDesktopClient("settings"); err != nil {
				fmt.Fprintf(os.Stderr, "open settings failed: %v\n", err)
			}
		}()
	}
	if noUI {
		fmt.Println(mustJSON(map[string]any{
			"type":    "tray.started",
			"addr":    trayListenAddress(),
			"mode":    settings.Mode,
			"no_ui":   true,
			"version": version,
		}))
		if duration > 0 {
			time.Sleep(time.Duration(duration) * time.Second)
			return
		}
		select {}
	}

	systray.Run(func() {
		setupTrayMenu(state)
	}, func() {
		_ = server.Shutdown(context.Background())
	})
}

func startTrayHTTPServer(state *trayState) (*http.Server, error) {
	listener, err := net.Listen("tcp", trayListenAddress())
	if err != nil {
		return nil, err
	}
	server := &http.Server{Handler: newTrayHTTPHandler(state)}
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "tray server stopped: %v\n", err)
		}
	}()
	return server, nil
}

func newTrayHTTPHandler(state *trayState) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/ui", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ui/settings", http.StatusFound)
	})
	mux.HandleFunc("/ui/history", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(localHistoryHTML()))
	})
	mux.HandleFunc("/ui/settings", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(localSettingsHTML()))
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeTrayJSON(w, map[string]any{"status": "ok", "mode": state.currentSettings().Mode})
	})
	mux.HandleFunc("/api/local/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writeTrayJSON(w, map[string]any{"data": localAgentStatus(state)})
	})
	mux.HandleFunc("/api/local/settings", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeTrayJSON(w, map[string]any{"data": state.currentSettings()})
		case http.MethodPost:
			var settings agentLocalSettings
			if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			settings = normalizeAgentLocalSettings(settings)
			if err := saveAgentLocalSettingsWithStartup(settings); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			state.setSettings(settings)
			writeTrayJSON(w, map[string]any{"data": settings})
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/local/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req agentLoginOptions
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		settings, err := configureAgentDesktopLogin(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		state.setSettings(settings)
		writeTrayJSON(w, map[string]any{"data": localAgentStatus(state)})
	})
	mux.HandleFunc("/api/local/logout", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		settings, err := clearAgentDesktopLogin()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		state.setSettings(settings)
		writeTrayJSON(w, map[string]any{"data": localAgentStatus(state)})
	})
	mux.HandleFunc("/api/local/offline", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		settings, err := setAgentOfflineMode()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		state.setSettings(settings)
		writeTrayJSON(w, map[string]any{"data": localAgentStatus(state)})
	})
	mux.HandleFunc("/api/local/ai-tools/defaults", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writeTrayJSON(w, map[string]any{"data": map[string]any{"items": defaultAIToolConfigs()}})
	})
	mux.HandleFunc("/api/local/ai-tools/sessions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		items, err := listAIToolSessions(state.currentSettings(), aiToolSessionFilter{
			ToolID: r.URL.Query().Get("tool_id"),
			Query:  r.URL.Query().Get("query"),
			Limit:  intQueryParam(r, "limit"),
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeTrayJSON(w, map[string]any{"data": map[string]any{"items": items}})
	})
	mux.HandleFunc("/api/local/ai-tool-session", func(w http.ResponseWriter, r *http.Request) {
		handleTrayAIToolSession(w, r, state)
	})
	mux.HandleFunc("/api/local/ai-tool-session/continue", func(w http.ResponseWriter, r *http.Request) {
		handleTrayAIToolSessionContinue(w, r, state)
	})
	mux.HandleFunc("/api/local/approvals/confirm", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req trayApprovalRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.Approval.ApprovalID == "" {
			req.Approval.ApprovalID = "local"
		}
		decision := state.confirmApproval(req)
		writeTrayJSON(w, map[string]any{"data": decision})
	})
	mux.HandleFunc("/api/local/sessions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		items, err := queryLocalSessions(localSessionFilter{
			CLIType: r.URL.Query().Get("cli_type"),
			Status:  r.URL.Query().Get("status"),
			Limit:   intQueryParam(r, "limit"),
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeTrayJSON(w, map[string]any{"data": map[string]any{"items": items}})
	})
	mux.HandleFunc("/api/local/sessions/", func(w http.ResponseWriter, r *http.Request) {
		handleTraySessionScoped(w, r)
	})
	return mux
}

func handleTrayAIToolSession(w http.ResponseWriter, r *http.Request, state *trayState) {
	toolID := r.URL.Query().Get("tool_id")
	sessionID := r.URL.Query().Get("session_id")
	if strings.TrimSpace(toolID) == "" || strings.TrimSpace(sessionID) == "" {
		http.Error(w, "tool_id and session_id are required", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodGet:
		detail, ok, err := aiToolSessionDetail(state.currentSettings(), toolID, sessionID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !ok {
			http.Error(w, "ai tool session not found", http.StatusNotFound)
			return
		}
		writeTrayJSON(w, map[string]any{"data": detail})
	case http.MethodDelete:
		result, ok, err := deleteAIToolSession(state.currentSettings(), toolID, sessionID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !ok {
			http.Error(w, "ai tool session not found", http.StatusNotFound)
			return
		}
		writeTrayJSON(w, map[string]any{"data": result})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func handleTrayAIToolSessionContinue(w http.ResponseWriter, r *http.Request, state *trayState) {
	toolID := r.URL.Query().Get("tool_id")
	sessionID := r.URL.Query().Get("session_id")
	if strings.TrimSpace(toolID) == "" || strings.TrimSpace(sessionID) == "" {
		http.Error(w, "tool_id and session_id are required", http.StatusBadRequest)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	result, ok, err := continueAIToolSession(state.currentSettings(), toolID, sessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "ai tool session not found", http.StatusNotFound)
		return
	}
	writeTrayJSON(w, map[string]any{"data": result})
}

func handleTraySessionScoped(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/local/sessions/"), "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	sessionID := parts[0]
	if len(parts) == 1 && r.Method == http.MethodGet {
		detail, ok, err := localSessionDetail(sessionID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !ok {
			http.Error(w, "session not found", http.StatusNotFound)
			return
		}
		writeTrayJSON(w, map[string]any{"data": detail})
		return
	}
	if len(parts) == 2 && parts[1] == "input" && r.Method == http.MethodPost {
		var req localSessionInputRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(req.Text) == "" {
			http.Error(w, "text is required", http.StatusBadRequest)
			return
		}
		if err := sendLocalSessionInput(sessionID, req.Text); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		writeTrayJSON(w, map[string]any{"data": map[string]any{"session_id": sessionID, "written": true}})
		return
	}
	http.NotFound(w, r)
}

func (s *trayState) currentSettings() agentLocalSettings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings
}

func (s *trayState) setSettings(settings agentLocalSettings) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settings = settings
}

func (s *trayState) setPending(req trayApprovalRequest) *trayPendingApproval {
	pending := &trayPendingApproval{Request: req, Response: make(chan trayDecisionResponse, 1)}
	s.mu.Lock()
	s.pending = pending
	s.mu.Unlock()
	return pending
}

func (s *trayState) completePending(decision trayDecisionResponse) bool {
	s.mu.Lock()
	pending := s.pending
	if pending != nil {
		s.pending = nil
	}
	s.mu.Unlock()
	if pending == nil {
		return false
	}
	pending.Response <- decision
	return true
}

func (s *trayState) confirmApproval(req trayApprovalRequest) trayDecisionResponse {
	settings := s.currentSettings()
	pending := s.setPending(req)
	if settings.NotificationEnabled && settings.NotificationStyle != "none" {
		go func() {
			decision, payload, err := showConfiguredNotification(settings, req)
			if err != nil {
				decision = "reject"
				payload = ""
			}
			s.completePending(trayDecisionResponse{DecisionType: decision, Payload: payload, Result: "selected"})
		}()
	}
	select {
	case decision := <-pending.Response:
		return decision
	case <-time.After(10 * time.Minute):
		return trayDecisionResponse{DecisionType: "reject", Result: "timeout"}
	}
}

func showConfiguredNotification(settings agentLocalSettings, req trayApprovalRequest) (string, string, error) {
	message := approvalPopupText(req.Approval)
	if req.WorkingDir != "" {
		message = "Directory: " + req.WorkingDir + "\n\n" + message
	}
	switch settings.NotificationStyle {
	case "mini_window", "toast":
		return windowsApprovalMiniWindow(message)
	default:
		decision, err := windowsApprovalPopup(message)
		return decision, "", err
	}
}

func setupTrayMenu(state *trayState) {
	systray.SetIcon(gatePilotTrayIcon())
	systray.SetTitle("GatePilot")
	systray.SetTooltip("GatePilot Agent")
	statusItem := systray.AddMenuItem("离线本地模式", "当前 GatePilot Agent 模式")
	statusItem.Disable()
	systray.AddSeparator()
	approveItem := systray.AddMenuItem("通过当前审批", "通过当前待处理审批")
	rejectItem := systray.AddMenuItem("拒绝当前审批", "拒绝当前待处理审批")
	systray.AddSeparator()
	toggleNotify := systray.AddMenuItem("关闭提醒", "切换本地审批提醒")
	toggleOffline := systray.AddMenuItem("切换离线/在线模式", "在已配置登录时切换离线或在线模式")
	toggleStartup := systray.AddMenuItem("开启开机启动", "切换 Windows 开机启动")
	openSettingsItem := systray.AddMenuItem("打开设置", "打开桌面客户端设置")
	openHistoryItem := systray.AddMenuItem("打开会话历史", "打开桌面客户端会话历史")
	historyItem := systray.AddMenuItem("显示历史路径", "输出本地历史文件路径")
	settingsItem := systray.AddMenuItem("显示设置路径", "输出设置文件路径")
	loginItem := systray.AddMenuItem("登录/切换账号", "打开桌面客户端登录设置")
	quitItem := systray.AddMenuItem("退出", "退出 GatePilot Agent")

	refresh := func() {
		settings := state.currentSettings()
		if settings.Mode == "online" {
			statusItem.SetTitle("在线模式")
		} else {
			statusItem.SetTitle("离线本地模式")
		}
		if settings.NotificationEnabled {
			toggleNotify.SetTitle("关闭提醒")
		} else {
			toggleNotify.SetTitle("开启提醒")
		}
		if settings.Mode == "online" {
			toggleOffline.SetTitle("切换为离线使用")
		} else {
			toggleOffline.SetTitle("切换为在线模式")
		}
		if settings.StartOnLogin {
			toggleStartup.SetTitle("关闭开机启动")
		} else {
			toggleStartup.SetTitle("开启开机启动")
		}
	}
	refresh()

	go func() {
		for range approveItem.ClickedCh {
			state.completePending(trayDecisionResponse{DecisionType: "approve", Result: "tray_menu"})
		}
	}()
	go func() {
		for range rejectItem.ClickedCh {
			state.completePending(trayDecisionResponse{DecisionType: "reject", Result: "tray_menu"})
		}
	}()
	go func() {
		for range toggleNotify.ClickedCh {
			settings := state.currentSettings()
			settings.NotificationEnabled = !settings.NotificationEnabled
			if err := saveAgentLocalSettingsWithStartup(settings); err == nil {
				state.setSettings(settings)
			}
			refresh()
		}
	}()
	go func() {
		for range toggleOffline.ClickedCh {
			settings := state.currentSettings()
			if settings.Mode == "online" {
				settings.Mode = "offline"
			} else if agentSettingsLoggedIn(settings) {
				settings.Mode = "online"
			}
			if err := saveAgentLocalSettingsWithStartup(settings); err == nil {
				state.setSettings(settings)
			}
			refresh()
		}
	}()
	go func() {
		for range toggleStartup.ClickedCh {
			settings := state.currentSettings()
			settings.StartOnLogin = !settings.StartOnLogin
			if err := saveAgentLocalSettingsWithStartup(settings); err == nil {
				state.setSettings(settings)
			}
			refresh()
		}
	}()
	go func() {
		for range openSettingsItem.ClickedCh {
			if err := openDesktopClient("settings"); err != nil {
				fmt.Fprintf(os.Stderr, "open settings failed: %v\n", err)
			}
		}
	}()
	go func() {
		for range openHistoryItem.ClickedCh {
			if err := openDesktopClient("history"); err != nil {
				fmt.Fprintf(os.Stderr, "open history failed: %v\n", err)
			}
		}
	}()
	go func() {
		for range historyItem.ClickedCh {
			if path, err := localHistoryPath(); err == nil {
				fmt.Println(path)
			}
		}
	}()
	go func() {
		for range settingsItem.ClickedCh {
			if path, err := agentSettingsPath(); err == nil {
				fmt.Println(path)
			}
		}
	}()
	go func() {
		for range loginItem.ClickedCh {
			if err := openDesktopClient("settings"); err != nil {
				fmt.Fprintf(os.Stderr, "open login settings failed: %v\n", err)
			}
		}
	}()
	go func() {
		<-quitItem.ClickedCh
		systray.Quit()
	}()
}

func gatePilotTrayIcon() []byte {
	const width = 16
	const height = 16
	const pixelBytes = width * height * 4
	const maskBytes = ((width + 31) / 32) * 4 * height
	const dibBytes = 40 + pixelBytes + maskBytes
	const imageOffset = 6 + 16

	var buffer bytes.Buffer
	_ = binary.Write(&buffer, binary.LittleEndian, uint16(0))
	_ = binary.Write(&buffer, binary.LittleEndian, uint16(1))
	_ = binary.Write(&buffer, binary.LittleEndian, uint16(1))
	buffer.WriteByte(width)
	buffer.WriteByte(height)
	buffer.WriteByte(0)
	buffer.WriteByte(0)
	_ = binary.Write(&buffer, binary.LittleEndian, uint16(1))
	_ = binary.Write(&buffer, binary.LittleEndian, uint16(32))
	_ = binary.Write(&buffer, binary.LittleEndian, uint32(dibBytes))
	_ = binary.Write(&buffer, binary.LittleEndian, uint32(imageOffset))

	_ = binary.Write(&buffer, binary.LittleEndian, uint32(40))
	_ = binary.Write(&buffer, binary.LittleEndian, int32(width))
	_ = binary.Write(&buffer, binary.LittleEndian, int32(height*2))
	_ = binary.Write(&buffer, binary.LittleEndian, uint16(1))
	_ = binary.Write(&buffer, binary.LittleEndian, uint16(32))
	_ = binary.Write(&buffer, binary.LittleEndian, uint32(0))
	_ = binary.Write(&buffer, binary.LittleEndian, uint32(pixelBytes+maskBytes))
	_ = binary.Write(&buffer, binary.LittleEndian, int32(0))
	_ = binary.Write(&buffer, binary.LittleEndian, int32(0))
	_ = binary.Write(&buffer, binary.LittleEndian, uint32(0))
	_ = binary.Write(&buffer, binary.LittleEndian, uint32(0))

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			inset := x > 1 && x < width-2 && y > 1 && y < height-2
			if inset {
				buffer.Write([]byte{0x42, 0xa5, 0x22, 0xff})
			} else {
				buffer.Write([]byte{0xf0, 0xf0, 0xf0, 0xff})
			}
		}
	}
	buffer.Write(make([]byte, maskBytes))
	return buffer.Bytes()
}

func requestTrayDecision(approval localApproval, workingDir string, output io.Writer) (string, string, error) {
	req := trayApprovalRequest{Approval: approval, WorkingDir: workingDir, Summary: approval.PromptText}
	body, err := json.Marshal(req)
	if err != nil {
		return "", "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+trayListenAddress()+"/api/local/approvals/confirm", bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(httpReq)
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
		return "", "", fmt.Errorf("tray decision missing")
	}
	if output != nil {
		fmt.Fprintln(output, mustJSON(map[string]any{
			"type":          "tray.decision_received",
			"decision_type": decoded.Data.DecisionType,
			"result":        decoded.Data.Result,
		}))
	}
	return decoded.Data.DecisionType, decoded.Data.Payload, nil
}

func trayListenAddress() string {
	return getenv("GATEPILOT_AGENT_TRAY_ADDR", "127.0.0.1:18731")
}

func writeTrayJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func localHistoryHTML() string {
	return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>GatePilot Agent History</title>
<style>
:root{color-scheme:light dark;font-family:Segoe UI,Arial,sans-serif;background:#f7f8fa;color:#1b1f24}
body{margin:0}
header{height:56px;display:flex;align-items:center;gap:14px;padding:0 18px;border-bottom:1px solid #d8dee4;background:#fff}
h1{font-size:18px;font-weight:650;margin:0}
main{display:grid;grid-template-columns:360px 1fr;min-height:calc(100vh - 57px)}
.list{border-right:1px solid #d8dee4;background:#fff;overflow:auto}
.filters{display:grid;grid-template-columns:1fr 1fr 72px;gap:8px;padding:12px;border-bottom:1px solid #d8dee4}
input,select,button,textarea{font:inherit;border:1px solid #c8d0d9;border-radius:6px;background:#fff;color:#1b1f24}
input,select{height:34px;padding:0 8px}
button{height:34px;padding:0 12px;cursor:pointer}
button.primary{background:#116329;color:#fff;border-color:#116329}
.session{display:block;width:100%;height:auto;text-align:left;border:0;border-bottom:1px solid #eef1f4;border-radius:0;padding:12px;background:#fff}
.session:hover,.session.active{background:#eef6ff}
.session strong{display:block;font-size:13px;margin-bottom:4px}
.meta{font-size:12px;color:#57606a;line-height:1.45;word-break:break-all}
.detail{padding:18px;overflow:auto}
.panel{border:1px solid #d8dee4;border-radius:8px;background:#fff;margin-bottom:14px}
.panel h2{font-size:14px;margin:0;padding:12px 14px;border-bottom:1px solid #eef1f4}
.panel .body{padding:12px 14px}
.grid{display:grid;grid-template-columns:160px 1fr;gap:8px;font-size:13px}
pre{white-space:pre-wrap;word-break:break-word;margin:0;font-size:12px;line-height:1.5}
.timeline{display:grid;gap:10px}
.item{border:1px solid #eef1f4;border-radius:6px;padding:10px}
.reply{display:flex;gap:8px;margin-top:12px}
.reply input{flex:1}
.empty{padding:24px;color:#57606a}
@media (max-width:800px){main{grid-template-columns:1fr}.list{border-right:0;border-bottom:1px solid #d8dee4;max-height:42vh}.filters{grid-template-columns:1fr 1fr}.filters button{grid-column:1 / -1}.grid{grid-template-columns:1fr}}
@media (prefers-color-scheme:dark){:root{background:#0d1117;color:#e6edf3}header,.list,.panel,.session,input,select,button,textarea{background:#161b22;color:#e6edf3;border-color:#30363d}.session{border-bottom-color:#30363d}.session:hover,.session.active{background:#1f2a36}.meta,.empty{color:#8b949e}.panel h2{border-bottom-color:#30363d}.item{border-color:#30363d}.primary{background:#238636!important;border-color:#238636!important}}
</style>
</head>
<body>
<header><h1>GatePilot Agent History</h1><span id="status" class="meta"></span></header>
<main>
<section class="list">
<div class="filters">
<select id="cli"><option value="">All CLI</option><option>custom</option><option>codex</option><option>claude</option><option>gemini</option><option>copilot</option><option>opencode</option></select>
<select id="state"><option value="">All status</option><option>running</option><option>waiting_approval</option><option>completed</option><option>failed</option><option>lost</option></select>
<button id="refresh">Refresh</button>
</div>
<div id="sessions" class="empty">Loading...</div>
</section>
<section class="detail" id="detail"><div class="empty">Select a session.</div></section>
</main>
<script>
const sessionsEl=document.getElementById('sessions');
const detailEl=document.getElementById('detail');
let selected='';
function text(value){return value===undefined||value===null||value===''?'-':String(value)}
function node(tag,cls,value){const el=document.createElement(tag);if(cls)el.className=cls;if(value!==undefined)el.textContent=value;return el}
async function loadStatus(){const r=await fetch('/api/local/status');const j=await r.json();document.getElementById('status').textContent=j.data.offline?'offline local':'online configured'}
async function loadSessions(){
 const params=new URLSearchParams();const cli=document.getElementById('cli').value;const state=document.getElementById('state').value;
 if(cli)params.set('cli_type',cli);if(state)params.set('status',state);params.set('limit','100');
 const r=await fetch('/api/local/sessions?'+params.toString());const j=await r.json();sessionsEl.innerHTML='';
 const items=j.data.items||[];if(!items.length){sessionsEl.className='empty';sessionsEl.textContent='No sessions.';return}
 sessionsEl.className='';
 for(const s of items){const b=node('button','session'+(s.session_id===selected?' active':''));b.onclick=()=>loadDetail(s.session_id);
  b.appendChild(node('strong','',s.session_id));b.appendChild(node('div','meta',s.cli_type+' / '+s.status+' / approvals '+s.pending_approval_count));
  b.appendChild(node('div','meta',s.working_dir||s.command_line_redacted||''));sessionsEl.appendChild(b)}
}
function panel(title,body){const p=node('div','panel');p.appendChild(node('h2','',title));const b=node('div','body');b.appendChild(body);p.appendChild(b);return p}
function kv(data){const g=node('div','grid');for(const [k,v] of Object.entries(data)){g.appendChild(node('div','meta',k));g.appendChild(node('div','',text(v)))}return g}
function timeline(items,kind){const box=node('div','timeline');if(!items.length){box.appendChild(node('div','meta','No '+kind+'.'));return box}
 for(const item of items){const x=node('div','item');const pre=node('pre','',JSON.stringify(item,null,2));x.appendChild(pre);box.appendChild(x)}return box}
async function loadDetail(id){
 selected=id;await loadSessions();const r=await fetch('/api/local/sessions/'+encodeURIComponent(id));const j=await r.json();const d=j.data;detailEl.innerHTML='';
 const s=d.session;detailEl.appendChild(panel('Session',kv({session_id:s.session_id,cli_type:s.cli_type,status:s.status,working_dir:s.working_dir,started_at:s.started_at,ended_at:s.ended_at,last_output_summary:s.last_output_summary})));
 const canReply=s.status==='running'||s.status==='waiting_approval';if(canReply){const wrap=node('div','');const row=node('div','reply');const input=node('input','');input.placeholder='Reply text';const send=node('button','primary','Send');send.onclick=async()=>{if(!input.value.trim())return;await fetch('/api/local/sessions/'+encodeURIComponent(id)+'/input',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({text:input.value})});input.value='';await loadDetail(id)};row.appendChild(input);row.appendChild(send);wrap.appendChild(row);detailEl.appendChild(panel('Reply',wrap))}
 detailEl.appendChild(panel('Output',timeline(d.output||[],'output')));
 detailEl.appendChild(panel('Approvals',timeline(d.approvals||[],'approvals')));
 detailEl.appendChild(panel('Decisions',timeline(d.decisions||[],'decisions')));
}
document.getElementById('refresh').onclick=loadSessions;document.getElementById('cli').onchange=loadSessions;document.getElementById('state').onchange=loadSessions;
loadStatus();loadSessions();
</script>
</body>
</html>`
}

func localSettingsHTML() string {
	return `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>GatePilot Agent Settings</title>
<style>
:root{color-scheme:light dark;font-family:Segoe UI,Arial,sans-serif;background:#f6f8fa;color:#1f2328}
body{margin:0}
header{height:56px;display:flex;align-items:center;justify-content:space-between;padding:0 20px;border-bottom:1px solid #d8dee4;background:#fff}
h1{font-size:18px;margin:0}
main{max-width:980px;margin:0 auto;padding:18px}
.nav{display:flex;gap:10px;align-items:center}
a{color:#0969da;text-decoration:none}
.panel{border:1px solid #d8dee4;border-radius:8px;background:#fff;margin-bottom:14px}
.panel h2{font-size:15px;margin:0;padding:12px 14px;border-bottom:1px solid #eef1f4}
.body{padding:14px}
.grid{display:grid;grid-template-columns:220px 1fr;gap:12px 14px;align-items:center}
label{font-size:13px;color:#57606a}
input,select,button{font:inherit;border:1px solid #c8d0d9;border-radius:6px;background:#fff;color:#1f2328}
input,select{height:34px;padding:0 8px;min-width:0}
input[type=checkbox]{height:auto;width:18px}
button{height:34px;padding:0 14px;cursor:pointer}
button.primary{background:#116329;color:#fff;border-color:#116329}
button.danger{background:#a40e26;color:#fff;border-color:#a40e26}
.actions{display:flex;gap:10px;flex-wrap:wrap;margin-top:14px}
.status{font-size:13px;color:#57606a}
.message{min-height:20px;margin-top:10px;font-size:13px}
.ok{color:#116329}.err{color:#a40e26}
code{font-family:Consolas,monospace;font-size:12px;word-break:break-all}
@media (max-width:760px){main{padding:12px}.grid{grid-template-columns:1fr}.nav{font-size:13px}}
@media (prefers-color-scheme:dark){:root{background:#0d1117;color:#e6edf3}header,.panel,input,select,button{background:#161b22;color:#e6edf3;border-color:#30363d}.panel h2{border-bottom-color:#30363d}label,.status{color:#8b949e}a{color:#58a6ff}.primary{background:#238636!important;border-color:#238636!important}.danger{background:#da3633!important;border-color:#da3633!important}}
</style>
</head>
<body>
<header>
<h1>GatePilot Agent 设置</h1>
<nav class="nav"><a href="/ui/history">会话历史</a><span id="mode" class="status"></span></nav>
</header>
<main>
<section class="panel">
<h2>本地模式与提醒</h2>
<div class="body">
<div class="grid">
<label for="modeInput">运行模式</label>
<select id="modeInput"><option value="offline">离线本地</option><option value="online">在线配置</option></select>
<label for="notifyInput">启用提醒</label>
<input id="notifyInput" type="checkbox">
<label for="styleInput">提醒样式</label>
<select id="styleInput"><option value="mini_window">右下角小窗口</option><option value="modal_popup">模态弹窗</option><option value="toast">Toast/小窗口</option><option value="none">不弹窗</option></select>
<label for="startupInput">开机启动</label>
<input id="startupInput" type="checkbox">
<label for="retentionInput">历史保留天数</label>
<input id="retentionInput" type="number" min="1" max="3650">
<label for="captureInput">输出捕获</label>
<select id="captureInput"><option value="summary_only">只保存摘要</option><option value="redacted_recent">保存脱敏近期输出</option><option value="full_local_only">完整本地保存</option></select>
<label for="cliInput">默认 CLI 类型</label>
<select id="cliInput"><option>custom</option><option>codex</option><option>claude</option><option>gemini</option><option>copilot</option><option>opencode</option></select>
<label for="serverInput">服务端地址</label>
<input id="serverInput" placeholder="http://127.0.0.1:8080">
</div>
<div class="actions"><button class="primary" id="save">保存设置</button><button id="reload">重新载入</button></div>
<div id="settingsMsg" class="message"></div>
</div>
</section>
<section class="panel">
<h2>登录与在线配置</h2>
<div class="body">
<div class="grid">
<label for="tenantInput">Tenant ID</label><input id="tenantInput">
<label for="deviceInput">Device ID</label><input id="deviceInput">
<label for="clientInput">Client Instance ID</label><input id="clientInput" placeholder="可留空，保存时自动注册">
<label>当前设置文件</label><code id="settingsPath"></code>
<label>当前历史文件</label><code id="historyPath"></code>
</div>
<div class="actions"><button class="primary" id="login">登录/绑定</button><button id="offline">切为离线</button><button class="danger" id="logout">退出登录</button></div>
<div id="loginMsg" class="message"></div>
</div>
</section>
</main>
<script>
let currentSettings=null;
const ids=['modeInput','notifyInput','styleInput','startupInput','retentionInput','captureInput','cliInput','serverInput','tenantInput','deviceInput','clientInput'];
function el(id){return document.getElementById(id)}
function msg(id,text,ok=true){const x=el(id);x.textContent=text;x.className='message '+(ok?'ok':'err')}
async function load(){
 const r=await fetch('/api/local/status');const j=await r.json();const d=j.data;const s=d.settings;currentSettings=s;
 el('mode').textContent=d.offline?'离线本地':'在线配置';
 el('modeInput').value=s.mode;el('notifyInput').checked=!!s.notification_enabled;el('styleInput').value=s.notification_style;
 el('startupInput').checked=!!s.start_on_login;el('retentionInput').value=s.history_retention_days;el('captureInput').value=s.capture_output_mode;
 el('cliInput').value=s.default_cli_type;el('serverInput').value=s.server_url||'';el('tenantInput').value=s.tenant_id||'';
 el('deviceInput').value=s.device_id||'';el('clientInput').value=s.client_instance_id||'';el('settingsPath').textContent=d.settings_path;el('historyPath').textContent=d.history_path;
}
function readSettings(){
 return {...currentSettings,
  mode:el('modeInput').value,start_on_login:el('startupInput').checked,notification_enabled:el('notifyInput').checked,
  notification_style:el('styleInput').value,history_retention_days:Number(el('retentionInput').value||30),
  capture_output_mode:el('captureInput').value,default_cli_type:el('cliInput').value,server_url:el('serverInput').value,
  tenant_id:el('tenantInput').value,device_id:el('deviceInput').value,client_instance_id:el('clientInput').value};
}
async function saveSettings(){
 const r=await fetch('/api/local/settings',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(readSettings())});
 if(!r.ok){msg('settingsMsg',await r.text(),false);return}
 msg('settingsMsg','已保存');await load();
}
async function login(){
 const body={server_url:el('serverInput').value,tenant_id:el('tenantInput').value,device_id:el('deviceInput').value,client_instance_id:el('clientInput').value};
 const r=await fetch('/api/local/login',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)});
 if(!r.ok){msg('loginMsg',await r.text(),false);return}
 msg('loginMsg','已登录/绑定');await load();
}
async function post(url,id,text){
 const r=await fetch(url,{method:'POST'});if(!r.ok){msg(id,await r.text(),false);return}msg(id,text);await load();
}
el('save').onclick=saveSettings;el('reload').onclick=load;el('login').onclick=login;
el('offline').onclick=()=>post('/api/local/offline','loginMsg','已切为离线');el('logout').onclick=()=>post('/api/local/logout','loginMsg','已退出登录');
load().catch(e=>msg('settingsMsg',String(e),false));
</script>
</body>
</html>`
}

func intQueryParam(r *http.Request, key string) int {
	value := strings.TrimSpace(r.URL.Query().Get(key))
	if value == "" {
		return 0
	}
	var parsed int
	if _, err := fmt.Sscanf(value, "%d", &parsed); err != nil {
		return 0
	}
	return parsed
}

func currentWorkingDir() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}

func printLocalHistory(args []string) {
	sessionID := ""
	filter := localSessionFilter{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--session-id":
			if i+1 < len(args) {
				sessionID = args[i+1]
				i++
			}
		case "--cli-type":
			if i+1 < len(args) {
				filter.CLIType = adapter.NormalizeCLIType(args[i+1])
				i++
			}
		case "--status":
			if i+1 < len(args) {
				filter.Status = args[i+1]
				i++
			}
		case "--limit":
			if i+1 < len(args) {
				_, _ = fmt.Sscanf(args[i+1], "%d", &filter.Limit)
				i++
			}
		}
	}
	if sessionID != "" {
		detail, ok, err := localSessionDetail(sessionID)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if !ok {
			fmt.Fprintf(os.Stderr, "session %s not found\n", sessionID)
			os.Exit(1)
		}
		fmt.Println(mustJSON(map[string]any{"data": detail}))
		return
	}
	items, err := queryLocalSessions(filter)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(mustJSON(map[string]any{"data": map[string]any{"items": items}}))
}

func replyLocalSession(args []string) {
	sessionID := ""
	text := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--session-id":
			if i+1 < len(args) {
				sessionID = args[i+1]
				i++
			}
		case "--text":
			if i+1 < len(args) {
				text = args[i+1]
				i++
			}
		}
	}
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(text) == "" {
		fmt.Fprintln(os.Stderr, "missing --session-id or --text")
		os.Exit(2)
	}
	if err := sendLocalSessionInput(sessionID, text); err != nil {
		fmt.Fprintf(os.Stderr, "reply failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(mustJSON(map[string]any{
		"type":       "agent.reply_sent",
		"session_id": sessionID,
	}))
}

func openLocalHistoryUI() {
	if err := openDesktopClient("history"); err != nil {
		fmt.Fprintf(os.Stderr, "open history failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(mustJSON(map[string]any{
		"type":   "agent.history_opened",
		"target": "desktop",
	}))
}

func openLocalSettingsUI() {
	if err := openDesktopClient("settings"); err != nil {
		fmt.Fprintf(os.Stderr, "open settings failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(mustJSON(map[string]any{
		"type":   "agent.settings_opened",
		"target": "desktop",
	}))
}

func openDesktopClient(view string) error {
	path, err := desktopClientPath()
	if err != nil {
		return err
	}
	args := []string{}
	switch strings.ToLower(strings.TrimSpace(view)) {
	case "history":
		args = append(args, "--history")
	case "settings":
		args = append(args, "--settings")
	}
	cmd := exec.Command(path, args...)
	return cmd.Start()
}

func desktopClientPath() (string, error) {
	if path := os.Getenv("GATEPILOT_AGENT_DESKTOP_EXE"); path != "" {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
		return "", fmt.Errorf("GATEPILOT_AGENT_DESKTOP_EXE points to missing file: %s", path)
	}
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	selfDir := filepath.Dir(self)
	wd, _ := os.Getwd()
	candidates := []string{
		filepath.Join(selfDir, "gatepilot-agent-desktop.exe"),
		filepath.Join(selfDir, "gatepilot-agent-desktop"),
		filepath.Join(selfDir, "..", "gatepilot-agent-windows-amd64", "gatepilot-agent-desktop.exe"),
		filepath.Join(wd, "dist", "gatepilot-agent-windows-amd64", "gatepilot-agent-desktop.exe"),
		filepath.Join(wd, "agent", "desktop", "build", "bin", "gatepilot-agent-desktop.exe"),
	}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("gatepilot-agent-desktop executable not found; build or install the desktop client first")
}

type localSessionHost struct {
	sessionID string
	addr      string
	server    *http.Server
	writer    io.Writer
	mu        sync.Mutex
}

func startLocalSessionHost(sessionID string, writer io.Writer) (*localSessionHost, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	host := &localSessionHost{
		sessionID: sessionID,
		addr:      listener.Addr().String(),
		writer:    writer,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/input", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req localSessionInputRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(req.Text) == "" {
			http.Error(w, "text is required", http.StatusBadRequest)
			return
		}
		host.mu.Lock()
		n, err := host.writer.Write([]byte(req.Text + "\r"))
		host.mu.Unlock()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = appendLocalDecision(localDecisionRecord{
			ApprovalID:      "manual_input",
			SessionID:       sessionID,
			DecisionType:    "reply",
			PayloadRedacted: req.Text,
			BytesWritten:    n,
			Result:          "manual_input",
			CreatedAt:       time.Now().UTC().Format(time.RFC3339),
		})
		writeTrayJSON(w, map[string]any{"data": map[string]any{"bytes_written": n}})
	})
	host.server = &http.Server{Handler: mux}
	go func() {
		if err := host.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "local session host stopped: %v\n", err)
		}
	}()
	return host, nil
}

func (h *localSessionHost) Close() error {
	if h == nil || h.server == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return h.server.Shutdown(ctx)
}

func localHostAddress(host *localSessionHost) string {
	if host == nil {
		return ""
	}
	return host.addr
}

func sendLocalSessionInput(sessionID string, text string) error {
	detail, ok, err := localSessionDetail(sessionID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("session not found")
	}
	session, ok := detail["session"].(localSessionRecord)
	if !ok {
		return fmt.Errorf("session detail invalid")
	}
	if session.Status != "running" && session.Status != "waiting_approval" {
		return fmt.Errorf("session is not running")
	}
	if session.ControlAddr == "" {
		return fmt.Errorf("session control is unavailable")
	}
	body, err := json.Marshal(localSessionInputRequest{Text: text})
	if err != nil {
		return err
	}
	resp, err := http.Post("http://"+session.ControlAddr+"/input", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s: %s", resp.Status, string(respBody))
	}
	return nil
}

func confirmLocalApproval(writer io.Writer, cliAdapter adapter.CLIAdapter, event adapter.DetectedEvent, options localUIOptions, reader io.Reader, output io.Writer) (string, int, string, string, error) {
	approval := localApproval{
		ApprovalID:    "local",
		TenantID:      "local",
		DeviceID:      hostname(),
		SessionID:     "local",
		CLIType:       cliAdapter.Type(),
		EventType:     event.EventType,
		RiskLevel:     event.RiskLevel,
		PromptText:    event.PromptText,
		ContextBefore: event.ContextBefore,
		ExpiresAt:     time.Now().UTC().Add(5 * time.Minute).Format(time.RFC3339),
	}
	notifyLocalApproval(output, approval)
	options.PopupText = approvalPopupText(approval)
	if strings.TrimSpace(options.DecisionType) == "" && !options.Popup {
		if decision, payload, err := requestTrayDecision(approval, currentWorkingDir(), output); err == nil {
			options.DecisionType = decision
			options.Payload = payload
			fmt.Fprintln(output, mustJSON(map[string]any{
				"type":          "local_ui.tray_decision",
				"decision_type": decision,
			}))
		}
	}
	decisionType, payload, err := localDecisionInput(options, reader, output)
	if err != nil {
		return "write_failed", 0, "", "", err
	}
	decisionInput, err := cliAdapter.BuildDecisionInput(adapter.ApprovalEvent{
		EventType:     event.EventType,
		PromptText:    event.PromptText,
		ContextBefore: event.ContextBefore,
	}, adapter.Decision{
		Type:    decisionType,
		Payload: payload,
	})
	if err != nil {
		return "write_failed", 0, decisionType, payload, err
	}
	n, err := writer.Write(decisionInput)
	if err != nil {
		return "write_failed", n, decisionType, payload, err
	}
	fmt.Fprintln(output, mustJSON(map[string]any{
		"type":          "local_only.decision_written",
		"decision_type": decisionType,
		"bytes_written": n,
	}))
	return "written", n, decisionType, payload, nil
}

func detectApprovalFromReader(reader io.Reader, cliAdapter adapter.CLIAdapter) (adapter.DetectedEvent, string, error) {
	event, output, _, err := detectApprovalFromReaderWithRemainder(reader, cliAdapter)
	return event, output, err
}

func detectApprovalFromReaderWithRemainder(reader io.Reader, cliAdapter adapter.CLIAdapter) (adapter.DetectedEvent, string, io.Reader, error) {
	buffered := bufio.NewReader(reader)
	recentLines := []string{}
	var visible strings.Builder
	sequence := int64(0)
	for {
		rawLine, err := buffered.ReadString('\n')
		if err != nil && rawLine == "" {
			if err == io.EOF {
				return adapter.DetectedEvent{}, visible.String(), buffered, fmt.Errorf("managed CLI prompt was not detected")
			}
			return adapter.DetectedEvent{}, visible.String(), buffered, err
		}
		line := strings.TrimRight(rawLine, "\r\n")
		fmt.Println(line)
		if visible.Len() > 0 {
			visible.WriteString("\n")
		}
		visible.WriteString(line)
		recentLines = append(recentLines, line)
		if len(recentLines) > 8 {
			recentLines = recentLines[len(recentLines)-8:]
		}
		sequence++
		events := cliAdapter.Detect(adapter.TerminalSnapshot{
			SequenceNo:  sequence,
			VisibleText: visible.String(),
			CursorLine:  line,
			RecentLines: recentLines,
		})
		if len(events) > 0 {
			return events[0], visible.String(), buffered, nil
		}
		if err == io.EOF {
			return adapter.DetectedEvent{}, visible.String(), buffered, fmt.Errorf("managed CLI prompt was not detected")
		}
	}
}

func flushQueue(args []string) {
	config, err := loadAgentConfig()
	if err != nil || config.DeviceID == "" {
		fmt.Fprintln(os.Stderr, "agent is not registered; run register first or set GATEPILOT_AGENT_CONFIG")
		os.Exit(2)
	}
	count, err := flushQueuedApprovals(config.ServerURL, config.DeviceToken)
	if err != nil {
		fmt.Fprintf(os.Stderr, "flush queue failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(mustJSON(map[string]any{
		"flushed": count,
	}))
}

func connectAgent(args []string) {
	deviceID := ""
	once := false
	waitDelivery := false
	readyFile := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--device-id":
			if i+1 < len(args) {
				deviceID = args[i+1]
				i++
			}
		case "--once":
			once = true
		case "--wait-delivery":
			waitDelivery = true
		case "--ready-file":
			if i+1 < len(args) {
				readyFile = args[i+1]
				i++
			}
		}
	}
	if deviceID == "" {
		fmt.Fprintln(os.Stderr, "missing --device-id")
		os.Exit(2)
	}

	wsURL := getenv("GATEPILOT_SERVER_WS_URL", "")
	if wsURL == "" {
		if config, err := loadAgentConfig(); err == nil && config.ServerWSURL != "" && config.DeviceID == deviceID {
			wsURL = config.ServerWSURL
		}
	}
	if wsURL == "" {
		serverURL := getenv("GATEPILOT_SERVER_URL", "http://127.0.0.1:8080")
		wsURL = httpURLToWS(serverURL) + "/ws/agent"
	}

	headers := http.Header{}
	if token := deviceTokenFor(deviceID); token != "" {
		headers.Set("Authorization", "Bearer "+token)
	}
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer conn.Close()

	traceID := "tr_agent_" + fmt.Sprintf("%d", time.Now().UnixNano())
	if err := conn.WriteJSON(newWSEnvelope("agent.hello", traceID, map[string]any{
		"device_id":        deviceID,
		"agent_version":    version,
		"protocol_version": protocolVersion,
		"platform":         runtime.GOOS,
		"capabilities": map[string]any{
			"pty":         runtime.GOOS != "windows",
			"conpty":      runtime.GOOS == "windows",
			"local_store": "sqlite",
		},
	})); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	var connected map[string]any
	if err := conn.ReadJSON(&connected); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if !waitDelivery {
		fmt.Println(mustJSON(connected))
	}
	if readyFile != "" {
		if err := os.WriteFile(readyFile, []byte("ready"), 0600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}

	if serverURL := serverURLForDevice(deviceID); serverURL != "" {
		if _, err := flushQueuedApprovals(serverURL, deviceTokenFor(deviceID)); err != nil {
			fmt.Fprintf(os.Stderr, "queue flush warning: %v\n", err)
		}
	}

	if err := conn.WriteJSON(newWSEnvelope("agent.heartbeat", traceID, map[string]any{
		"active_sessions":   0,
		"local_queue_depth": approvalQueueDepth(),
		"last_error":        nil,
	})); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if once {
		return
	}
	if waitDelivery {
		waitForDelivery(conn, traceID)
		return
	}

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		if err := conn.WriteJSON(newWSEnvelope("agent.heartbeat", traceID, map[string]any{
			"active_sessions":   0,
			"local_queue_depth": approvalQueueDepth(),
			"last_error":        nil,
		})); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}

func waitForDelivery(conn *websocket.Conn, traceID string) {
	for {
		var msg struct {
			Type    string          `json:"type"`
			Payload json.RawMessage `json:"payload"`
		}
		if err := conn.ReadJSON(&msg); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if msg.Type != "approval.decision.deliver" {
			continue
		}

		var delivery struct {
			DeliveryID   string  `json:"delivery_id"`
			ApprovalID   string  `json:"approval_id"`
			SessionID    string  `json:"session_id"`
			DecisionType string  `json:"decision_type"`
			Payload      *string `json:"payload"`
		}
		if err := json.Unmarshal(msg.Payload, &delivery); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		decisionInput, err := adapter.ForCLI("custom").BuildDecisionInput(adapter.ApprovalEvent{}, adapter.Decision{
			Type:    deliveryInputType(delivery.DecisionType),
			Payload: stringValue(delivery.Payload),
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		bytesWritten := len(decisionInput)
		ackResult := "written"
		if deliveryDecisionWriter != nil {
			n, err := deliveryDecisionWriter.Write(decisionInput)
			bytesWritten = n
			if err != nil {
				ackResult = "write_failed"
			}
		}
		ackPayload := map[string]any{
			"delivery_id": delivery.DeliveryID,
			"approval_id": delivery.ApprovalID,
			"session_id":  delivery.SessionID,
			"ack_result":  ackResult,
			"detail": map[string]any{
				"source":        "agent-websocket",
				"decision_type": delivery.DecisionType,
				"bytes_written": bytesWritten,
			},
		}
		if err := conn.WriteJSON(newWSEnvelope("approval.decision.ack", traceID, ackPayload)); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(mustJSON(map[string]any{
			"delivery_id": delivery.DeliveryID,
			"approval_id": delivery.ApprovalID,
			"session_id":  delivery.SessionID,
			"ack_result":  ackResult,
		}))
		return
	}
}

func deliveryInputType(decisionType string) string {
	switch decisionType {
	case "policy_approve":
		return "approve"
	case "policy_reject", "timeout_reject":
		return "reject"
	default:
		return decisionType
	}
}

type localUIOptions struct {
	DeviceID         string
	TenantID         string
	ClientInstanceID string
	DecisionType     string
	Payload          string
	Popup            bool
	PopupText        string
	Once             bool
	ReadyFile        string
	TimeoutSeconds   int
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

type localClientEvent struct {
	Type       string
	ApprovalID string
}

func runLocalUI(args []string) {
	options := parseLocalUIOptions(args)
	config, _ := loadAgentConfig()
	if options.DeviceID == "" {
		options.DeviceID = config.DeviceID
	}
	if options.DeviceID == "" {
		fmt.Fprintln(os.Stderr, "missing --device-id and no registered agent config found")
		os.Exit(2)
	}
	serverURL := getenv("GATEPILOT_SERVER_URL", config.ServerURL)
	if serverURL == "" {
		serverURL = "http://127.0.0.1:8080"
	}
	deviceToken := deviceTokenFor(options.DeviceID)
	if options.TenantID == "" {
		tenantID, err := tenantIDForDevice(serverURL, options.DeviceID, deviceToken)
		if err != nil {
			fmt.Fprintf(os.Stderr, "resolve tenant failed: %v\n", err)
			os.Exit(1)
		}
		options.TenantID = tenantID
	}
	if options.ClientInstanceID == "" {
		clientID, err := registerAgentDesktopClient(serverURL, options.TenantID, options.DeviceID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "register local UI failed: %v\n", err)
			os.Exit(1)
		}
		options.ClientInstanceID = clientID
	}
	localEvents, closeLocalEvents, err := startLocalClientNotifications(serverURL, options.TenantID, options.ClientInstanceID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "local notification websocket warning: %v\n", err)
	} else {
		defer closeLocalEvents()
	}
	if options.ReadyFile != "" {
		if err := os.WriteFile(options.ReadyFile, []byte("ready"), 0600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	fmt.Println(mustJSON(map[string]any{
		"type":               "local_ui.connected",
		"tenant_id":          options.TenantID,
		"device_id":          options.DeviceID,
		"client_instance_id": options.ClientInstanceID,
		"client_type":        "agent_desktop",
	}))

	seen := map[string]bool{}
	for {
		approval, err := waitForLocalApproval(serverURL, options.TenantID, options.DeviceID, deviceToken, seen, options.TimeoutSeconds, localEvents)
		if err != nil {
			fmt.Fprintf(os.Stderr, "wait local approval failed: %v\n", err)
			os.Exit(1)
		}
		seen[approval.ApprovalID] = true
		notifyLocalApproval(os.Stdout, approval)
		decisionOptions := options
		decisionOptions.PopupText = approvalPopupText(approval)
		decisionType, payload, err := localDecisionInput(decisionOptions, os.Stdin, os.Stdout)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		respBody, err := submitLocalApprovalDecision(serverURL, approval.ApprovalID, options.ClientInstanceID, decisionType, payload)
		if err != nil {
			fmt.Fprintf(os.Stderr, "submit local decision failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(mustJSON(map[string]any{
			"type":               "local_ui.decision_submitted",
			"approval_id":        approval.ApprovalID,
			"session_id":         approval.SessionID,
			"decision_type":      decisionType,
			"client_instance_id": options.ClientInstanceID,
			"client_type":        "agent_desktop",
			"delivery_id":        responseDataString(respBody, "delivery_id"),
		}))
		if options.Once {
			return
		}
	}
}

func parseLocalUIOptions(args []string) localUIOptions {
	options := localUIOptions{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--device-id":
			if i+1 < len(args) {
				options.DeviceID = args[i+1]
				i++
			}
		case "--tenant-id":
			if i+1 < len(args) {
				options.TenantID = args[i+1]
				i++
			}
		case "--client-instance-id":
			if i+1 < len(args) {
				options.ClientInstanceID = args[i+1]
				i++
			}
		case "--decision":
			if i+1 < len(args) {
				options.DecisionType = args[i+1]
				i++
			}
		case "--payload":
			if i+1 < len(args) {
				options.Payload = args[i+1]
				i++
			}
		case "--popup":
			options.Popup = true
		case "--once", "--confirm-once":
			options.Once = true
		case "--ready-file":
			if i+1 < len(args) {
				options.ReadyFile = args[i+1]
				i++
			}
		case "--timeout-seconds":
			if i+1 < len(args) {
				fmt.Sscanf(args[i+1], "%d", &options.TimeoutSeconds)
				i++
			}
		}
	}
	return options
}

func tenantIDForDevice(serverURL string, deviceID string, token string) (string, error) {
	body, err := getJSONWithToken(serverURL+"/api/v1/devices/"+url.PathEscape(deviceID), token)
	if err != nil {
		return "", err
	}
	return responseDataString(body, "tenant_id"), nil
}

func registerAgentDesktopClient(serverURL string, tenantID string, deviceID string) (string, error) {
	body := mustMarshal(map[string]any{
		"tenant_id":    tenantID,
		"client_type":  "agent_desktop",
		"device_id":    deviceID,
		"display_name": hostname() + " Agent",
		"app_version":  version,
		"platform":     runtime.GOOS,
	})
	respBody, err := postJSONWithHeaders(serverURL+"/api/v1/client-instances", body, "", map[string]string{
		"Idempotency-Key": "agent-desktop-" + deviceID,
	})
	if err != nil {
		return "", err
	}
	clientID := responseDataString(respBody, "client_instance_id")
	if clientID == "" {
		return "", fmt.Errorf("client_instance_id missing in response")
	}
	return clientID, nil
}

func startLocalClientNotifications(serverURL string, tenantID string, clientInstanceID string) (<-chan localClientEvent, func(), error) {
	wsURL := httpURLToWS(serverURL) + "/ws/client?tenant_id=" + url.QueryEscape(tenantID) + "&client_instance_id=" + url.QueryEscape(clientInstanceID)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		return nil, nil, err
	}
	events := make(chan localClientEvent, 8)
	go func() {
		defer close(events)
		defer conn.Close()
		for {
			var msg struct {
				Type    string          `json:"type"`
				Payload json.RawMessage `json:"payload"`
			}
			if err := conn.ReadJSON(&msg); err != nil {
				return
			}
			event := localClientEvent{Type: msg.Type}
			var payload struct {
				ApprovalID string `json:"approval_id"`
			}
			if err := json.Unmarshal(msg.Payload, &payload); err == nil {
				event.ApprovalID = payload.ApprovalID
			}
			select {
			case events <- event:
			default:
			}
		}
	}()
	return events, func() { _ = conn.Close() }, nil
}

func waitForLocalApproval(serverURL string, tenantID string, deviceID string, token string, seen map[string]bool, timeoutSeconds int, events <-chan localClientEvent) (localApproval, error) {
	deadline := time.Time{}
	if timeoutSeconds > 0 {
		deadline = time.Now().Add(time.Duration(timeoutSeconds) * time.Second)
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		items, err := listWaitingLocalApprovals(serverURL, tenantID, deviceID, token)
		if err != nil {
			return localApproval{}, err
		}
		for _, item := range items {
			if !seen[item.ApprovalID] {
				return item, nil
			}
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			return localApproval{}, fmt.Errorf("timed out waiting for local approval")
		}
		select {
		case _, ok := <-events:
			if !ok {
				events = nil
			}
		case <-ticker.C:
		}
	}
}

func listWaitingLocalApprovals(serverURL string, tenantID string, deviceID string, token string) ([]localApproval, error) {
	body, err := getJSONWithToken(serverURL+"/api/v1/tenants/"+url.PathEscape(tenantID)+"/approvals?status=waiting_decision", token)
	if err != nil {
		return nil, err
	}
	var response struct {
		Data struct {
			Items []localApproval `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, err
	}
	filtered := []localApproval{}
	for _, item := range response.Data.Items {
		if item.DeviceID == deviceID {
			filtered = append(filtered, item)
		}
	}
	return filtered, nil
}

func notifyLocalApproval(writer io.Writer, approval localApproval) {
	fmt.Fprintln(writer, mustJSON(map[string]any{
		"type":           "local_ui.approval_notification",
		"approval_id":    approval.ApprovalID,
		"session_id":     approval.SessionID,
		"device_id":      approval.DeviceID,
		"risk_level":     approval.RiskLevel,
		"event_type":     approval.EventType,
		"prompt_text":    approval.PromptText,
		"context_before": approval.ContextBefore,
		"expires_at":     approval.ExpiresAt,
	}))
}

func approvalPopupText(approval localApproval) string {
	parts := []string{
		"GatePilot needs your confirmation.",
		"",
		"Action: " + firstNonEmptyLocal(approval.EventType, "approval"),
		"Risk: " + firstNonEmptyLocal(approval.RiskLevel, "unknown"),
	}
	if approval.PromptText != "" {
		parts = append(parts, "", approval.PromptText)
	}
	if approval.ContextBefore != "" {
		parts = append(parts, "", "Context:", approval.ContextBefore)
	}
	parts = append(parts, "", "Choose Yes to approve, or No to reject.")
	return strings.Join(parts, "\n")
}

func firstNonEmptyLocal(value string, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func localDecisionInput(options localUIOptions, reader io.Reader, writer io.Writer) (string, string, error) {
	decisionType := strings.TrimSpace(options.DecisionType)
	if decisionType == "" && options.Popup {
		popupDecision, err := windowsApprovalPopup(options.PopupText)
		if err != nil {
			fmt.Fprintf(writer, "popup warning: %v\n", err)
		} else {
			decisionType = popupDecision
			fmt.Fprintln(writer, mustJSON(map[string]any{
				"type":          "local_ui.popup_decision",
				"decision_type": decisionType,
			}))
		}
	}
	if decisionType == "" {
		fmt.Fprint(writer, "Decision [approve/reject/reply]: ")
		line, err := readDecisionLine(reader)
		if err != nil {
			return "", "", err
		}
		decisionType = line
	}
	switch decisionType {
	case "approve", "reject", "reply":
	default:
		return "", "", fmt.Errorf("unsupported local decision %q", decisionType)
	}
	return decisionType, options.Payload, nil
}

func windowsApprovalPopup(message string) (string, error) {
	if override := strings.TrimSpace(os.Getenv("GATEPILOT_AGENT_POPUP_DECISION")); override != "" {
		switch override {
		case "approve", "reject":
			return override, nil
		default:
			return "", fmt.Errorf("unsupported popup override %q", override)
		}
	}
	if runtime.GOOS != "windows" {
		return "", fmt.Errorf("windows popup is only available on Windows")
	}
	if strings.TrimSpace(message) == "" {
		message = "GatePilot needs your confirmation.\n\nChoose Yes to approve, or No to reject."
	}
	script := `$ErrorActionPreference = "Stop"
Add-Type -AssemblyName System.Windows.Forms
$form = New-Object System.Windows.Forms.Form
$form.TopMost = $true
$form.ShowInTaskbar = $false
$form.WindowState = [System.Windows.Forms.FormWindowState]::Minimized
$form.Load.Add({ $form.Hide() })
$form.Show()
$result = [System.Windows.Forms.MessageBox]::Show($form, $env:GATEPILOT_POPUP_TEXT, "GatePilot Approval", [System.Windows.Forms.MessageBoxButtons]::YesNo, [System.Windows.Forms.MessageBoxIcon]::Warning, [System.Windows.Forms.MessageBoxDefaultButton]::Button2)
$form.Dispose()
if ($result -eq [System.Windows.Forms.DialogResult]::Yes) { "approve" } else { "reject" }`
	cmd := exec.Command("powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-STA", "-Command", script)
	cmd.Env = append(os.Environ(), "GATEPILOT_POPUP_TEXT="+message)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%v: %s", err, strings.TrimSpace(string(output)))
	}
	decision := strings.TrimSpace(string(output))
	switch decision {
	case "approve", "reject":
		return decision, nil
	default:
		return "", fmt.Errorf("unexpected popup result %q", decision)
	}
}

func windowsApprovalMiniWindow(message string) (string, string, error) {
	if override := strings.TrimSpace(os.Getenv("GATEPILOT_AGENT_POPUP_DECISION")); override != "" {
		switch override {
		case "approve", "reject":
			return override, "", nil
		default:
			return "", "", fmt.Errorf("unsupported popup override %q", override)
		}
	}
	if runtime.GOOS != "windows" {
		return "", "", fmt.Errorf("windows mini window is only available on Windows")
	}
	if strings.TrimSpace(message) == "" {
		message = "GatePilot needs your confirmation."
	}
	script := `$ErrorActionPreference = "Stop"
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
Add-Type -AssemblyName Microsoft.VisualBasic
$form = New-Object System.Windows.Forms.Form
$form.Text = "GatePilot Approval"
$form.Width = 420
$form.Height = 250
$form.TopMost = $true
$form.FormBorderStyle = [System.Windows.Forms.FormBorderStyle]::FixedDialog
$form.MaximizeBox = $false
$form.MinimizeBox = $false
$screen = [System.Windows.Forms.Screen]::PrimaryScreen.WorkingArea
$form.StartPosition = [System.Windows.Forms.FormStartPosition]::Manual
$form.Location = New-Object System.Drawing.Point(($screen.Right - $form.Width - 16), ($screen.Bottom - $form.Height - 16))
$label = New-Object System.Windows.Forms.Label
$label.AutoSize = $false
$label.Left = 16
$label.Top = 16
$label.Width = 372
$label.Height = 145
$label.Text = $env:GATEPILOT_POPUP_TEXT
$label.Font = New-Object System.Drawing.Font("Segoe UI", 9)
$form.Controls.Add($label)
$script:decision = "reject"
$script:payload = ""
$approve = New-Object System.Windows.Forms.Button
$approve.Text = "通过"
$approve.Width = 92
$approve.Height = 30
$approve.Left = 96
$approve.Top = 176
$approve.Add_Click({ $script:decision = "approve"; $form.Close() })
$form.Controls.Add($approve)
$reject = New-Object System.Windows.Forms.Button
$reject.Text = "拒绝"
$reject.Width = 92
$reject.Height = 30
$reject.Left = 196
$reject.Top = 176
$reject.Add_Click({ $script:decision = "reject"; $form.Close() })
$form.Controls.Add($reject)
$reply = New-Object System.Windows.Forms.Button
$reply.Text = "其他"
$reply.Width = 92
$reply.Height = 30
$reply.Left = 296
$reply.Top = 176
$reply.Add_Click({
  $text = [Microsoft.VisualBasic.Interaction]::InputBox("输入要回复给 CLI 的内容", "GatePilot Reply", "")
  if ($text.Length -gt 0) { $script:decision = "reply"; $script:payload = $text; $form.Close() }
})
$form.Controls.Add($reply)
$form.AcceptButton = $approve
$form.CancelButton = $reject
[void]$form.ShowDialog()
[pscustomobject]@{ decision_type = $script:decision; payload = $script:payload } | ConvertTo-Json -Compress`
	cmd := exec.Command("powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-STA", "-Command", script)
	cmd.Env = append(os.Environ(), "GATEPILOT_POPUP_TEXT="+message)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", "", fmt.Errorf("%v: %s", err, strings.TrimSpace(string(output)))
	}
	var result struct {
		DecisionType string `json:"decision_type"`
		Payload      string `json:"payload"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		return "", "", err
	}
	switch result.DecisionType {
	case "approve", "reject", "reply":
		return result.DecisionType, result.Payload, nil
	default:
		return "", "", fmt.Errorf("unexpected mini window result %q", result.DecisionType)
	}
}

func submitLocalApprovalDecision(serverURL string, approvalID string, clientInstanceID string, decisionType string, payload string) ([]byte, error) {
	body := mustMarshal(map[string]any{
		"decision_type": decisionType,
		"payload":       payload,
	})
	return postJSONWithHeaders(serverURL+"/api/v1/approvals/"+url.PathEscape(approvalID)+"/decision", body, "", map[string]string{
		"Idempotency-Key":      "agent-local-" + approvalID + "-" + fmt.Sprintf("%d", time.Now().UnixNano()),
		"X-Client-Instance-Id": clientInstanceID,
	})
}

func ackDecision(args []string) {
	approvalID := ""
	deliveryID := ""
	sessionID := ""
	ackResult := "written"
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--approval-id":
			if i+1 < len(args) {
				approvalID = args[i+1]
				i++
			}
		case "--delivery-id":
			if i+1 < len(args) {
				deliveryID = args[i+1]
				i++
			}
		case "--session-id":
			if i+1 < len(args) {
				sessionID = args[i+1]
				i++
			}
		case "--ack-result":
			if i+1 < len(args) {
				ackResult = args[i+1]
				i++
			}
		}
	}
	if approvalID == "" || deliveryID == "" || sessionID == "" {
		fmt.Fprintln(os.Stderr, "missing --approval-id, --delivery-id or --session-id")
		os.Exit(2)
	}

	serverURL := getenv("GATEPILOT_SERVER_URL", "http://127.0.0.1:8080")
	payload := map[string]any{
		"approval_id": approvalID,
		"delivery_id": deliveryID,
		"session_id":  sessionID,
		"ack_result":  ackResult,
		"detail": map[string]any{
			"source": "fake-cli",
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// 决策 ACK 先走 HTTP 占位链路，后续替换为 Agent WebSocket 的 approval.decision.ack 消息。
	respBody, err := postJSONWithToken(serverURL+"/api/v1/agent/approval-acks", body, deviceTokenFor(""))
	if err != nil {
		fmt.Fprintf(os.Stderr, "ack decision failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(respBody))
}

func supersedeApproval(args []string) {
	approvalID := ""
	sessionID := ""
	reason := "local_input"
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--approval-id":
			if i+1 < len(args) {
				approvalID = args[i+1]
				i++
			}
		case "--session-id":
			if i+1 < len(args) {
				sessionID = args[i+1]
				i++
			}
		case "--reason":
			if i+1 < len(args) {
				reason = args[i+1]
				i++
			}
		}
	}
	if approvalID == "" || sessionID == "" {
		fmt.Fprintln(os.Stderr, "missing --approval-id or --session-id")
		os.Exit(2)
	}

	serverURL := getenv("GATEPILOT_SERVER_URL", "http://127.0.0.1:8080")
	payload := map[string]any{
		"approval_id": approvalID,
		"session_id":  sessionID,
		"reason":      reason,
		"detail": map[string]any{
			"source": "local-terminal",
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	respBody, err := postJSONWithToken(serverURL+"/api/v1/agent/approval-supersedes", body, deviceTokenFor(""))
	if err != nil {
		fmt.Fprintf(os.Stderr, "supersede approval failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(respBody))
}

func printVersion() {
	fmt.Printf("GatePilot Agent %s\n", version)
	fmt.Printf("Protocol %s\n", protocolVersion)
	fmt.Printf("Runtime %s/%s\n", runtime.GOOS, runtime.GOARCH)
}

func runFakeCLI() {
	// fake CLI 用于端到端联调，不依赖真实 AI CLI，输出必须和 testdata 样本保持一致。
	fmt.Println("GatePilot fake AI CLI")
	fmt.Println("permission_request: allow command execution? [approve/reject/reply]")
	fmt.Println("waiting_for_input")
	decision, err := readDecisionLine(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if decision == "" {
		fmt.Fprintln(os.Stderr, "empty fake CLI decision")
		os.Exit(1)
	}
	fmt.Printf("received_decision: %s\n", decision)
}

func readDecisionLine(reader io.Reader) (string, error) {
	buffered := bufio.NewReader(reader)
	var input strings.Builder
	for {
		b, err := buffered.ReadByte()
		if err != nil {
			if input.Len() > 0 {
				break
			}
			return "", err
		}
		if b == '\r' || b == '\n' {
			break
		}
		input.WriteByte(b)
	}
	return strings.TrimSpace(input.String()), nil
}

func detectApproval(args []string) {
	deviceID := ""
	sessionID := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--device-id":
			if i+1 < len(args) {
				deviceID = args[i+1]
				i++
			}
		case "--session-id":
			if i+1 < len(args) {
				sessionID = args[i+1]
				i++
			}
		}
	}
	if deviceID == "" || sessionID == "" {
		fmt.Fprintln(os.Stderr, "missing --device-id or --session-id")
		os.Exit(2)
	}

	serverURL := getenv("GATEPILOT_SERVER_URL", "http://127.0.0.1:8080")
	cliType := adapter.NormalizeCLIType("custom")
	cliAdapter := adapter.ForCLI(cliType)
	detected := cliAdapter.Detect(adapter.TerminalSnapshot{
		SessionID:   sessionID,
		SequenceNo:  1,
		VisibleText: "GatePilot fake AI CLI\npermission_request: allow command execution? [approve/reject/reply]\nwaiting_for_input",
		CursorLine:  "waiting_for_input",
		RecentLines: []string{
			"GatePilot fake AI CLI",
			"permission_request: allow command execution? [approve/reject/reply]",
			"waiting_for_input",
		},
	})
	if len(detected) == 0 {
		fmt.Fprintln(os.Stderr, "fake approval prompt was not detected")
		os.Exit(1)
	}
	event := detected[0]
	payload := map[string]any{
		"device_id":          deviceID,
		"session_id":         sessionID,
		"cli_type":           cliType,
		"event_type":         event.EventType,
		"risk_level":         event.RiskLevel,
		"prompt_text":        event.PromptText,
		"context_before":     event.ContextBefore,
		"idempotency_key":    approvalIdempotencyKey(deviceID, sessionID, cliType, event.PromptText, event.ContextBefore),
		"suggested_actions":  event.SuggestedActions,
		"expires_in_seconds": 300,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// 审批检测先走 HTTP 占位链路，后续替换为 Agent WebSocket 的 approval.detected 消息。
	respBody, err := postJSONWithToken(serverURL+"/api/v1/agent/approvals", body, deviceTokenFor(deviceID))
	if err != nil {
		fmt.Fprintf(os.Stderr, "detect approval failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(respBody))
}

func approvalIdempotencyKey(deviceID, sessionID, cliType, promptText, contextBefore string) string {
	stableInputs := struct {
		DeviceID      string `json:"device_id"`
		SessionID     string `json:"session_id"`
		CliType       string `json:"cli_type"`
		PromptText    string `json:"prompt_text"`
		ContextBefore string `json:"context_before"`
	}{
		DeviceID:      deviceID,
		SessionID:     sessionID,
		CliType:       cliType,
		PromptText:    promptText,
		ContextBefore: contextBefore,
	}
	body, err := json.Marshal(stableInputs)
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func createSession(args []string) {
	deviceID := ""
	cliType := "custom"
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--device-id":
			if i+1 < len(args) {
				deviceID = args[i+1]
				i++
			}
		case "--cli-type":
			if i+1 < len(args) {
				cliType = args[i+1]
				i++
			}
		}
	}
	if deviceID == "" {
		fmt.Fprintln(os.Stderr, "missing --device-id")
		os.Exit(2)
	}

	serverURL := getenv("GATEPILOT_SERVER_URL", "http://127.0.0.1:8080")
	payload := map[string]any{
		"device_id":             deviceID,
		"cli_type":              cliType,
		"command_line_redacted": "gatepilot fake",
		"working_dir_hash":      "sha256:local",
		"last_output_summary":   "fake CLI session started",
	}

	body, err := json.Marshal(payload)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// 会话创建先走 HTTP 占位链路，后续替换为 Agent WebSocket 的 session.created 消息。
	respBody, err := postJSONWithToken(serverURL+"/api/v1/agent/sessions", body, deviceTokenFor(deviceID))
	if err != nil {
		fmt.Fprintf(os.Stderr, "create session failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(respBody))
}

func registerDevice(args []string) {
	code := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--activation-code" && i+1 < len(args) {
			code = args[i+1]
			i++
		}
	}
	if code == "" {
		fmt.Fprintln(os.Stderr, "missing --activation-code")
		os.Exit(2)
	}

	serverURL := getenv("GATEPILOT_SERVER_URL", "http://127.0.0.1:8080")
	payload := map[string]any{
		"activation_code":  code,
		"device_name":      hostname(),
		"platform":         runtime.GOOS,
		"arch":             runtime.GOARCH,
		"agent_version":    version,
		"protocol_version": protocolVersion,
		"capabilities": map[string]any{
			"pty":         runtime.GOOS != "windows",
			"conpty":      runtime.GOOS == "windows",
			"local_store": "sqlite",
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// 注册动作只通过服务端 API 完成，后续会把返回的 device_token 写入系统安全存储。
	client := http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(serverURL+"/api/v1/agent/register", "application/json", bytes.NewReader(body))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		fmt.Fprintf(os.Stderr, "register failed: %s\n%s\n", resp.Status, string(respBody))
		os.Exit(1)
	}
	if err := saveRegisteredConfig(serverURL, respBody); err != nil {
		fmt.Fprintf(os.Stderr, "save config failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(respBody))
}

func postJSONWithToken(url string, body []byte, token string) ([]byte, error) {
	return postJSONWithHeaders(url, body, token, nil)
}

func postJSONWithHeaders(url string, body []byte, token string, headers map[string]string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	client := http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s\n%s", resp.Status, string(respBody))
	}
	return respBody, nil
}

func getJSONWithToken(url string, token string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s\n%s", resp.Status, string(respBody))
	}
	return respBody, nil
}

func postQueuedApproval(serverURL string, token string, event localqueue.ApprovalEvent) ([]byte, error) {
	body, err := json.Marshal(approvalEventPayload(event))
	if err != nil {
		return nil, err
	}
	return postJSONWithToken(serverURL+"/api/v1/agent/approvals", body, token)
}

func approvalEventPayload(event localqueue.ApprovalEvent) map[string]any {
	expiresIn := event.ExpiresInSeconds
	if expiresIn <= 0 {
		expiresIn = 300
	}
	return map[string]any{
		"device_id":          event.DeviceID,
		"session_id":         event.SessionID,
		"cli_type":           event.CLIType,
		"event_type":         event.EventType,
		"risk_level":         event.RiskLevel,
		"prompt_text":        event.PromptText,
		"context_before":     event.ContextBefore,
		"idempotency_key":    event.IdempotencyKey,
		"suggested_actions":  event.SuggestedActions,
		"expires_in_seconds": expiresIn,
	}
}

func enqueueApprovalForRetry(event localqueue.ApprovalEvent) error {
	queue, err := approvalQueue()
	if err != nil {
		return err
	}
	return queue.EnqueueApproval(event)
}

func flushQueuedApprovals(serverURL string, token string) (int, error) {
	queue, err := approvalQueue()
	if err != nil {
		return 0, err
	}
	items, err := queue.ListApprovals()
	if err != nil {
		return 0, err
	}
	flushed := 0
	for _, item := range items {
		if _, err := postQueuedApproval(serverURL, token, item); err != nil {
			return flushed, err
		}
		if err := queue.RemoveApproval(item.EventID); err != nil {
			return flushed, err
		}
		flushed++
	}
	return flushed, nil
}

func approvalQueueDepth() int {
	queue, err := approvalQueue()
	if err != nil {
		return 0
	}
	items, err := queue.ListApprovals()
	if err != nil {
		return 0
	}
	return len(items)
}

func approvalQueue() (localqueue.Queue, error) {
	path, err := localqueue.DefaultPath()
	if err != nil {
		return localqueue.Queue{}, err
	}
	return localqueue.New(path), nil
}

func appendOutputChunk(serverURL string, deviceID string, deviceToken string, sessionID string, sequenceNo int64, streamType string, content string) error {
	payload := map[string]any{
		"device_id":        deviceID,
		"session_id":       sessionID,
		"sequence_no":      sequenceNo,
		"stream_type":      streamType,
		"content_redacted": content,
		"content_hash":     "sha256:" + sha256String(content),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = postJSONWithToken(serverURL+"/api/v1/agent/output-chunks", body, deviceToken)
	return err
}

func updateSessionStatus(serverURL string, deviceID string, deviceToken string, sessionID string, status string, exitCode *int, summary string) error {
	payload := map[string]any{
		"device_id":           deviceID,
		"session_id":          sessionID,
		"status":              status,
		"last_output_summary": summary,
	}
	if exitCode != nil {
		payload["exit_code"] = *exitCode
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = postJSONWithToken(serverURL+"/api/v1/agent/session-updates", body, deviceToken)
	return err
}

func mustMarshal(value any) []byte {
	body, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return body
}

func responseDataString(body []byte, key string) string {
	var response struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return ""
	}
	if value, ok := response.Data[key].(string); ok {
		return value
	}
	return ""
}

func saveRegisteredConfig(serverURL string, responseBody []byte) error {
	var response struct {
		Data agentConfig `json:"data"`
	}
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return err
	}
	response.Data.ServerURL = serverURL
	return saveAgentConfig(response.Data)
}

func saveAgentConfig(config agentConfig) error {
	path, err := agentConfigPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	body, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, body, 0600)
}

func loadAgentConfig() (agentConfig, error) {
	path, err := agentConfigPath()
	if err != nil {
		return agentConfig{}, err
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return agentConfig{}, err
	}
	var config agentConfig
	if err := json.Unmarshal(body, &config); err != nil {
		return agentConfig{}, err
	}
	return config, nil
}

func agentConfigPath() (string, error) {
	if path := os.Getenv("GATEPILOT_AGENT_CONFIG"); path != "" {
		return path, nil
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "GatePilot", "agent.json"), nil
}

func deviceTokenFor(deviceID string) string {
	if token := os.Getenv("GATEPILOT_DEVICE_TOKEN"); token != "" {
		return token
	}
	config, err := loadAgentConfig()
	if err != nil {
		return ""
	}
	if deviceID == "" || config.DeviceID == deviceID {
		return config.DeviceToken
	}
	return ""
}

func serverURLForDevice(deviceID string) string {
	if serverURL := os.Getenv("GATEPILOT_SERVER_URL"); serverURL != "" {
		return serverURL
	}
	config, err := loadAgentConfig()
	if err != nil {
		return ""
	}
	if deviceID == "" || config.DeviceID == deviceID {
		return config.ServerURL
	}
	return ""
}

func hostname() string {
	name, err := os.Hostname()
	if err != nil || name == "" {
		return "GatePilot Agent"
	}
	return name
}

func newWSEnvelope(messageType string, traceID string, payload map[string]any) map[string]any {
	return map[string]any{
		"type":           messageType,
		"message_id":     "msg_" + fmt.Sprintf("%d", time.Now().UnixNano()),
		"trace_id":       traceID,
		"sent_at":        time.Now().UTC().Format(time.RFC3339),
		"schema_version": protocolVersion,
		"payload":        payload,
	}
}

func httpURLToWS(url string) string {
	switch {
	case strings.HasPrefix(url, "https://"):
		return "wss://" + strings.TrimPrefix(url, "https://")
	case strings.HasPrefix(url, "http://"):
		return "ws://" + strings.TrimPrefix(url, "http://")
	default:
		return url
	}
}

func mustJSON(value any) string {
	body, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(body)
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func sha256String(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
