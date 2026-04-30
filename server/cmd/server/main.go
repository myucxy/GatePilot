package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const version = "0.1.0-dev"

type envelope struct {
	Data      any    `json:"data"`
	RequestID string `json:"request_id"`
	TraceID   string `json:"trace_id"`
}

type activationCode struct {
	TenantID  string
	Name      string
	Code      string
	ExpiresAt time.Time
	Consumed  bool
}

type device struct {
	DeviceID  string `json:"device_id"`
	TenantID  string `json:"tenant_id"`
	Name      string `json:"name"`
	Platform  string `json:"platform"`
	Arch      string `json:"arch"`
	Status    string `json:"status"`
	LastSeen  string `json:"last_seen_at"`
	CreatedAt string `json:"created_at"`
}

type session struct {
	SessionID         string `json:"session_id"`
	TenantID          string `json:"tenant_id"`
	DeviceID          string `json:"device_id"`
	CLIType           string `json:"cli_type"`
	Status            string `json:"status"`
	StartedAt         string `json:"started_at"`
	LastOutputSummary string `json:"last_output_summary"`
	PendingApprovals  int    `json:"pending_approval_count"`
}

type approval struct {
	ApprovalID      string `json:"approval_id"`
	TenantID        string `json:"tenant_id"`
	DeviceID        string `json:"device_id"`
	SessionID       string `json:"session_id"`
	CLIType         string `json:"cli_type"`
	EventType       string `json:"event_type"`
	RiskLevel       string `json:"risk_level"`
	PromptText      string `json:"prompt_text"`
	ContextBefore   string `json:"context_before"`
	Status          string `json:"status"`
	DecisionType    string `json:"decision_type"`
	DecisionPayload string `json:"decision_payload"`
	DecidedAt       string `json:"decided_at"`
	CreatedAt       string `json:"created_at"`
	ExpiresAt       string `json:"expires_at"`
}

type memoryStore struct {
	mu              sync.Mutex
	activationCodes map[string]activationCode
	devices         map[string]device
	sessions        map[string]session
	approvals       map[string]approval
}

var store = memoryStore{
	activationCodes: map[string]activationCode{},
	devices:         map[string]device{},
	sessions:        map[string]session{},
	approvals:       map[string]approval{},
}

func main() {
	addr := getenv("GATEPILOT_SERVER_ADDR", ":8080")

	// M0 阶段先暴露健康检查和当前用户接口，后续模块按 docs/03-detailed-design.md 拆入 domain service。
	log.Printf("gatepilot server listening on %s", addr)
	if err := http.ListenAndServe(addr, newRouter()); err != nil {
		log.Fatal(err)
	}
}

func newRouter() http.Handler {
	// 路由集中在这里，测试可以直接复用同一套 HTTP 行为，避免脚本和单测走出不同契约。
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/healthz", healthHandler)
	mux.HandleFunc("/api/v1/me", meHandler)
	mux.HandleFunc("/api/v1/agent/register", agentRegisterHandler)
	mux.HandleFunc("/api/v1/agent/sessions", agentSessionsHandler)
	mux.HandleFunc("/api/v1/agent/approvals", agentApprovalsHandler)
	mux.HandleFunc("/api/v1/approvals/", approvalScopedHandler)
	mux.HandleFunc("/api/v1/devices/", deviceScopedHandler)
	mux.HandleFunc("/api/v1/tenants/", tenantScopedHandler)

	return requestLog(cors(mux))
}

func approvalScopedHandler(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/approvals/"), "/")
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}

	approvalID := parts[0]
	resource := parts[1]
	switch {
	case resource == "decision" && r.Method == http.MethodPost:
		submitApprovalDecisionHandler(w, r, approvalID)
	default:
		http.NotFound(w, r)
	}
}

