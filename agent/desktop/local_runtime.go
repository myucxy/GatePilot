package main

import (
	"bufio"
	"bytes"
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
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type agentLocalSettings = AgentLocalSettings
type aiToolConfig = AIToolConfig
type aiToolSessionRecord = AIToolSessionRecord
type aiToolMessageRecord = AIToolMessageRecord
type aiToolSessionDetailRecord = AIToolSessionDetail

type localHistory struct {
	Sessions     []SessionRecord       `json:"sessions"`
	Output       []localOutputRecord   `json:"output"`
	Approvals    []localApprovalRecord `json:"approvals"`
	Decisions    []localDecisionRecord `json:"decisions"`
	LastModified string                `json:"last_modified"`
}

type localOutputRecord struct {
	SessionID       string `json:"session_id"`
	SequenceNo      int64  `json:"sequence_no"`
	StreamType      string `json:"stream_type"`
	ContentRedacted string `json:"content_redacted"`
	ContentHash     string `json:"content_hash"`
	CreatedAt       string `json:"created_at"`
}

type localApprovalRecord struct {
	ApprovalID    string `json:"approval_id"`
	SessionID     string `json:"session_id"`
	CLIType       string `json:"cli_type"`
	EventType     string `json:"event_type"`
	RiskLevel     string `json:"risk_level"`
	PromptText    string `json:"prompt_text"`
	ContextBefore string `json:"context_before"`
	Status        string `json:"status"`
	CreatedAt     string `json:"created_at"`
	DecidedAt     string `json:"decided_at,omitempty"`
}

type localDecisionRecord struct {
	ApprovalID      string `json:"approval_id"`
	SessionID       string `json:"session_id"`
	DecisionType    string `json:"decision_type"`
	PayloadRedacted string `json:"payload_redacted"`
	BytesWritten    int    `json:"bytes_written"`
	Result          string `json:"result"`
	CreatedAt       string `json:"created_at"`
}

type localSessionFilter struct {
	CLIType string
	Status  string
	Limit   int
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

type localSessionInputRequest struct {
	Text string `json:"text"`
}

type runtimePendingApproval struct {
	Request  trayApprovalRequest
	Response chan trayDecisionResponse
}

type localRuntimeState struct {
	mu           sync.Mutex
	settings     agentLocalSettings
	pending      *runtimePendingApproval
	eventClients map[*runtimeEventClient]bool
}

type aiToolSessionFilter struct {
	ToolID string
	Query  string
	Limit  int
}

type aiToolDeleteResult struct {
	SessionID string   `json:"session_id"`
	ToolID    string   `json:"tool_id"`
	Moved     []string `json:"moved"`
	Updated   []string `json:"updated"`
}

type runtimeEventEnvelope struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

type runtimeEventClient struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

type gpRuntimeEvent struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

var runtimeWebSocketUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		host := strings.ToLower(r.Host)
		return strings.HasPrefix(host, "127.0.0.1:") || strings.HasPrefix(host, "localhost:")
	},
}

var (
	localRuntimeMu      sync.Mutex
	localRuntimeStarted bool
	localRuntimeStateV  *localRuntimeState
)

func startLocalRuntime() error {
	if trayHealthy() {
		return nil
	}
	localRuntimeMu.Lock()
	defer localRuntimeMu.Unlock()
	if localRuntimeStarted || trayHealthy() {
		return nil
	}
	settings, err := loadAgentLocalSettings()
	if err != nil {
		return err
	}
	state := &localRuntimeState{settings: settings, eventClients: map[*runtimeEventClient]bool{}}
	listener, err := net.Listen("tcp", trayAddr)
	if err != nil {
		if trayHealthy() {
			return nil
		}
		return err
	}
	server := &http.Server{Handler: newLocalRuntimeHandler(state)}
	go func() {
		_ = server.Serve(listener)
	}()
	localRuntimeStateV = state
	localRuntimeStarted = true
	return nil
}

