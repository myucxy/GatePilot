package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAIToolSessionsRequireConfiguredEnabledTools(t *testing.T) {
	items, err := listAIToolSessions(defaultAgentLocalSettings(), aiToolSessionFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("items = %+v, want no sessions without configured tools", items)
	}
}

func TestScanCodexSessionsFromHistoryAndRollout(t *testing.T) {
	root := t.TempDir()
	historyPath := filepath.Join(root, "history.jsonl")
	sessionsDir := filepath.Join(root, "sessions")
	if err := os.MkdirAll(sessionsDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(historyPath, []byte(
		`{"session_id":"session-1","ts":1777614683,"text":"Build the desktop agent"}`+"\n"+
			`{"session_id":"session-1","ts":1777614690,"text":"Continue"}`+"\n",
	), 0600); err != nil {
		t.Fatal(err)
	}
	rollout := filepath.Join(sessionsDir, "rollout-2026-05-01T09-30-51-session-1.jsonl")
	if err := os.WriteFile(rollout, []byte(
		`{"timestamp":"2026-05-01T01:30:51Z","type":"session_meta","payload":{"id":"session-1","cwd":"E:\\WorkSpace\\GatePilot","agent_nickname":"Worker"}}`+"\n"+
			`{"timestamp":"2026-05-01T01:31:00Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Done"}]}}`+"\n",
	), 0600); err != nil {
		t.Fatal(err)
	}

	items, err := scanCodexSessions(aiToolConfig{ToolID: "codex", ToolType: "codex", DisplayName: "Codex", Enabled: true, HistoryPath: historyPath, SessionsDir: sessionsDir})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %+v, want one codex session", items)
	}
	if items[0].ID != "session-1" || items[0].MessageCount < 2 || items[0].WorkingDir == "" || items[0].Title == "" {
		t.Fatalf("item = %+v, want merged codex metadata", items[0])
	}
	detail, ok, err := aiToolSessionDetail(agentLocalSettings{AITools: []aiToolConfig{{ToolID: "codex", ToolType: "codex", DisplayName: "Codex", Enabled: true, HistoryPath: historyPath, SessionsDir: sessionsDir}}}, "codex", "session-1")
	if err != nil || !ok {
		t.Fatalf("detail ok=%v err=%v", ok, err)
	}
	if len(detail.Messages) < 2 {
		t.Fatalf("messages = %+v, want codex history and rollout messages", detail.Messages)
	}
}

func TestScanClaudeSessionsFromHistory(t *testing.T) {
	root := t.TempDir()
	historyPath := filepath.Join(root, "history.jsonl")
	if err := os.WriteFile(historyPath, []byte(
		`{"display":"Hello Claude","timestamp":1777614683000,"project":"E:\\WorkSpace\\GatePilot","sessionId":"claude-1"}`+"\n"+
			`{"display":"Continue","timestamp":1777614690000,"project":"E:\\WorkSpace\\GatePilot","sessionId":"claude-1"}`+"\n",
	), 0600); err != nil {
		t.Fatal(err)
	}
	items, err := scanClaudeSessions(aiToolConfig{ToolID: "claude", ToolType: "claude", DisplayName: "Claude Code", Enabled: true, HistoryPath: historyPath})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "claude-1" || items[0].MessageCount != 2 || items[0].WorkingDir == "" {
		t.Fatalf("items = %+v, want grouped claude session", items)
	}
}

func TestDeleteAIToolSessionRewritesHistoryWithBackup(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APPDATA", root)
	historyPath := filepath.Join(root, "history.jsonl")
	if err := os.WriteFile(historyPath, []byte(
		`{"display":"remove","timestamp":1777614683000,"project":"E:\\A","sessionId":"delete-me"}`+"\n"+
			`{"display":"keep","timestamp":1777614690000,"project":"E:\\B","sessionId":"keep-me"}`+"\n",
	), 0600); err != nil {
		t.Fatal(err)
	}
	settings := agentLocalSettings{AITools: []aiToolConfig{{ToolID: "claude", ToolType: "claude", DisplayName: "Claude Code", Enabled: true, HistoryPath: historyPath}}}
	result, ok, err := deleteAIToolSession(settings, "claude", "delete-me")
	if err != nil || !ok {
		t.Fatalf("delete ok=%v err=%v", ok, err)
	}
	if len(result.Updated) != 1 {
		t.Fatalf("result = %+v, want one updated file", result)
	}
	body, err := os.ReadFile(historyPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) == "" || strings.Contains(string(body), "delete-me") || !strings.Contains(string(body), "keep-me") {
		t.Fatalf("history after delete = %q", string(body))
	}
}