func deviceScopedHandler(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/devices/"), "/")
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}

	deviceID := parts[0]
	resource := parts[1]
	switch {
	case resource == "sessions" && r.Method == http.MethodGet:
		listDeviceSessionsHandler(w, r, deviceID)
	default:
		http.NotFound(w, r)
	}
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	writeJSON(w, envelope{
		Data: map[string]string{
			"status":  "ok",
			"service": "gatepilot-server",
			"version": version,
		},
		RequestID: requestID(r),
		TraceID:   traceID(r),
	})
}

func meHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 本地开发占位数据用于 Web/Mobile 先打通契约和页面状态，接入 OIDC 后替换为真实用户上下文。
	writeJSON(w, envelope{
		Data: map[string]any{
			"user_id":      "00000000-0000-0000-0000-000000000001",
			"email":        "owner@example.local",
			"display_name": "Local Owner",
			"tenants": []map[string]any{
				{
					"tenant_id":   "00000000-0000-0000-0000-000000000100",
					"role":        "owner",
					"permissions": []string{"tenant:admin", "device:admin", "approval:approve"},
				},
			},
		},
		RequestID: requestID(r),
		TraceID:   traceID(r),
	})
}

func tenantScopedHandler(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/tenants/"), "/")
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}

	tenantID := parts[0]
	resource := parts[1]
	switch {
	case resource == "device-activation-codes" && r.Method == http.MethodPost:
		createActivationCodeHandler(w, r, tenantID)
	case resource == "devices" && r.Method == http.MethodGet:
		listDevicesHandler(w, r, tenantID)
	case resource == "approvals" && r.Method == http.MethodGet:
		listApprovalsHandler(w, r, tenantID)
	default:
		http.NotFound(w, r)
	}
}

func createActivationCodeHandler(w http.ResponseWriter, r *http.Request, tenantID string) {
	var req struct {
		Name             string `json:"name"`
		ExpiresInSeconds int    `json:"expires_in_seconds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, "message_schema_invalid", err.Error())
		return
	}
	if req.Name == "" {
		req.Name = "New Device"
	}
	if req.ExpiresInSeconds <= 0 {
		req.ExpiresInSeconds = 600
	}

	code := "GP-" + randomHex(3) + "-" + randomHex(3)
	expiresAt := time.Now().UTC().Add(time.Duration(req.ExpiresInSeconds) * time.Second)

	store.mu.Lock()
	store.activationCodes[code] = activationCode{
		TenantID:  tenantID,
		Name:      req.Name,
		Code:      code,
		ExpiresAt: expiresAt,
	}
	store.mu.Unlock()

	writeStatusJSON(w, http.StatusCreated, envelope{
		Data: map[string]any{
			"activation_code": code,
			"expires_at":      expiresAt.Format(time.RFC3339),
		},
		RequestID: requestID(r),
		TraceID:   traceID(r),
	})
}

func listDevicesHandler(w http.ResponseWriter, r *http.Request, tenantID string) {
	items := []device{}

	store.mu.Lock()
	for _, item := range store.devices {
		if item.TenantID == tenantID {
			items = append(items, item)
		}
	}
	store.mu.Unlock()

	writeJSON(w, envelope{
		Data: map[string]any{
			"items":       items,
			"next_cursor": nil,
			"has_more":    false,
		},
		RequestID: requestID(r),
		TraceID:   traceID(r),
	})
}

func agentRegisterHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ActivationCode  string         `json:"activation_code"`
		DeviceName      string         `json:"device_name"`
		Platform        string         `json:"platform"`
		Arch            string         `json:"arch"`
		AgentVersion    string         `json:"agent_version"`
		ProtocolVersion string         `json:"protocol_version"`
		Capabilities    map[string]any `json:"capabilities"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, "message_schema_invalid", err.Error())
		return
	}

	now := time.Now().UTC()
	store.mu.Lock()
	code, ok := store.activationCodes[req.ActivationCode]
	if !ok || code.Consumed || now.After(code.ExpiresAt) {
		store.mu.Unlock()
		writeError(w, r, http.StatusUnprocessableEntity, "activation_code_invalid", "activation code is invalid")
		return
	}

	deviceID := "dev_" + randomHex(12)
	deviceToken := "dt_" + randomHex(24)
	created := now.Format(time.RFC3339)
	store.devices[deviceID] = device{
		DeviceID:  deviceID,
		TenantID:  code.TenantID,
		Name:      firstNonEmpty(req.DeviceName, code.Name),
		Platform:  req.Platform,
		Arch:      req.Arch,
		Status:    "active",
		LastSeen:  created,
		CreatedAt: created,
	}
	code.Consumed = true
	store.activationCodes[req.ActivationCode] = code
	store.mu.Unlock()

	writeStatusJSON(w, http.StatusCreated, envelope{
		Data: map[string]any{
			"device_id":     deviceID,
			"device_token":  deviceToken,
			"server_ws_url": "ws://127.0.0.1:8080/ws/agent",
		},
		RequestID: requestID(r),
		TraceID:   traceID(r),
	})
}

func agentSessionsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		DeviceID            string `json:"device_id"`
		CLIType             string `json:"cli_type"`
		CommandLineRedacted string `json:"command_line_redacted"`
		WorkingDirHash      string `json:"working_dir_hash"`
		LastOutputSummary   string `json:"last_output_summary"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, "message_schema_invalid", err.Error())
		return
	}
	if req.CLIType == "" {
		req.CLIType = "custom"
	}

	now := time.Now().UTC()
	store.mu.Lock()
	deviceItem, ok := store.devices[req.DeviceID]
	if !ok {
		store.mu.Unlock()
		writeError(w, r, http.StatusNotFound, "device_offline", "device not found")
		return
	}

	sessionID := "ses_" + randomHex(12)
	item := session{
		SessionID:         sessionID,
		TenantID:          deviceItem.TenantID,
		DeviceID:          req.DeviceID,
		CLIType:           req.CLIType,
		Status:            "running",
		StartedAt:         now.Format(time.RFC3339),
		LastOutputSummary: firstNonEmpty(req.LastOutputSummary, "fake CLI session started"),
		PendingApprovals:  0,
	}
	store.sessions[sessionID] = item
	store.mu.Unlock()

	writeStatusJSON(w, http.StatusCreated, envelope{
		Data:      item,
		RequestID: requestID(r),
		TraceID:   traceID(r),
	})
}

func agentApprovalsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		DeviceID      string   `json:"device_id"`
		SessionID     string   `json:"session_id"`
		CLIType       string   `json:"cli_type"`
		EventType     string   `json:"event_type"`
		RiskLevel     string   `json:"risk_level"`
		PromptText    string   `json:"prompt_text"`
		ContextBefore string   `json:"context_before"`
		Suggested     []string `json:"suggested_actions"`
		ExpiresIn     int      `json:"expires_in_seconds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, "message_schema_invalid", err.Error())
		return
	}
	if req.EventType == "" {
		req.EventType = "permission_request"
	}
	if req.RiskLevel == "" {
		req.RiskLevel = "high"
	}
	if req.ExpiresIn <= 0 {
		req.ExpiresIn = 300
	}

	now := time.Now().UTC()
	store.mu.Lock()
	sessionItem, ok := store.sessions[req.SessionID]
	if !ok || sessionItem.DeviceID != req.DeviceID {
		store.mu.Unlock()
		writeError(w, r, http.StatusNotFound, "agent_session_not_found", "session not found")
		return
	}

	approvalID := "apr_" + randomHex(12)
	item := approval{
		ApprovalID:    approvalID,
		TenantID:      sessionItem.TenantID,
		DeviceID:      req.DeviceID,
		SessionID:     req.SessionID,
		CLIType:       firstNonEmpty(req.CLIType, sessionItem.CLIType),
		EventType:     req.EventType,
		RiskLevel:     req.RiskLevel,
		PromptText:    firstNonEmpty(req.PromptText, "allow command execution?"),
		ContextBefore: req.ContextBefore,
		Status:        "waiting_decision",
		CreatedAt:     now.Format(time.RFC3339),
		ExpiresAt:     now.Add(time.Duration(req.ExpiresIn) * time.Second).Format(time.RFC3339),
	}
	store.approvals[approvalID] = item
	sessionItem.Status = "waiting_approval"
	sessionItem.PendingApprovals++
	store.sessions[req.SessionID] = sessionItem
	store.mu.Unlock()

	writeStatusJSON(w, http.StatusCreated, envelope{
		Data:      item,
		RequestID: requestID(r),
		TraceID:   traceID(r),
	})
}