func newLocalRuntimeHandler(state *localRuntimeState) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeRuntimeJSON(w, map[string]any{"status": "ok", "mode": state.currentSettings().Mode})
	})
	mux.HandleFunc("/api/local/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writeRuntimeJSON(w, map[string]any{"data": localAgentStatus(state)})
	})
	mux.HandleFunc("/api/local/settings", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeRuntimeJSON(w, map[string]any{"data": state.currentSettings()})
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
			writeRuntimeJSON(w, map[string]any{"data": settings})
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/local/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req LoginRequest
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
		writeRuntimeJSON(w, map[string]any{"data": localAgentStatus(state)})
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
		writeRuntimeJSON(w, map[string]any{"data": localAgentStatus(state)})
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
		writeRuntimeJSON(w, map[string]any{"data": localAgentStatus(state)})
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
		writeRuntimeJSON(w, map[string]any{"data": state.confirmApproval(req)})
	})
	mux.HandleFunc("/api/local/approvals/confirm-ws", func(w http.ResponseWriter, r *http.Request) {
		state.handleApprovalWebSocket(w, r)
	})
	mux.HandleFunc("/api/local/gp/events", func(w http.ResponseWriter, r *http.Request) {
		state.handleGPEventWebSocket(w, r)
	})
	mux.HandleFunc("/api/local/events", func(w http.ResponseWriter, r *http.Request) {
		state.handleEventSubscriberWebSocket(w, r)
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
		writeRuntimeJSON(w, map[string]any{"data": map[string]any{"items": items}})
	})
	mux.HandleFunc("/api/local/sessions/", handleRuntimeSessionScoped)
	mux.HandleFunc("/api/local/ai-tools/defaults", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writeRuntimeJSON(w, map[string]any{"data": map[string]any{"items": defaultAIToolConfigs()}})
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
		writeRuntimeJSON(w, map[string]any{"data": map[string]any{"items": items}})
	})
	mux.HandleFunc("/api/local/ai-tool-session", func(w http.ResponseWriter, r *http.Request) {
		handleRuntimeAIToolSession(w, r, state)
	})
	mux.HandleFunc("/api/local/ai-tool-session/continue", func(w http.ResponseWriter, r *http.Request) {
		handleRuntimeAIToolSessionContinue(w, r, state)
	})
	return mux
}

func localAgentStatus(state *localRuntimeState) AgentStatus {
	settings := state.currentSettings()
	settingsPath, _ := agentSettingsPath()
	historyPath, _ := localHistoryPath()
	return AgentStatus{
		Settings:     settings,
		LoggedIn:     agentSettingsLoggedIn(settings),
		Offline:      settings.Mode != "online",
		SettingsPath: settingsPath,
		HistoryPath:  historyPath,
		TrayAddr:     trayAddr,
	}
}

func (s *localRuntimeState) currentSettings() agentLocalSettings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings
}

func (s *localRuntimeState) setSettings(settings agentLocalSettings) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settings = settings
}

func (s *localRuntimeState) setPending(req trayApprovalRequest) *runtimePendingApproval {
	pending := &runtimePendingApproval{Request: req, Response: make(chan trayDecisionResponse, 1)}
	s.mu.Lock()
	s.pending = pending
	s.mu.Unlock()
	return pending
}

