package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/myucxy/gatepilot/agent/internal/adapter"
)

type aiToolConfig struct {
	ToolID                  string `json:"tool_id"`
	ToolType                string `json:"tool_type"`
	DisplayName             string `json:"display_name"`
	Enabled                 bool   `json:"enabled"`
	HomeDir                 string `json:"home_dir"`
	HistoryPath             string `json:"history_path"`
	SessionsDir             string `json:"sessions_dir"`
	ExecutablePath          string `json:"executable_path"`
	ContinueCommandTemplate string `json:"continue_command_template"`
}

type aiToolSessionFilter struct {
	ToolID string
	Query  string
	Limit  int
}

type aiToolSessionRecord struct {
	ID           string `json:"id"`
	ToolID       string `json:"tool_id"`
	ToolType     string `json:"tool_type"`
	DisplayName  string `json:"display_name"`
	Title        string `json:"title"`
	WorkingDir   string `json:"working_dir"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
	MessageCount int    `json:"message_count"`
	Preview      string `json:"preview"`
	SourcePath   string `json:"source_path"`
	CanContinue  bool   `json:"can_continue"`
}

type aiToolMessageRecord struct {
	Timestamp string         `json:"timestamp"`
	Role      string         `json:"role"`
	Type      string         `json:"type"`
	Text      string         `json:"text"`
	Raw       map[string]any `json:"raw,omitempty"`
}

type aiToolSessionDetailRecord struct {
	Session  aiToolSessionRecord   `json:"session"`
	Messages []aiToolMessageRecord `json:"messages"`
}

type aiToolDeleteResult struct {
	SessionID string   `json:"session_id"`
	ToolID    string   `json:"tool_id"`
	Moved     []string `json:"moved"`
	Updated   []string `json:"updated"`
}

func normalizeAIToolConfigs(configs []aiToolConfig) []aiToolConfig {
	normalized := []aiToolConfig{}
	seen := map[string]bool{}
	for _, cfg := range configs {
		cfg.ToolType = normalizeAIToolType(cfg.ToolType)
		if cfg.ToolType == "" {
			cfg.ToolType = normalizeAIToolType(cfg.ToolID)
		}
		if cfg.ToolType == "" {
			continue
		}
		cfg.ToolID = strings.TrimSpace(cfg.ToolID)
		if cfg.ToolID == "" {
			cfg.ToolID = cfg.ToolType
		}
		cfg.ToolID = strings.ToLower(strings.ReplaceAll(cfg.ToolID, " ", "_"))
		if seen[cfg.ToolID] {
			continue
		}
		seen[cfg.ToolID] = true
		if cfg.DisplayName == "" {
			cfg.DisplayName = defaultAIToolDisplayName(cfg.ToolType)
		}
		cfg.HomeDir = cleanOptionalPath(cfg.HomeDir)
		cfg.HistoryPath = cleanOptionalPath(cfg.HistoryPath)
		cfg.SessionsDir = cleanOptionalPath(cfg.SessionsDir)
		cfg.ExecutablePath = strings.TrimSpace(cfg.ExecutablePath)
		cfg.ContinueCommandTemplate = strings.TrimSpace(cfg.ContinueCommandTemplate)
		normalized = append(normalized, cfg)
	}
	return normalized
}

func normalizeAIToolType(value string) string {
	switch adapter.NormalizeCLIType(value) {
	case "codex":
		return "codex"
	case "claude_code":
		return "claude"
	default:
		return strings.TrimSpace(strings.ToLower(value))
	}
}

func defaultAIToolDisplayName(toolType string) string {
	switch toolType {
	case "codex":
		return "Codex"
	case "claude":
		return "Claude Code"
	default:
		return strings.Title(toolType)
	}
}

func cleanOptionalPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if strings.HasPrefix(path, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(path, "~"), string(filepath.Separator)))
		}
	}
	return filepath.Clean(path)
}

func defaultAIToolConfigs() []aiToolConfig {
	home, _ := os.UserHomeDir()
	configs := []aiToolConfig{}
	if home != "" {
		configs = append(configs,
			aiToolConfig{
				ToolID:                  "codex",
				ToolType:                "codex",
				DisplayName:             "Codex",
				Enabled:                 true,
				HomeDir:                 filepath.Join(home, ".codex"),
				HistoryPath:             filepath.Join(home, ".codex", "history.jsonl"),
				SessionsDir:             filepath.Join(home, ".codex", "sessions"),
				ExecutablePath:          "codex",
				ContinueCommandTemplate: "codex resume {session_id}",
			},
			aiToolConfig{
				ToolID:                  "claude",
				ToolType:                "claude",
				DisplayName:             "Claude Code",
				Enabled:                 true,
				HomeDir:                 filepath.Join(home, ".claude"),
				HistoryPath:             filepath.Join(home, ".claude", "history.jsonl"),
				SessionsDir:             filepath.Join(home, ".claude", "sessions"),
				ExecutablePath:          "claude",
				ContinueCommandTemplate: "claude --resume {session_id}",
			},
		)
	}
	return normalizeAIToolConfigs(configs)
}

func configuredAITools(settings agentLocalSettings) []aiToolConfig {
	items := []aiToolConfig{}
	for _, cfg := range normalizeAIToolConfigs(settings.AITools) {
		if cfg.Enabled {
			items = append(items, cfg)
		}
	}
	return items
}

func listAIToolSessions(settings agentLocalSettings, filter aiToolSessionFilter) ([]aiToolSessionRecord, error) {
	items := []aiToolSessionRecord{}
	for _, cfg := range configuredAITools(settings) {
		if filter.ToolID != "" && cfg.ToolID != filter.ToolID {
			continue
		}
		toolSessions, err := scanAIToolSessions(cfg)
		if err != nil {
			return nil, err
		}
		items = append(items, toolSessions...)
	}
	if filter.Query != "" {
		items = filterAIToolSessions(items, filter.Query)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].UpdatedAt > items[j].UpdatedAt
	})
	if filter.Limit > 0 && len(items) > filter.Limit {
		items = items[:filter.Limit]
	}
	return items, nil
}

func filterAIToolSessions(items []aiToolSessionRecord, query string) []aiToolSessionRecord {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return items
	}
	filtered := []aiToolSessionRecord{}
	for _, item := range items {
		haystack := strings.ToLower(strings.Join([]string{item.ID, item.Title, item.WorkingDir, item.Preview, item.ToolID}, "\n"))
		if strings.Contains(haystack, query) {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func scanAIToolSessions(cfg aiToolConfig) ([]aiToolSessionRecord, error) {
	switch cfg.ToolType {
	case "codex":
		return scanCodexSessions(cfg)
	case "claude":
		return scanClaudeSessions(cfg)
	default:
		return nil, fmt.Errorf("unsupported ai tool type %q", cfg.ToolType)
	}
}

func aiToolSessionDetail(settings agentLocalSettings, toolID string, sessionID string) (aiToolSessionDetailRecord, bool, error) {
	cfg, ok := findAIToolConfig(settings, toolID)
	if !ok {
		return aiToolSessionDetailRecord{}, false, nil
	}
	sessions, err := scanAIToolSessions(cfg)
	if err != nil {
		return aiToolSessionDetailRecord{}, false, err
	}
	for _, session := range sessions {
		if session.ID != sessionID {
			continue
		}
		messages, err := readAIToolMessages(cfg, session)
		if err != nil {
			return aiToolSessionDetailRecord{}, false, err
		}
		return aiToolSessionDetailRecord{Session: session, Messages: messages}, true, nil
	}
	return aiToolSessionDetailRecord{}, false, nil
}

func findAIToolConfig(settings agentLocalSettings, toolID string) (aiToolConfig, bool) {
	toolID = strings.TrimSpace(toolID)
	for _, cfg := range configuredAITools(settings) {
		if cfg.ToolID == toolID {
			return cfg, true
		}
	}
	return aiToolConfig{}, false
}

func readAIToolMessages(cfg aiToolConfig, session aiToolSessionRecord) ([]aiToolMessageRecord, error) {
	switch cfg.ToolType {
	case "codex":
		return readCodexMessages(cfg, session)
	case "claude":
		return readClaudeMessages(cfg, session.ID)
	default:
		return nil, fmt.Errorf("unsupported ai tool type %q", cfg.ToolType)
	}
}

func scanCodexSessions(cfg aiToolConfig) ([]aiToolSessionRecord, error) {
	records := map[string]*aiToolSessionRecord{}
	if cfg.HistoryPath != "" {
		if err := scanCodexHistory(cfg, records); err != nil {
			return nil, err
		}
	}
	if cfg.SessionsDir != "" {
		if err := scanCodexRollouts(cfg, records); err != nil {
			return nil, err
		}
	}
	items := mapToAIToolSessions(records)
	sort.Slice(items, func(i, j int) bool {
		return items[i].UpdatedAt > items[j].UpdatedAt
	})
	return items, nil
}

func scanCodexHistory(cfg aiToolConfig, records map[string]*aiToolSessionRecord) error {
	return scanJSONLines(cfg.HistoryPath, func(line []byte) error {
		var item struct {
			SessionID string `json:"session_id"`
			TS        int64  `json:"ts"`
			Text      string `json:"text"`
		}
		if err := json.Unmarshal(line, &item); err != nil || item.SessionID == "" {
			return nil
		}
		rec := ensureAIToolSession(records, cfg, item.SessionID)
		rec.MessageCount++
		updateAIToolTime(rec, unixSeconds(item.TS))
		if rec.Title == "" {
			rec.Title = shortText(item.Text, 80)
		}
		rec.Preview = shortText(item.Text, 240)
		return nil
	})
}

func scanCodexRollouts(cfg aiToolConfig, records map[string]*aiToolSessionRecord) error {
	if _, err := os.Stat(cfg.SessionsDir); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return filepath.WalkDir(cfg.SessionsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(strings.ToLower(d.Name()), ".jsonl") {
			return err
		}
		meta, count := readCodexRolloutSummary(path)
		if meta.ID == "" {
			meta.ID = codexSessionIDFromPath(path)
		}
		if meta.ID == "" {
			return nil
		}
		rec := ensureAIToolSession(records, cfg, meta.ID)
		rec.SourcePath = path
		rec.WorkingDir = firstNonEmptyLocal(rec.WorkingDir, meta.CWD)
		rec.MessageCount += count
		updateAIToolTime(rec, meta.Timestamp)
		if rec.Title == "" {
			rec.Title = firstNonEmptyLocal(meta.Nickname, filepath.Base(path))
		}
		if rec.Preview == "" {
			rec.Preview = firstNonEmptyLocal(meta.Nickname, filepath.Base(path))
		}
		return nil
	})
}

type codexRolloutMeta struct {
	ID        string
	Timestamp time.Time
	CWD       string
	Nickname  string
}

func readCodexRolloutSummary(path string) (codexRolloutMeta, int) {
	var meta codexRolloutMeta
	count := 0
	_ = scanJSONLines(path, func(line []byte) error {
		var item map[string]any
		if err := json.Unmarshal(line, &item); err != nil {
			return nil
		}
		if ts, ok := item["timestamp"].(string); ok {
			if parsed, err := time.Parse(time.RFC3339Nano, ts); err == nil {
				if meta.Timestamp.IsZero() || parsed.After(meta.Timestamp) {
					meta.Timestamp = parsed.UTC()
				}
			}
		}
		itemType, _ := item["type"].(string)
		if itemType == "session_meta" {
			if payload, ok := item["payload"].(map[string]any); ok {
				meta.ID = stringFromMap(payload, "id")
				meta.CWD = stringFromMap(payload, "cwd")
				meta.Nickname = firstNonEmptyLocal(stringFromMap(payload, "agent_nickname"), stringFromMap(payload, "agent_role"))
				if ts := stringFromMap(payload, "timestamp"); ts != "" {
					if parsed, err := time.Parse(time.RFC3339Nano, ts); err == nil {
						meta.Timestamp = parsed.UTC()
					}
				}
			}
		}
		switch itemType {
		case "response_item", "event_msg":
			count++
		}
		return nil
	})
	return meta, count
}

func codexSessionIDFromPath(path string) string {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	parts := strings.Split(base, "-")
	if len(parts) < 7 {
		return ""
	}
	return strings.Join(parts[len(parts)-5:], "-")
}

func readCodexMessages(cfg aiToolConfig, session aiToolSessionRecord) ([]aiToolMessageRecord, error) {
	messages := []aiToolMessageRecord{}
	if session.SourcePath != "" {
		err := scanJSONLines(session.SourcePath, func(line []byte) error {
			msg := codexLineToMessage(line)
			if msg.Text != "" || msg.Type != "" {
				messages = append(messages, msg)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	if cfg.HistoryPath != "" {
		history, err := readCodexHistoryMessages(cfg.HistoryPath, session.ID)
		if err != nil {
			return nil, err
		}
		messages = append(history, messages...)
	}
	return messages, nil
}

func readCodexHistoryMessages(path string, sessionID string) ([]aiToolMessageRecord, error) {
	messages := []aiToolMessageRecord{}
	err := scanJSONLines(path, func(line []byte) error {
		var item struct {
			SessionID string `json:"session_id"`
			TS        int64  `json:"ts"`
			Text      string `json:"text"`
		}
		if err := json.Unmarshal(line, &item); err != nil || item.SessionID != sessionID {
			return nil
		}
		messages = append(messages, aiToolMessageRecord{
			Timestamp: formatTime(unixSeconds(item.TS)),
			Role:      "user",
			Type:      "history",
			Text:      item.Text,
		})
		return nil
	})
	return messages, err
}

func codexLineToMessage(line []byte) aiToolMessageRecord {
	var item map[string]any
	if err := json.Unmarshal(line, &item); err != nil {
		return aiToolMessageRecord{}
	}
	msg := aiToolMessageRecord{Type: stringFromMap(item, "type"), Raw: item}
	if ts := stringFromMap(item, "timestamp"); ts != "" {
		msg.Timestamp = ts
	}
	payload, _ := item["payload"].(map[string]any)
	if payload == nil {
		return msg
	}
	switch msg.Type {
	case "session_meta":
		msg.Role = "system"
		msg.Text = "Session started in " + stringFromMap(payload, "cwd")
	case "event_msg":
		msg.Role = "system"
		msg.Text = firstNonEmptyLocal(stringFromMap(payload, "type"), stringFromMap(payload, "message"))
	case "response_item":
		msg.Role = stringFromMap(payload, "role")
		msg.Text = textFromContent(payload["content"])
		if msg.Text == "" {
			msg.Text = firstNonEmptyLocal(stringFromMap(payload, "type"), stringFromMap(payload, "status"))
		}
	}
	return msg
}

func scanClaudeSessions(cfg aiToolConfig) ([]aiToolSessionRecord, error) {
	records := map[string]*aiToolSessionRecord{}
	if cfg.HistoryPath == "" {
		return nil, nil
	}
	err := scanJSONLines(cfg.HistoryPath, func(line []byte) error {
		var item struct {
			Display   string         `json:"display"`
			Timestamp int64          `json:"timestamp"`
			Project   string         `json:"project"`
			SessionID string         `json:"sessionId"`
			Pasted    map[string]any `json:"pastedContents"`
		}
		if err := json.Unmarshal(line, &item); err != nil || item.SessionID == "" {
			return nil
		}
		rec := ensureAIToolSession(records, cfg, item.SessionID)
		rec.MessageCount++
		rec.WorkingDir = firstNonEmptyLocal(rec.WorkingDir, item.Project)
		updateAIToolTime(rec, unixMillis(item.Timestamp))
		if rec.Title == "" {
			rec.Title = shortText(item.Display, 80)
		}
		rec.Preview = shortText(item.Display, 240)
		rec.SourcePath = cfg.HistoryPath
		return nil
	})
	if err != nil {
		return nil, err
	}
	return mapToAIToolSessions(records), nil
}

func readClaudeMessages(cfg aiToolConfig, sessionID string) ([]aiToolMessageRecord, error) {
	messages := []aiToolMessageRecord{}
	if cfg.HistoryPath == "" {
		return messages, nil
	}
	err := scanJSONLines(cfg.HistoryPath, func(line []byte) error {
		var item map[string]any
		if err := json.Unmarshal(line, &item); err != nil || stringFromMap(item, "sessionId") != sessionID {
			return nil
		}
		ts := time.Time{}
		if raw, ok := item["timestamp"].(float64); ok {
			ts = unixMillis(int64(raw))
		}
		messages = append(messages, aiToolMessageRecord{
			Timestamp: formatTime(ts),
			Role:      "user",
			Type:      "history",
			Text:      stringFromMap(item, "display"),
			Raw:       item,
		})
		return nil
	})
	return messages, err
}

func ensureAIToolSession(records map[string]*aiToolSessionRecord, cfg aiToolConfig, id string) *aiToolSessionRecord {
	if rec := records[id]; rec != nil {
		return rec
	}
	rec := &aiToolSessionRecord{
		ID:          id,
		ToolID:      cfg.ToolID,
		ToolType:    cfg.ToolType,
		DisplayName: cfg.DisplayName,
		CanContinue: true,
	}
	records[id] = rec
	return rec
}

func mapToAIToolSessions(records map[string]*aiToolSessionRecord) []aiToolSessionRecord {
	items := make([]aiToolSessionRecord, 0, len(records))
	for _, rec := range records {
		if rec.Title == "" {
			rec.Title = rec.ID
		}
		items = append(items, *rec)
	}
	return items
}

func updateAIToolTime(rec *aiToolSessionRecord, value time.Time) {
	if value.IsZero() {
		return
	}
	formatted := formatTime(value)
	if rec.CreatedAt == "" || formatted < rec.CreatedAt {
		rec.CreatedAt = formatted
	}
	if rec.UpdatedAt == "" || formatted > rec.UpdatedAt {
		rec.UpdatedAt = formatted
	}
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func unixSeconds(value int64) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	return time.Unix(value, 0).UTC()
}

func unixMillis(value int64) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(value).UTC()
}

func scanJSONLines(path string, visit func([]byte) error) error {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if err := visit([]byte(line)); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		return err
	}
	return nil
}

func stringFromMap(item map[string]any, key string) string {
	value, ok := item[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case float64:
		return strconv.FormatInt(int64(typed), 10)
	default:
		return fmt.Sprint(typed)
	}
}

func textFromContent(value any) string {
	items, ok := value.([]any)
	if !ok {
		return ""
	}
	parts := []string{}
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if text := stringFromMap(item, "text"); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func shortText(value string, limit int) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\r\n", "\n"))
	value = strings.Join(strings.Fields(value), " ")
	if limit > 0 && len(value) > limit {
		return value[:limit] + "..."
	}
	return value
}

func deleteAIToolSession(settings agentLocalSettings, toolID string, sessionID string) (aiToolDeleteResult, bool, error) {
	cfg, ok := findAIToolConfig(settings, toolID)
	if !ok {
		return aiToolDeleteResult{}, false, nil
	}
	result := aiToolDeleteResult{ToolID: toolID, SessionID: sessionID}
	switch cfg.ToolType {
	case "codex":
		if cfg.HistoryPath != "" {
			updated, err := rewriteJSONLExcluding(cfg.HistoryPath, func(line []byte) bool {
				var item struct {
					SessionID string `json:"session_id"`
				}
				_ = json.Unmarshal(line, &item)
				return item.SessionID == sessionID
			})
			if err != nil {
				return result, true, err
			}
			if updated {
				result.Updated = append(result.Updated, cfg.HistoryPath)
			}
		}
		sessions, err := scanCodexSessions(cfg)
		if err != nil {
			return result, true, err
		}
		for _, session := range sessions {
			if session.ID == sessionID && session.SourcePath != "" {
				moved, err := moveToAIToolTrash(cfg, session.SourcePath)
				if err != nil {
					return result, true, err
				}
				result.Moved = append(result.Moved, moved)
			}
		}
	case "claude":
		if cfg.HistoryPath != "" {
			updated, err := rewriteJSONLExcluding(cfg.HistoryPath, func(line []byte) bool {
				var item struct {
					SessionID string `json:"sessionId"`
				}
				_ = json.Unmarshal(line, &item)
				return item.SessionID == sessionID
			})
			if err != nil {
				return result, true, err
			}
			if updated {
				result.Updated = append(result.Updated, cfg.HistoryPath)
			}
		}
		if cfg.SessionsDir != "" {
			matches, _ := filepath.Glob(filepath.Join(cfg.SessionsDir, "*.json"))
			for _, path := range matches {
				body, err := os.ReadFile(path)
				if err != nil || !strings.Contains(string(body), sessionID) {
					continue
				}
				moved, err := moveToAIToolTrash(cfg, path)
				if err != nil {
					return result, true, err
				}
				result.Moved = append(result.Moved, moved)
			}
		}
	default:
		return result, true, fmt.Errorf("unsupported ai tool type %q", cfg.ToolType)
	}
	return result, true, nil
}

func rewriteJSONLExcluding(path string, shouldRemove func([]byte) bool) (bool, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	lines := strings.Split(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n")
	kept := []string{}
	removed := false
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if shouldRemove([]byte(line)) {
			removed = true
			continue
		}
		kept = append(kept, line)
	}
	if !removed {
		return false, nil
	}
	if _, err := copyToAIToolTrash(path); err != nil {
		return false, err
	}
	return true, os.WriteFile(path, []byte(strings.Join(kept, "\n")+"\n"), 0600)
}

func moveToAIToolTrash(cfg aiToolConfig, path string) (string, error) {
	target, err := trashPathForAITool(cfg.ToolID, path)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return "", err
	}
	if err := os.Rename(path, target); err != nil {
		return "", err
	}
	return target, nil
}

func copyToAIToolTrash(path string) (string, error) {
	target, err := trashPathForAITool("history-backup", path)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return "", err
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return target, os.WriteFile(target, body, 0600)
}

func trashPathForAITool(toolID string, source string) (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	stamp := time.Now().UTC().Format("20060102T150405Z")
	name := strings.ReplaceAll(filepath.Clean(source), ":", "")
	name = strings.ReplaceAll(name, string(filepath.Separator), "_")
	return filepath.Join(configDir, "GatePilot", "trash", "ai-tools", toolID, stamp+"_"+name), nil
}

func continueAIToolSession(settings agentLocalSettings, toolID string, sessionID string) (map[string]any, bool, error) {
	detail, ok, err := aiToolSessionDetail(settings, toolID, sessionID)
	if err != nil || !ok {
		return nil, ok, err
	}
	cfg, _ := findAIToolConfig(settings, toolID)
	commandLine := aiToolContinueCommand(cfg, detail.Session)
	if strings.TrimSpace(commandLine) == "" {
		return nil, true, fmt.Errorf("continue command is empty")
	}
	if err := startAIToolCommand(commandLine, detail.Session.WorkingDir); err != nil {
		return nil, true, err
	}
	return map[string]any{
		"tool_id":      toolID,
		"session_id":   sessionID,
		"command_line": commandLine,
		"working_dir":  detail.Session.WorkingDir,
	}, true, nil
}

func aiToolContinueCommand(cfg aiToolConfig, session aiToolSessionRecord) string {
	template := cfg.ContinueCommandTemplate
	if template == "" {
		switch cfg.ToolType {
		case "codex":
			template = firstNonEmptyLocal(cfg.ExecutablePath, "codex") + " resume {session_id}"
		case "claude":
			template = firstNonEmptyLocal(cfg.ExecutablePath, "claude") + " --resume {session_id}"
		}
	}
	replacements := map[string]string{
		"{session_id}":  session.ID,
		"{working_dir}": session.WorkingDir,
		"{tool_home}":   cfg.HomeDir,
	}
	for from, to := range replacements {
		template = strings.ReplaceAll(template, from, to)
	}
	return template
}

func startAIToolCommand(commandLine string, workingDir string) error {
	if workingDir == "" {
		workingDir = currentWorkingDir()
	}
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/c", "start", "GatePilot AI Session", "cmd", "/k", commandLine)
	} else {
		cmd = exec.Command("sh", "-lc", commandLine)
	}
	cmd.Dir = workingDir
	return cmd.Start()
}

func aiToolSessionURL(toolID string, sessionID string) string {
	values := url.Values{}
	values.Set("tool_id", toolID)
	values.Set("session_id", sessionID)
	return "/api/local/ai-tool-session?" + values.Encode()
}