func listApprovalsHandler(w http.ResponseWriter, r *http.Request, tenantID string) {
	status := r.URL.Query().Get("status")
	items := []approval{}

	store.mu.Lock()
	for _, item := range store.approvals {
		if item.TenantID != tenantID {
			continue
		}
		if status != "" && item.Status != status {
			continue
		}
		items = append(items, item)
	}
	store.mu.Unlock()

	writeJSON(w, envelope{
		Data: map[string]any{
			"items":       items,
			"next_cursor": nil,
			"has_more":    false,
		},
		RequestID: requestID(r),
		TraceID:   traceID(r),
	})
}

func submitApprovalDecisionHandler(w http.ResponseWriter, r *http.Request, approvalID string) {
	var req struct {
		DecisionType string `json:"decision_type"`
		Payload      string `json:"payload"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, "message_schema_invalid", err.Error())
		return
	}
	if req.DecisionType == "" {
		req.DecisionType = "approve"
	}

	now := time.Now().UTC().Format(time.RFC3339)
	store.mu.Lock()
	item, ok := store.approvals[approvalID]
	if !ok {
		store.mu.Unlock()
		writeError(w, r, http.StatusNotFound, "approval_not_found", "approval not found")
		return
	}
	if item.Status != "waiting_decision" {
		store.mu.Unlock()
		writeError(w, r, http.StatusConflict, "approval_already_decided", "approval already decided")
		return
	}

	item.Status = "delivered"
	item.DecisionType = req.DecisionType
	item.DecisionPayload = req.Payload
	item.DecidedAt = now
	store.approvals[approvalID] = item

	if sessionItem, ok := store.sessions[item.SessionID]; ok {
		sessionItem.Status = "running"
		if sessionItem.PendingApprovals > 0 {
			sessionItem.PendingApprovals--
		}
		sessionItem.LastOutputSummary = "approval " + req.DecisionType + " delivered"
		store.sessions[item.SessionID] = sessionItem
	}
	store.mu.Unlock()

	writeJSON(w, envelope{
		Data:      item,
		RequestID: requestID(r),
		TraceID:   traceID(r),
	})
}

func listDeviceSessionsHandler(w http.ResponseWriter, r *http.Request, deviceID string) {
	items := []session{}

	store.mu.Lock()
	for _, item := range store.sessions {
		if item.DeviceID == deviceID {
			items = append(items, item)
		}
	}
	store.mu.Unlock()

	writeJSON(w, envelope{
		Data: map[string]any{
			"items":       items,
			"next_cursor": nil,
			"has_more":    false,
		},
		RequestID: requestID(r),
		TraceID:   traceID(r),
	})
}

func writeJSON(w http.ResponseWriter, body any) {
	writeStatusJSON(w, http.StatusOK, body)
}

func writeStatusJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code string, message string) {
	writeStatusJSON(w, status, map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": message,
			"details": map[string]any{},
		},
		"request_id": requestID(r),
		"trace_id":   traceID(r),
	})
}

func requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start))
	})
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key, X-Request-Id, X-Client-Instance-Id")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func requestID(r *http.Request) string {
	if v := r.Header.Get("X-Request-Id"); v != "" {
		return v
	}
	return "req_local"
}

func traceID(r *http.Request) string {
	if v := r.Header.Get("X-Trace-Id"); v != "" {
		return v
	}
	return "tr_local"
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func randomHex(bytes int) string {
	buffer := make([]byte, bytes)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return strings.ToUpper(hex.EncodeToString(buffer))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