func (s *localRuntimeState) completePending(decision trayDecisionResponse) bool {
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

func (s *localRuntimeState) handleApprovalWebSocket(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	conn, err := runtimeWebSocketUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	var req trayApprovalRequest
	if err := conn.ReadJSON(&req); err != nil {
		_ = conn.WriteJSON(map[string]any{"type": "error", "error": err.Error()})
		return
	}
	if req.Approval.ApprovalID == "" {
		req.Approval.ApprovalID = "local"
	}
	decision := s.confirmApproval(req)
	_ = conn.WriteJSON(map[string]any{"type": "approval_decision", "data": decision})
}

func (s *localRuntimeState) handleGPEventWebSocket(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	conn, err := runtimeWebSocketUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	for {
		var event gpRuntimeEvent
		if err := conn.ReadJSON(&event); err != nil {
			return
		}
		event.Type = strings.TrimSpace(event.Type)
		if event.Type == "" {
			continue
		}
		s.handleGPEvent(event)
	}
}

func (s *localRuntimeState) handleGPEvent(event gpRuntimeEvent) {
	switch event.Type {
	case "session_started", "session_updated":
		var session SessionRecord
		if err := json.Unmarshal(event.Data, &session); err == nil && session.SessionID != "" {
			s.broadcastEvent(event.Type, session)
		}
	case "output":
		var output map[string]any
		if err := json.Unmarshal(event.Data, &output); err == nil {
			s.broadcastEvent("output", output)
		}
	case "approval", "decision":
		var item map[string]any
		if err := json.Unmarshal(event.Data, &item); err == nil {
			s.broadcastEvent(event.Type, item)
		}
	default:
		var item map[string]any
		_ = json.Unmarshal(event.Data, &item)
		s.broadcastEvent(event.Type, item)
	}
}

func (s *localRuntimeState) handleEventSubscriberWebSocket(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	conn, err := runtimeWebSocketUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	client := &runtimeEventClient{conn: conn}
	s.addEventClient(client)
	defer func() {
		s.removeEventClient(client)
		_ = conn.Close()
	}()
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}

func (s *localRuntimeState) addEventClient(client *runtimeEventClient) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.eventClients == nil {
		s.eventClients = map[*runtimeEventClient]bool{}
	}
	s.eventClients[client] = true
}

func (s *localRuntimeState) removeEventClient(client *runtimeEventClient) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.eventClients, client)
}

func (s *localRuntimeState) broadcastEvent(eventType string, data any) {
	s.mu.Lock()
	clients := make([]*runtimeEventClient, 0, len(s.eventClients))
	for client := range s.eventClients {
		clients = append(clients, client)
	}
	s.mu.Unlock()
	if len(clients) == 0 {
		return
	}
	message := runtimeEventEnvelope{Type: eventType, Data: data}
	for _, client := range clients {
		client.mu.Lock()
		_ = client.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
		err := client.conn.WriteJSON(message)
		client.mu.Unlock()
		if err != nil {
			s.removeEventClient(client)
			_ = client.conn.Close()
		}
	}
}

func (s *localRuntimeState) confirmApproval(req trayApprovalRequest) trayDecisionResponse {
	settings := s.currentSettings()
	pending := s.setPending(req)
	s.broadcastEvent("approval", map[string]any{
		"approval":    req.Approval,
		"working_dir": req.WorkingDir,
		"summary":     req.Summary,
	})
	if settings.NotificationEnabled && settings.NotificationStyle != "none" {
		go func() {
			decision, payload, err := showApprovalNotification(settings, req)
			if err != nil {
				decision = "reject"
				payload = ""
			}
			s.completePending(trayDecisionResponse{DecisionType: decision, Payload: payload, Result: "selected"})
		}()
	}
	select {
	case decision := <-pending.Response:
		s.broadcastEvent("decision", map[string]any{
			"approval_id":   req.Approval.ApprovalID,
			"session_id":    req.Approval.SessionID,
			"decision_type": decision.DecisionType,
			"payload":       decision.Payload,
			"result":        decision.Result,
		})
		return decision
	case <-time.After(10 * time.Minute):
		decision := trayDecisionResponse{DecisionType: "reject", Result: "timeout"}
		s.broadcastEvent("decision", map[string]any{
			"approval_id":   req.Approval.ApprovalID,
			"session_id":    req.Approval.SessionID,
			"decision_type": decision.DecisionType,
			"result":        decision.Result,
		})
		return decision
	}
}

func showApprovalNotification(settings agentLocalSettings, req trayApprovalRequest) (string, string, error) {
	message := approvalPopupText(req.Approval)
	if req.WorkingDir != "" {
		message = "当前会话目录: " + req.WorkingDir + "\n\n" + message
	}
	return platformApprovalPrompt("GatePilot 需要确认", message, settings.NotificationStyle)
}

