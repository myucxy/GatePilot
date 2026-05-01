package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const trayAddr = "127.0.0.1:18731"

type App struct {
	ctx context.Context
}

type AgentLocalSettings struct {
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
	AITools              []AIToolConfig `json:"ai_tools"`
}

type AIToolConfig struct {
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

type AgentStatus struct {
	Settings     AgentLocalSettings `json:"settings"`
	LoggedIn     bool               `json:"logged_in"`
	Offline      bool               `json:"offline"`
	SettingsPath string             `json:"settings_path"`
	HistoryPath  string             `json:"history_path"`
	TrayAddr     string             `json:"tray_addr"`
}

type LoginRequest struct {
	ServerURL        string `json:"server_url"`
	TenantID         string `json:"tenant_id"`
	DeviceID         string `json:"device_id"`
	ClientInstanceID string `json:"client_instance_id"`
}

type SessionRecord struct {
	SessionID           string `json:"session_id"`
	CLIType             string `json:"cli_type"`
	CommandLineRedacted string `json:"command_line_redacted"`
	WorkingDir          string `json:"working_dir"`
	WorkingDirHash      string `json:"working_dir_hash"`
	Status              string `json:"status"`
	StartedAt           string `json:"started_at"`
	EndedAt             string `json:"ended_at,omitempty"`
	LastOutputSummary   string `json:"last_output_summary"`
	PendingApprovals    int    `json:"pending_approval_count"`
	ControlAddr         string `json:"control_addr,omitempty"`
}

type SessionList struct {
	Items []SessionRecord `json:"items"`
}

type SessionDetail struct {
	Session   SessionRecord    `json:"session"`
	Output    []map[string]any `json:"output"`
	Approvals []map[string]any `json:"approvals"`
	Decisions []map[string]any `json:"decisions"`
}

type AIToolSessionRecord struct {
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

type AIToolSessionList struct {
	Items []AIToolSessionRecord `json:"items"`
}

type AIToolMessageRecord struct {
	Timestamp string         `json:"timestamp"`
	Role      string         `json:"role"`
	Type      string         `json:"type"`
	Text      string         `json:"text"`
	Raw       map[string]any `json:"raw,omitempty"`
}

type AIToolSessionDetail struct {
	Session  AIToolSessionRecord   `json:"session"`
	Messages []AIToolMessageRecord `json:"messages"`
}

type AIToolConfigList struct {
	Items []AIToolConfig `json:"items"`
}

func NewApp() *App {
	return &App{}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	_ = a.EnsureAgent()
}

func (a *App) EnsureAgent() error {
	if trayHealthy() {
		return nil
	}
	exe, err := coreAgentPath()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "tray")
	applyHiddenWindow(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	for i := 0; i < 40; i++ {
		if trayHealthy() {
			return nil
		}
		time.Sleep(150 * time.Millisecond)
	}
	return fmt.Errorf("agent tray did not become ready")
}

func (a *App) InitialView() string {
	for _, arg := range os.Args[1:] {
		switch strings.ToLower(strings.TrimSpace(arg)) {
		case "--history", "history":
			return "history"
		case "--settings", "settings":
			return "settings"
		}
	}
	return "settings"
}

func (a *App) GetStatus() (AgentStatus, error) {
	var response struct {
		Data AgentStatus `json:"data"`
	}
	if err := getJSON("/api/local/status", &response); err != nil {
		return AgentStatus{}, err
	}
	return response.Data, nil
}

func (a *App) SaveSettings(settings AgentLocalSettings) (AgentLocalSettings, error) {
	var response struct {
		Data AgentLocalSettings `json:"data"`
	}
	if err := postJSON("/api/local/settings", settings, &response); err != nil {
		return AgentLocalSettings{}, err
	}
	return response.Data, nil
}

func (a *App) Login(req LoginRequest) (AgentStatus, error) {
	var response struct {
		Data AgentStatus `json:"data"`
	}
	if err := postJSON("/api/local/login", req, &response); err != nil {
		return AgentStatus{}, err
	}
	return response.Data, nil
}

func (a *App) Offline() (AgentStatus, error) {
	var response struct {
		Data AgentStatus `json:"data"`
	}
	if err := postJSON("/api/local/offline", map[string]any{}, &response); err != nil {
		return AgentStatus{}, err
	}
	return response.Data, nil
}

func (a *App) Logout() (AgentStatus, error) {
	var response struct {
		Data AgentStatus `json:"data"`
	}
	if err := postJSON("/api/local/logout", map[string]any{}, &response); err != nil {
		return AgentStatus{}, err
	}
	return response.Data, nil
}

func (a *App) ListSessions(cliType string, status string, limit int) (SessionList, error) {
	path := fmt.Sprintf("/api/local/sessions?limit=%d", limit)
	if strings.TrimSpace(cliType) != "" {
		path += "&cli_type=" + strings.TrimSpace(cliType)
	}
	if strings.TrimSpace(status) != "" {
		path += "&status=" + strings.TrimSpace(status)
	}
	var response struct {
		Data SessionList `json:"data"`
	}
	if err := getJSON(path, &response); err != nil {
		return SessionList{}, err
	}
	return response.Data, nil
}

func (a *App) GetSessionDetail(sessionID string) (SessionDetail, error) {
	var response struct {
		Data SessionDetail `json:"data"`
	}
	if err := getJSON("/api/local/sessions/"+sessionID, &response); err != nil {
		return SessionDetail{}, err
	}
	return response.Data, nil
}

func (a *App) ReplySession(sessionID string, text string) error {
	return postJSON("/api/local/sessions/"+sessionID+"/input", map[string]string{"text": text}, nil)
}

func (a *App) DetectAIToolDefaults() (AIToolConfigList, error) {
	var response struct {
		Data AIToolConfigList `json:"data"`
	}
	if err := getJSON("/api/local/ai-tools/defaults", &response); err != nil {
		return AIToolConfigList{}, err
	}
	return response.Data, nil
}

func (a *App) ListAIToolSessions(toolID string, query string, limit int) (AIToolSessionList, error) {
	values := url.Values{}
	if strings.TrimSpace(toolID) != "" {
		values.Set("tool_id", strings.TrimSpace(toolID))
	}
	if strings.TrimSpace(query) != "" {
		values.Set("query", strings.TrimSpace(query))
	}
	if limit > 0 {
		values.Set("limit", fmt.Sprintf("%d", limit))
	}
	path := "/api/local/ai-tools/sessions"
	if encoded := values.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var response struct {
		Data AIToolSessionList `json:"data"`
	}
	if err := getJSON(path, &response); err != nil {
		return AIToolSessionList{}, err
	}
	return response.Data, nil
}

func (a *App) GetAIToolSessionDetail(toolID string, sessionID string) (AIToolSessionDetail, error) {
	var response struct {
		Data AIToolSessionDetail `json:"data"`
	}
	if err := getJSON(aiToolSessionPath("/api/local/ai-tool-session", toolID, sessionID), &response); err != nil {
		return AIToolSessionDetail{}, err
	}
	return response.Data, nil
}

func (a *App) ContinueAIToolSession(toolID string, sessionID string) error {
	return postJSON(aiToolSessionPath("/api/local/ai-tool-session/continue", toolID, sessionID), map[string]any{}, nil)
}

func (a *App) DeleteAIToolSession(toolID string, sessionID string) error {
	return requestJSON(http.MethodDelete, aiToolSessionPath("/api/local/ai-tool-session", toolID, sessionID), nil, nil)
}

func coreAgentPath() (string, error) {
	if path := os.Getenv("GATEPILOT_AGENT_EXE"); path != "" {
		return path, nil
	}
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(self)
	candidates := []string{
		filepath.Join(dir, "gatepilot-agent.exe"),
		filepath.Join(dir, "gatepilot-agent"),
		filepath.Join(dir, "..", "gatepilot-agent-windows-amd64", "gatepilot-agent.exe"),
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("gatepilot-agent executable not found next to desktop app")
}

func trayHealthy() bool {
	client := http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Get("http://" + trayAddr + "/healthz")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func getJSON(path string, target any) error {
	client := http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get("http://" + trayAddr + path)
	if err != nil {
		return err
	}
	return decodeResponse(resp, target)
}

func postJSON(path string, payload any, target any) error {
	return requestJSON(http.MethodPost, path, payload, target)
}

func requestJSON(method string, path string, payload any, target any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	client := http.Client{Timeout: 20 * time.Second}
	req, err := http.NewRequest(method, "http://"+trayAddr+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	return decodeResponse(resp, target)
}

func aiToolSessionPath(base string, toolID string, sessionID string) string {
	values := url.Values{}
	values.Set("tool_id", toolID)
	values.Set("session_id", sessionID)
	return base + "?" + values.Encode()
}

func decodeResponse(resp *http.Response, target any) error {
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	if target == nil {
		return nil
	}
	if err := json.Unmarshal(body, target); err != nil {
		return err
	}
	return nil
}
