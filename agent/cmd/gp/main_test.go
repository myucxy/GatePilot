package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTerminalTitleForCLI(t *testing.T) {
	if got := terminalTitleForCLI("codex"); got != "GatePilot:codex" {
		t.Fatalf("codex title = %q", got)
	}
	if got := terminalTitleForCLI("claude-code"); got != "GatePilot:claude" {
		t.Fatalf("claude title = %q", got)
	}
	if got := terminalTitleForCLI("custom"); got != "" {
		t.Fatalf("custom title = %q, want empty", got)
	}
}

func TestCommandLineForDisplayQuotesSpacedArgs(t *testing.T) {
	got := commandLineForDisplay([]string{"codex", "--cd", `E:\Work Space\GatePilot`})
	if got != `codex --cd "E:\\Work Space\\GatePilot"` {
		t.Fatalf("command line = %q", got)
	}
}

func TestCleanTerminalTextRemovesControlSequences(t *testing.T) {
	input := "\x1b[?2026h\x1b[13;2HCodex\x1b[K\r\n\x1b]0;GatePilot\aAllow / Deny\x1b[?25h"
	got := cleanTerminalText(input)
	for _, fragment := range []string{"?2026h", "13;2H", "0;GatePilot", "?25h"} {
		if strings.Contains(got, fragment) {
			t.Fatalf("cleaned text = %q, still contains %q", got, fragment)
		}
	}
	if !strings.Contains(got, "Codex") || !strings.Contains(got, "Allow / Deny") {
		t.Fatalf("cleaned text = %q, want visible prompt text", got)
	}
}

func TestConfiguredExecutableUsesSettings(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	t.Setenv("GATEPILOT_AGENT_SETTINGS", settingsPath)
	body := `{"ai_tools":[{"tool_id":"codex","tool_type":"codex","enabled":true,"executable_path":"D:\\Tools\\codex.cmd"}]}`
	if err := os.WriteFile(settingsPath, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	if got := configuredExecutable("codex"); got != `D:\Tools\codex.cmd` {
		t.Fatalf("configured executable = %q", got)
	}
}