func approvalPopupText(approval localApproval) string {
	lines := []string{
		"CLI: " + firstNonEmpty(approval.CLIType, "unknown"),
		"操作: " + firstNonEmpty(approval.EventType, "approval"),
		"风险: " + firstNonEmpty(approval.RiskLevel, "unknown"),
	}
	if approval.PromptText != "" {
		lines = append(lines, "", approval.PromptText)
	}
	if approval.ContextBefore != "" {
		lines = append(lines, "", approval.ContextBefore)
	}
	lines = append(lines, "", "选择“是/通过”写回批准，选择“否/拒绝”写回拒绝。")
	return strings.Join(lines, "\n")
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

func defaultAgentLocalSettings() agentLocalSettings {
	return agentLocalSettings{
		Mode:                 "offline",
		NotificationEnabled:  true,
		NotificationStyle:    "mini_window",
		HistoryRetentionDays: 30,
		CaptureOutputMode:    "summary_only",
		DefaultCLIType:       "custom",
		AITools:              defaultAIToolConfigs(),
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
	if settings.Mode != "online" {
		settings.Mode = defaults.Mode
	}
	switch settings.NotificationStyle {
	case "none", "toast", "mini_window", "modal_popup":
	default:
		settings.NotificationStyle = defaults.NotificationStyle
	}
	if settings.HistoryRetentionDays <= 0 {
		settings.HistoryRetentionDays = defaults.HistoryRetentionDays
	}
	switch settings.CaptureOutputMode {
	case "summary_only", "redacted_recent", "full_local_only":
	default:
		settings.CaptureOutputMode = defaults.CaptureOutputMode
	}
	if settings.DefaultCLIType == "" {
		settings.DefaultCLIType = defaults.DefaultCLIType
	}
	settings.AITools = normalizeAIToolConfigs(settings.AITools)
	if len(settings.AITools) == 0 {
		settings.AITools = defaults.AITools
	}
	return settings
}

func configureAgentDesktopLogin(options LoginRequest) (agentLocalSettings, error) {
	settings, err := loadAgentLocalSettings()
	if err != nil {
		return settings, err
	}
	if strings.TrimSpace(options.ServerURL) == "" {
		return settings, fmt.Errorf("服务端地址不能为空")
	}
	if strings.TrimSpace(options.DeviceID) == "" {
		options.DeviceID = hostname()
	}
	if strings.TrimSpace(options.ClientInstanceID) == "" {
		options.ClientInstanceID = "desktop_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	settings.Mode = "online"
	settings.ServerURL = strings.TrimSpace(options.ServerURL)
	settings.TenantID = strings.TrimSpace(options.TenantID)
	settings.DeviceID = strings.TrimSpace(options.DeviceID)
	settings.ClientInstanceID = strings.TrimSpace(options.ClientInstanceID)
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
	return settings.ServerURL != "" && settings.DeviceID != "" && settings.ClientInstanceID != ""
}

func configureStartupRegistration(enabled bool) error {
	if runtime.GOOS != "windows" {
		return nil
	}
	const runKey = `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`
	const valueName = "GatePilot Desktop"
	if !enabled {
		cmd := exec.Command("reg.exe", "delete", runKey, "/v", valueName, "/f")
		output, err := cmd.CombinedOutput()
		if err != nil && !strings.Contains(strings.ToLower(string(output)), "unable to find") {
			return fmt.Errorf("关闭开机启动失败: %v: %s", err, strings.TrimSpace(string(output)))
		}
		return nil
	}
	exePath, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command("reg.exe", "add", runKey, "/v", valueName, "/t", "REG_SZ", "/d", `"`+exePath+`"`, "/f")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("开启开机启动失败: %v: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func localHistoryPath() (string, error) {
	if path := os.Getenv("GATEPILOT_AGENT_HISTORY"); path != "" {
		return path, nil
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "GatePilot", "local-history.json"), nil
}

func loadLocalHistory() (localHistory, error) {
	path, err := localHistoryPath()
	if err != nil {
		return localHistory{}, err
	}
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return localHistory{}, nil
		}
		return localHistory{}, err
	}
	var history localHistory
	if err := json.Unmarshal(body, &history); err != nil {
		return localHistory{}, err
	}
	return history, nil
}

func queryLocalSessions(filter localSessionFilter) ([]SessionRecord, error) {
	history, err := loadLocalHistory()
	if err != nil {
		return nil, err
	}
	items := []SessionRecord{}
	for _, item := range history.Sessions {
		if filter.CLIType != "" && item.CLIType != filter.CLIType {
			continue
		}
		if filter.Status != "" && item.Status != filter.Status {
			continue
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].StartedAt > items[j].StartedAt
	})
	if filter.Limit > 0 && len(items) > filter.Limit {
		items = items[:filter.Limit]
	}
	return items, nil
}

func localSessionDetail(sessionID string) (SessionDetail, bool, error) {
	history, err := loadLocalHistory()
	if err != nil {
		return SessionDetail{}, false, err
	}
	for _, session := range history.Sessions {
		if session.SessionID != sessionID {
			continue
		}
		detail := SessionDetail{
			Session:   session,
			Output:    []map[string]any{},
			Approvals: []map[string]any{},
			Decisions: []map[string]any{},
		}
		for _, item := range history.Output {
			if item.SessionID == sessionID {
				detail.Output = append(detail.Output, structToMap(item))
			}
		}
		for _, item := range history.Approvals {
			if item.SessionID == sessionID {
				detail.Approvals = append(detail.Approvals, structToMap(item))
			}
		}
		for _, item := range history.Decisions {
			if item.SessionID == sessionID {
				detail.Decisions = append(detail.Decisions, structToMap(item))
			}
		}
		return detail, true, nil
	}
	return SessionDetail{}, false, nil
}

func handleRuntimeSessionScoped(w http.ResponseWriter, r *http.Request) {
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
		writeRuntimeJSON(w, map[string]any{"data": detail})
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
		writeRuntimeJSON(w, map[string]any{"data": map[string]any{"session_id": sessionID, "written": true}})
		return
	}
	http.NotFound(w, r)
}

