package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

type aiToolMonitor struct {
	state      *trayState
	mu         sync.Mutex
	offsets    map[string]int64
	lastNotify map[string]time.Time
}

type aiToolMonitorEvent struct {
	ToolID     string
	ToolName   string
	SessionID  string
	WorkingDir string
	SourcePath string
	Summary    string
	CreatedAt  time.Time
}

func startAIToolMonitor(state *trayState) {
	monitor := &aiToolMonitor{
		state:      state,
		offsets:    map[string]int64{},
		lastNotify: map[string]time.Time{},
	}
	go monitor.loop()
}

func (m *aiToolMonitor) loop() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	m.scan(true)
	for range ticker.C {
		m.scan(false)
	}
}

func (m *aiToolMonitor) scan(baseline bool) {
	settings := m.state.currentSettings()
	if !settings.NotificationEnabled || settings.NotificationStyle == "none" {
		return
	}
	for _, cfg := range configuredAITools(settings) {
		events, err := m.scanTool(cfg, baseline)
		if err != nil {
			continue
		}
		for _, event := range events {
			m.notify(settings, event)
		}
	}
}

func (m *aiToolMonitor) scanTool(cfg aiToolConfig, baseline bool) ([]aiToolMonitorEvent, error) {
	switch cfg.ToolType {
	case "codex":
		return m.scanCodex(cfg, baseline)
	case "claude":
		return m.scanClaude(cfg, baseline)
	default:
		return nil, nil
	}
}

func (m *aiToolMonitor) scanCodex(cfg aiToolConfig, baseline bool) ([]aiToolMonitorEvent, error) {
	if cfg.SessionsDir == "" {
		return nil, nil
	}
	events := []aiToolMonitorEvent{}
	err := filepath.WalkDir(cfg.SessionsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(strings.ToLower(d.Name()), ".jsonl") {
			return err
		}
		fileEvents, err := m.scanCodexFile(cfg, path, baseline)
		if err != nil {
			return nil
		}
		events = append(events, fileEvents...)
		return nil
	})
	return events, err
}

func (m *aiToolMonitor) scanCodexFile(cfg aiToolConfig, path string, baseline bool) ([]aiToolMonitorEvent, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	offset, seen := m.offsets[path]
	switch {
	case baseline:
		m.offsets[path] = info.Size()
		m.mu.Unlock()
		return nil, nil
	case !seen || info.Size() < offset:
		offset = 0
	}
	m.offsets[path] = info.Size()
	m.mu.Unlock()
	if info.Size() == offset {
		return nil, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if _, err := file.Seek(offset, 0); err != nil {
		return nil, err
	}
	events := []aiToolMonitorEvent{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	meta := codexMonitorMetaFromPath(path)
	for scanner.Scan() {
		if event, ok := codexMonitorEventFromLine(cfg, path, scanner.Text(), meta); ok {
			events = append(events, event)
		}
	}
	return events, scanner.Err()
}

func codexMonitorMetaFromPath(path string) aiToolMonitorEvent {
	meta, _ := readCodexRolloutSummary(path)
	return aiToolMonitorEvent{
		SessionID:  firstNonEmptyLocal(meta.ID, codexSessionIDFromPath(path)),
		WorkingDir: meta.CWD,
		CreatedAt:  meta.Timestamp,
	}
}

func codexMonitorEventFromLine(cfg aiToolConfig, path string, line string, meta aiToolMonitorEvent) (aiToolMonitorEvent, bool) {
	var item map[string]any
	if err := json.Unmarshal([]byte(line), &item); err != nil {
		return aiToolMonitorEvent{}, false
	}
	if stringFromMap(item, "type") == "session_meta" {
		return aiToolMonitorEvent{}, false
	}
	payload, _ := item["payload"].(map[string]any)
	payloadType := stringFromMap(payload, "type")
	raw := strings.ToLower(line)
	if !looksLikeApprovalRequest(payloadType, raw) {
		return aiToolMonitorEvent{}, false
	}
	summary := firstNonEmptyLocal(payloadType, "需要确认")
	for _, key := range []string{"prompt_text", "message", "reason", "command", "aggregated_output"} {
		if value := shortText(stringFromMap(payload, key), 220); value != "" {
			summary = value
			break
		}
	}
	if summary == "" || summary == "需要确认" {
		summary = shortText(line, 220)
	}
	eventTime := time.Now().UTC()
	if ts := stringFromMap(item, "timestamp"); ts != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			eventTime = parsed.UTC()
		}
	}
	return aiToolMonitorEvent{
		ToolID:     cfg.ToolID,
		ToolName:   cfg.DisplayName,
		SessionID:  meta.SessionID,
		WorkingDir: meta.WorkingDir,
		SourcePath: path,
		Summary:    summary,
		CreatedAt:  eventTime,
	}, true
}

