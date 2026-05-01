package adapter

import (
	"strings"
	"testing"
)

func TestAdaptersDetectPermissionPrompts(t *testing.T) {
	tests := []struct {
		cliType string
		text    string
	}{
		{cliType: "custom", text: "GatePilot fake AI CLI\npermission_request: allow command execution? [approve/reject/reply]\nwaiting_for_input"},
		{cliType: "codex", text: "Codex needs permission to run command\nApprove or reject?"},
		{cliType: "codex", text: "Codex requires approval\nAllow this command?\n  Y yes\n  N no"},
		{cliType: "codex", text: "Run command: powershell.exe -NoProfile\nAllow / Deny"},
		{cliType: "codex", text: "Continue anyway?\nCodex wants to execute a shell command\nYes / No"},
		{cliType: "claude_code", text: "Claude Code asks: do you want to proceed?\n1. Yes\n2. No"},
		{cliType: "opencode", text: "OpenCode permission request\nAllow or deny command execution"},
		{cliType: "copilot", text: "GitHub Copilot needs approval\napprove / reject"},
		{cliType: "gemini", text: "Gemini wants to run a command\nAllow or deny?"},
	}

	for _, tt := range tests {
		t.Run(tt.cliType, func(t *testing.T) {
			events := ForCLI(tt.cliType).Detect(TerminalSnapshot{
				VisibleText: tt.text,
				RecentLines: []string{
					"previous output",
					tt.text,
				},
			})
			if len(events) != 1 {
				t.Fatalf("Detect() event count = %d, want 1", len(events))
			}
			if events[0].EventType != "permission_request" {
				t.Fatalf("event type = %q, want permission_request", events[0].EventType)
			}
			if len(events[0].SuggestedActions) != 3 {
				t.Fatalf("suggested action count = %d, want 3", len(events[0].SuggestedActions))
			}
		})
	}
}

func TestAdaptersBuildDecisionInput(t *testing.T) {
	tests := []struct {
		cliType string
		approve string
		reject  string
	}{
		{cliType: "custom", approve: "approve\r", reject: "reject\r"},
		{cliType: "codex", approve: "y\r", reject: "n\r"},
		{cliType: "claude_code", approve: "1\r", reject: "2\r"},
		{cliType: "opencode", approve: "a\r", reject: "d\r"},
		{cliType: "copilot", approve: "approve\r", reject: "reject\r"},
		{cliType: "gemini", approve: "y\r", reject: "n\r"},
	}

	for _, tt := range tests {
		t.Run(tt.cliType, func(t *testing.T) {
			adapter := ForCLI(tt.cliType)
			gotApprove, err := adapter.BuildDecisionInput(ApprovalEvent{}, Decision{Type: "approve"})
			if err != nil {
				t.Fatal(err)
			}
			if string(gotApprove) != tt.approve {
				t.Fatalf("approve input = %q, want %q", string(gotApprove), tt.approve)
			}
			gotReject, err := adapter.BuildDecisionInput(ApprovalEvent{}, Decision{Type: "reject"})
			if err != nil {
				t.Fatal(err)
			}
			if string(gotReject) != tt.reject {
				t.Fatalf("reject input = %q, want %q", string(gotReject), tt.reject)
			}
		})
	}
}

func TestReplyRequiresPayload(t *testing.T) {
	_, err := ForCLI("custom").BuildDecisionInput(ApprovalEvent{}, Decision{Type: "reply"})
	if err == nil {
		t.Fatal("BuildDecisionInput(reply) error = nil, want error")
	}
}

func TestCodexAdapterIgnoresOrdinaryOutput(t *testing.T) {
	tests := []string{
		"Codex is thinking\nrunning analysis\ncommand output finished successfully",
		"command failed: permission denied",
	}
	for _, text := range tests {
		events := ForCLI("codex").Detect(TerminalSnapshot{
			VisibleText: text,
			RecentLines: strings.Split(text, "\n"),
		})
		if len(events) != 0 {
			t.Fatalf("Detect(%q) event count = %d, want 0", text, len(events))
		}
	}
}

func TestPromptStillActive(t *testing.T) {
	cliAdapter := ForCLI("custom")
	event := ApprovalEvent{PromptText: "permission_request: allow command execution?"}
	active := cliAdapter.IsPromptStillActive(TerminalSnapshot{VisibleText: "permission_request: allow command execution?"}, event)
	if !active {
		t.Fatal("IsPromptStillActive() = false, want true")
	}
	inactive := cliAdapter.IsPromptStillActive(TerminalSnapshot{VisibleText: "command completed"}, event)
	if inactive {
		t.Fatal("IsPromptStillActive() = true, want false")
	}
}

func TestNormalizeCLIType(t *testing.T) {
	if got := NormalizeCLIType("claude-code"); got != "claude_code" {
		t.Fatalf("NormalizeCLIType() = %q, want claude_code", got)
	}
	if got := NormalizeCLIType("fake-ai-cli"); got != "custom" {
		t.Fatalf("NormalizeCLIType() = %q, want custom", got)
	}
}
