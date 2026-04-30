package adapter

import (
	"errors"
	"strings"
)

type TerminalSnapshot struct {
	SessionID   string
	SequenceNo  int64
	VisibleText string
	CursorLine  string
	RecentLines []string
}

type DetectedEvent struct {
	EventType            string
	RiskLevel            string
	PromptText           string
	ContextBefore        string
	SuggestedActions     []string
	DefaultTimeoutAction string
}

type ApprovalEvent struct {
	EventType     string
	PromptText    string
	ContextBefore string
}

type Decision struct {
	Type    string
	Payload string
}

type CLIAdapter interface {
	Type() string
	Detect(snapshot TerminalSnapshot) []DetectedEvent
	BuildDecisionInput(event ApprovalEvent, decision Decision) ([]byte, error)
	IsPromptStillActive(snapshot TerminalSnapshot, event ApprovalEvent) bool
}

type ruleAdapter struct {
	cliType       string
	promptMarkers []string
	approveInput  string
	rejectInput   string
	replySuffix   string
}

func ForCLI(cliType string) CLIAdapter {
	switch NormalizeCLIType(cliType) {
	case "codex":
		return ruleAdapter{cliType: "codex", promptMarkers: []string{"approve", "reject", "permission", "command"}, approveInput: "y\r", rejectInput: "n\r", replySuffix: "\r"}
	case "claude_code":
		return ruleAdapter{cliType: "claude_code", promptMarkers: []string{"do you want to proceed", "yes", "no"}, approveInput: "1\r", rejectInput: "2\r", replySuffix: "\r"}
	case "opencode":
		return ruleAdapter{cliType: "opencode", promptMarkers: []string{"allow", "deny", "permission"}, approveInput: "a\r", rejectInput: "d\r", replySuffix: "\r"}
	case "copilot":
		return ruleAdapter{cliType: "copilot", promptMarkers: []string{"approve", "reject", "github copilot"}, approveInput: "approve\r", rejectInput: "reject\r", replySuffix: "\r"}
	case "gemini":
		return ruleAdapter{cliType: "gemini", promptMarkers: []string{"allow", "deny", "gemini"}, approveInput: "y\r", rejectInput: "n\r", replySuffix: "\r"}
	default:
		return ruleAdapter{cliType: "custom", promptMarkers: []string{"permission_request", "approve", "reject"}, approveInput: "approve\r", rejectInput: "reject\r", replySuffix: "\r"}
	}
}

func NormalizeCLIType(cliType string) string {
	normalized := strings.ToLower(strings.TrimSpace(cliType))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	normalized = strings.ReplaceAll(normalized, " ", "_")
	switch normalized {
	case "", "fake", "fake_ai_cli", "fake-ai-cli":
		return "custom"
	case "claude", "claude-code":
		return "claude_code"
	case "gh_copilot", "github_copilot":
		return "copilot"
	default:
		return normalized
	}
}

func (a ruleAdapter) Type() string {
	return a.cliType
}

func (a ruleAdapter) Detect(snapshot TerminalSnapshot) []DetectedEvent {
	text := snapshotText(snapshot)
	if text == "" || !containsAllFold(text, a.promptMarkers) {
		return nil
	}

	prompt := firstPromptLine(snapshot)
	if prompt == "" {
		prompt = "permission_request: allow command execution?"
	}
	return []DetectedEvent{
		{
			EventType:            "permission_request",
			RiskLevel:            riskLevelForPrompt(text),
			PromptText:           prompt,
			ContextBefore:        contextBefore(snapshot),
			SuggestedActions:     []string{"approve", "reject", "reply"},
			DefaultTimeoutAction: "reject",
		},
	}
}

func (a ruleAdapter) BuildDecisionInput(_ ApprovalEvent, decision Decision) ([]byte, error) {
	switch strings.ToLower(strings.TrimSpace(decision.Type)) {
	case "approve":
		return []byte(a.approveInput), nil
	case "reject":
		return []byte(a.rejectInput), nil
	case "reply":
		payload := strings.TrimSpace(decision.Payload)
		if payload == "" {
			return nil, errors.New("reply decision requires payload")
		}
		return []byte(payload + a.replySuffix), nil
	default:
		return nil, errors.New("unsupported decision type")
	}
}

func (a ruleAdapter) IsPromptStillActive(snapshot TerminalSnapshot, event ApprovalEvent) bool {
	text := snapshotText(snapshot)
	if event.PromptText != "" && strings.Contains(strings.ToLower(text), strings.ToLower(event.PromptText)) {
		return true
	}
	return containsAllFold(text, a.promptMarkers)
}

func snapshotText(snapshot TerminalSnapshot) string {
	if snapshot.VisibleText != "" {
		return snapshot.VisibleText
	}
	if len(snapshot.RecentLines) > 0 {
		return strings.Join(snapshot.RecentLines, "\n")
	}
	return snapshot.CursorLine
}

func firstPromptLine(snapshot TerminalSnapshot) string {
	lines := snapshot.RecentLines
	if len(lines) == 0 && snapshot.VisibleText != "" {
		lines = strings.Split(snapshot.VisibleText, "\n")
	}
	for _, line := range lines {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "permission") || strings.Contains(lower, "allow") || strings.Contains(lower, "approve") {
			return strings.TrimSpace(line)
		}
	}
	return strings.TrimSpace(snapshot.CursorLine)
}

func contextBefore(snapshot TerminalSnapshot) string {
	lines := snapshot.RecentLines
	if len(lines) == 0 && snapshot.VisibleText != "" {
		lines = strings.Split(snapshot.VisibleText, "\n")
	}
	if len(lines) <= 1 {
		return strings.TrimSpace(snapshot.VisibleText)
	}
	return strings.TrimSpace(strings.Join(lines[:len(lines)-1], "\n"))
}

func containsAllFold(text string, markers []string) bool {
	lower := strings.ToLower(text)
	for _, marker := range markers {
		if !strings.Contains(lower, strings.ToLower(marker)) {
			return false
		}
	}
	return true
}

func riskLevelForPrompt(text string) string {
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "delete") || strings.Contains(lower, "rm -rf") || strings.Contains(lower, "sudo"):
		return "critical"
	case strings.Contains(lower, "write") || strings.Contains(lower, "execute") || strings.Contains(lower, "command"):
		return "high"
	default:
		return "medium"
	}
}
