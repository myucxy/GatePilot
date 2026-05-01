package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	Mode                 string `json:"mode"`
	StartOnLogin         bool   `json:"start_on_login"`
	NotificationEnabled  bool   `json:"notification_enabled"`
	NotificationStyle    string `json:"notification_style"`
	HistoryRetentionDays int    `json:"history_retention_days"`
	CaptureOutputMode    string `json:"capture_output_mode"`
	DefaultCLIType       string `json:"default_cli_type"`
	ServerURL            string `json:"server_url"`
	TenantID             string `json:"tenant_id"`
	DeviceID             string `json:"device_id"`
	ClientInstanceID     string `json:"client_instance_id"`
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
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	client := http.Client{Timeout: 20 * time.Second}
	resp, err := client.Post("http://"+trayAddr+path, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	return decodeResponse(resp, target)
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
