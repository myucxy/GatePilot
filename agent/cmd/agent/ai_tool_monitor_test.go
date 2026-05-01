package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAIToolMonitorBaselinesExistingCodexFiles(t *testing.T) {
	root := t.TempDir()
	sessionsDir := filepath.Join(root, "sessions")
	if err := os.MkdirAll(sessionsDir, 0700); err != nil {
		t.Fatal(err)
	}
	rollout := filepath.Join(sessionsDir, "rollout-2026-05-01T09-30-51-session-1.jsonl")
	if err := os.WriteFile(rollout, []byte(
		`{"timestamp":"2026-05-01T01:30:51Z","type":"session_meta","payload":{"id":"session-1","cwd":"E:\\WorkSpace\\GatePilot"}}`+"\n"+
			`{"timestamp":"2026-05-01T01:31:00Z","type":"response_item","payload":{"type":"approval_request","prompt_text":"old approval"}}`+"\n",
	), 0600); err != nil {
		t.Fatal(err)
	}

	monitor := newTestAIToolMonitor()
	cfg := aiToolConfig{ToolID: "codex", ToolType: "codex", DisplayName: "Codex", Enabled: true, SessionsDir: sessionsDir}
	events, err := monitor.scanCodex(cfg, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %+v, want no startup notifications", events)
	}
	if err := appendLine(rollout, `{"timestamp":"2026-05-01T01:32:00Z","type":"response_item","payload":{"type":"approval_request","prompt_text":"run tests?"}}`); err != nil {
		t.Fatal(err)
	}
	events, err = monitor.scanCodex(cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].SessionID != "session-1" || !strings.Contains(events[0].Summary, "run tests") {
		t.Fatalf("events = %+v, want appended approval event", events)
	}
}

func TestAIToolMonitorReadsNewCodexFilesAfterBaseline(t *testing.T) {
	root := t.TempDir()
	sessionsDir := filepath.Join(root, "sessions")
	if err := os.MkdirAll(sessionsDir, 0700); err != nil {
		t.Fatal(err)
	}
	monitor := newTestAIToolMonitor()
	cfg := aiToolConfig{ToolID: "codex", ToolType: "codex", DisplayName: "Codex", Enabled: true, SessionsDir: sessionsDir}
	if _, err := monitor.scanCodex(cfg, true); err != nil {
		t.Fatal(err)
	}
	rollout := filepath.Join(sessionsDir, "rollout-2026-05-01T09-30-51-session-2.jsonl")
	if err := os.WriteFile(rollout, []byte(
		`{"timestamp":"2026-05-01T01:30:51Z","type":"session_meta","payload":{"id":"session-2","cwd":"E:\\WorkSpace\\GatePilot"}}`+"\n"+
			`{"timestamp":"2026-05-01T01:31:00Z","type":"response_item","payload":{"type":"permission_request","command":"go test ./..."}}`+"\n",
	), 0600); err != nil {
		t.Fatal(err)
	}
	events, err := monitor.scanCodex(cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].SessionID != "session-2" || !strings.Contains(events[0].Summary, "go test") {
		t.Fatalf("events = %+v, want new-file approval event", events)
	}
}

func TestAIToolMonitorDetectsClaudeApprovalLines(t *testing.T) {
	root := t.TempDir()
	historyPath := filepath.Join(root, "history.jsonl")
	if err := os.WriteFile(historyPath, []byte(`{"display":"Permission request: run tests?","timestamp":1777614683000,"project":"E:\\WorkSpace\\GatePilot","sessionId":"claude-1"}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	monitor := newTestAIToolMonitor()
	events, err := monitor.scanClaude(aiToolConfig{ToolID: "claude", ToolType: "claude", DisplayName: "Claude Code", Enabled: true, HistoryPath: historyPath}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].SessionID != "claude-1" || !strings.Contains(events[0].WorkingDir, "GatePilot") {
		t.Fatalf("events = %+v, want claude approval event", events)
	}
}

func TestAIToolMonitorMessageIsChineseAndActionable(t *testing.T) {
	message := aiToolMonitorMessage(aiToolMonitorEvent{
		ToolID:     "codex",
		ToolName:   "Codex",
		SessionID:  "session-1",
		WorkingDir: `E:\WorkSpace\GatePilot`,
		Summary:    "permission_request",
	})
	for _, want := range []string{"检测到 AI 工具可能需要确认", "工具：Codex", "目录：", "会话：session-1", "请回到对应 CLI 窗口确认"} {
		if !strings.Contains(message, want) {
			t.Fatalf("message = %q, want %q", message, want)
		}
	}
}

func newTestAIToolMonitor() *aiToolMonitor {
	return &aiToolMonitor{
		offsets:    map[string]int64{},
		lastNotify: map[string]time.Time{},
	}
}

func appendLine(path string, line string) error {
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.WriteString(line + "\n")
	return err
}