func looksLikeApprovalRequest(payloadType string, raw string) bool {
	value := strings.ToLower(payloadType + "\n" + raw)
	markers := []string{
		"approval_request",
		"approval requested",
		"permission_request",
		"permission request",
		"requires_approval",
		"waiting_for_approval",
		"confirm_action",
		"needs_confirmation",
		"requires confirmation",
	}
	for _, marker := range markers {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func (m *aiToolMonitor) scanClaude(cfg aiToolConfig, baseline bool) ([]aiToolMonitorEvent, error) {
	if cfg.HistoryPath == "" {
		return nil, nil
	}
	info, err := os.Stat(cfg.HistoryPath)
	if err != nil {
		return nil, nil
	}
	m.mu.Lock()
	offset, seen := m.offsets[cfg.HistoryPath]
	switch {
	case baseline:
		m.offsets[cfg.HistoryPath] = info.Size()
		m.mu.Unlock()
		return nil, nil
	case !seen || info.Size() < offset:
		offset = 0
	}
	m.offsets[cfg.HistoryPath] = info.Size()
	m.mu.Unlock()
	if info.Size() == offset {
		return nil, nil
	}
	file, err := os.Open(cfg.HistoryPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if _, err := file.Seek(offset, 0); err != nil {
		return nil, err
	}
	events := []aiToolMonitorEvent{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		if event, ok := claudeMonitorEventFromLine(cfg, cfg.HistoryPath, scanner.Text()); ok {
			events = append(events, event)
		}
	}
	return events, scanner.Err()
}

func claudeMonitorEventFromLine(cfg aiToolConfig, path string, line string) (aiToolMonitorEvent, bool) {
	var item map[string]any
	if err := json.Unmarshal([]byte(line), &item); err != nil {
		return aiToolMonitorEvent{}, false
	}
	display := stringFromMap(item, "display")
	if !looksLikeApprovalRequest("", strings.ToLower(display)) {
		return aiToolMonitorEvent{}, false
	}
	eventTime := time.Now().UTC()
	if raw, ok := item["timestamp"].(float64); ok {
		eventTime = unixMillis(int64(raw))
	}
	return aiToolMonitorEvent{
		ToolID:     cfg.ToolID,
		ToolName:   cfg.DisplayName,
		SessionID:  stringFromMap(item, "sessionId"),
		WorkingDir: stringFromMap(item, "project"),
		SourcePath: path,
		Summary:    shortText(display, 220),
		CreatedAt:  eventTime,
	}, true
}

func (m *aiToolMonitor) notify(settings agentLocalSettings, event aiToolMonitorEvent) {
	key := event.ToolID + ":" + event.SessionID + ":" + event.Summary
	m.mu.Lock()
	if last := m.lastNotify[key]; !last.IsZero() && time.Since(last) < 30*time.Second {
		m.mu.Unlock()
		return
	}
	m.lastNotify[key] = time.Now()
	m.mu.Unlock()
	message := aiToolMonitorMessage(event)
	if settings.NotificationStyle == "modal_popup" {
		_ = windowsInfoPopup(message)
		return
	}
	action, err := windowsAIToolMonitorMiniWindow(message)
	if err == nil && action == "open" {
		_ = openDesktopClient("history")
	}
}

func aiToolMonitorMessage(event aiToolMonitorEvent) string {
	parts := []string{
		"检测到 AI 工具可能需要确认。",
		"",
		"工具：" + firstNonEmptyLocal(event.ToolName, event.ToolID),
	}
	if event.WorkingDir != "" {
		parts = append(parts, "目录："+event.WorkingDir)
	}
	if event.SessionID != "" {
		parts = append(parts, "会话："+event.SessionID)
	}
	if event.Summary != "" {
		parts = append(parts, "", event.Summary)
	}
	parts = append(parts, "", "请回到对应 CLI 窗口确认，或打开 GatePilot 查看会话。")
	return strings.Join(parts, "\n")
}

func windowsInfoPopup(message string) error {
	if runtime.GOOS != "windows" {
		return nil
	}
	return nativeInfoMessageBox("GatePilot 提醒", message)
}

func windowsAIToolMonitorMiniWindow(message string) (string, error) {
	if runtime.GOOS != "windows" {
		return "", nil
	}
	open, err := nativeOpenClientMessageBox("GatePilot 提醒", message)
	if err != nil {
		return "", err
	}
	if open {
		return "open", nil
	}
	return "ignore", nil
}