func sendLocalSessionInput(sessionID string, text string) error {
	detail, ok, err := localSessionDetail(sessionID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("session not found")
	}
	session := detail.Session
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
	switch strings.TrimSpace(strings.ToLower(strings.ReplaceAll(value, "-", "_"))) {
	case "codex":
		return "codex"
	case "claude", "claude_code":
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
				SessionsDir:             filepath.Join(home, ".claude", "projects"),
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
	for _, cfg := range configuredAITools(settings) {
		if cfg.ToolID == strings.TrimSpace(toolID) {
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
		_ = scanCodexHistory(cfg, records)
	}
	if cfg.SessionsDir != "" {
		_ = scanCodexRollouts(cfg, records)
	}
	items := mapToAIToolSessions(records)
	sort.Slice(items, func(i, j int) bool { return items[i].UpdatedAt > items[j].UpdatedAt })
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
		rec.WorkingDir = firstNonEmpty(rec.WorkingDir, meta.CWD)
		rec.MessageCount += count
		updateAIToolTime(rec, meta.Timestamp)
		rec.Title = firstNonEmpty(rec.Title, meta.Nickname, filepath.Base(path))
		rec.Preview = firstNonEmpty(rec.Preview, meta.Nickname, filepath.Base(path))
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
		if ts := stringFromMap(item, "timestamp"); ts != "" {
			if parsed, err := time.Parse(time.RFC3339Nano, ts); err == nil && (meta.Timestamp.IsZero() || parsed.After(meta.Timestamp)) {
				meta.Timestamp = parsed.UTC()
			}
		}
		itemType := stringFromMap(item, "type")
		if itemType == "session_meta" {
			if payload, ok := item["payload"].(map[string]any); ok {
				meta.ID = stringFromMap(payload, "id")
				meta.CWD = stringFromMap(payload, "cwd")
				meta.Nickname = firstNonEmpty(stringFromMap(payload, "agent_nickname"), stringFromMap(payload, "agent_role"))
			}
		}
		if itemType == "response_item" || itemType == "event_msg" {
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
	if cfg.HistoryPath != "" {
		history, err := readCodexHistoryMessages(cfg.HistoryPath, session.ID)
		if err != nil {
			return nil, err
		}
		messages = append(messages, history...)
	}
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
		messages = append(messages, aiToolMessageRecord{Timestamp: formatTime(unixSeconds(item.TS)), Role: "user", Type: "history", Text: item.Text})
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
	msg.Timestamp = stringFromMap(item, "timestamp")
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
		msg.Text = firstNonEmpty(stringFromMap(payload, "type"), stringFromMap(payload, "message"))
	case "response_item":
		msg.Role = stringFromMap(payload, "role")
		msg.Text = textFromContent(payload["content"])
		if msg.Text == "" {
			msg.Text = firstNonEmpty(stringFromMap(payload, "type"), stringFromMap(payload, "status"))
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
			Display   string `json:"display"`
			Timestamp int64  `json:"timestamp"`
			Project   string `json:"project"`
			SessionID string `json:"sessionId"`
		}
		if err := json.Unmarshal(line, &item); err != nil || item.SessionID == "" {
			return nil
		}
		rec := ensureAIToolSession(records, cfg, item.SessionID)
		rec.MessageCount++
		rec.WorkingDir = firstNonEmpty(rec.WorkingDir, item.Project)
		updateAIToolTime(rec, unixMillis(item.Timestamp))
		rec.Title = firstNonEmpty(rec.Title, shortText(item.Display, 80))
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
	rec := &aiToolSessionRecord{ID: id, ToolID: cfg.ToolID, ToolType: cfg.ToolType, DisplayName: cfg.DisplayName, CanContinue: true}
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
		sessions, _ := scanCodexSessions(cfg)
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
	return map[string]any{"tool_id": toolID, "session_id": sessionID, "command_line": commandLine, "working_dir": detail.Session.WorkingDir}, true, nil
}

func aiToolContinueCommand(cfg aiToolConfig, session aiToolSessionRecord) string {
	template := cfg.ContinueCommandTemplate
	if template == "" {
		switch cfg.ToolType {
		case "codex":
			template = firstNonEmpty(cfg.ExecutablePath, "codex") + " resume {session_id}"
		case "claude":
			template = firstNonEmpty(cfg.ExecutablePath, "claude") + " --resume {session_id}"
		}
	}
	replacements := map[string]string{"{session_id}": session.ID, "{working_dir}": session.WorkingDir, "{tool_home}": cfg.HomeDir}
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

func handleRuntimeAIToolSession(w http.ResponseWriter, r *http.Request, state *localRuntimeState) {
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
		writeRuntimeJSON(w, map[string]any{"data": detail})
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
		writeRuntimeJSON(w, map[string]any{"data": result})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func handleRuntimeAIToolSessionContinue(w http.ResponseWriter, r *http.Request, state *localRuntimeState) {
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
	writeRuntimeJSON(w, map[string]any{"data": result})
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
	scanner := bufio.NewScanner(file)
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

func intQueryParam(r *http.Request, key string) int {
	value, _ := strconv.Atoi(r.URL.Query().Get(key))
	return value
}

func hostname() string {
	name, err := os.Hostname()
	if err != nil || strings.TrimSpace(name) == "" {
		return "unknown"
	}
	return name
}

func currentWorkingDir() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func structToMap(value any) map[string]any {
	body, _ := json.Marshal(value)
	var out map[string]any
	_ = json.Unmarshal(body, &out)
	return out
}

func writeRuntimeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func aiToolSessionURL(toolID string, sessionID string) string {
	values := url.Values{}
	values.Set("tool_id", toolID)
	values.Set("session_id", sessionID)
	return "/api/local/ai-tool-session?" + values.Encode()
}
