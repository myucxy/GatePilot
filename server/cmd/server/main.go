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
	ApprovalID      string            `json:"approval_id"`
	TenantID        string            `json:"tenant_id"`
	DeviceID        string            `json:"device_id"`
	SessionID       string            `json:"session_id"`
	CLIType         string            `json:"cli_type"`
	EventType       string            `json:"event_type"`
	RiskLevel       string            `json:"risk_level"`
	PromptText      string            `json:"prompt_text"`
	ContextBefore   string            `json:"context_before"`
	Status          string            `json:"status"`
	DecisionType    string            `json:"decision_type"`
	DecisionPayload string            `json:"decision_payload"`
	DeliveryID      string            `json:"delivery_id"`
	DeliveryStatus  string            `json:"delivery_status"`
	DecidedBy       map[string]string `json:"decided_by"`
	DecidedAt       string            `json:"decided_at"`
	CreatedAt       string            `json:"created_at"`
	ExpiresAt       string            `json:"expires_at"`
}

type createActivationCodeRequest struct {
	Name             string `json:"name"`
	ExpiresInSeconds int    `json:"expires_in_seconds"`
}

type registerAgentRequest struct {
	ActivationCode  string         `json:"activation_code"`
	DeviceName      string         `json:"device_name"`
	Platform        string         `json:"platform"`
	Arch            string         `json:"arch"`
	AgentVersion    string         `json:"agent_version"`
	ProtocolVersion string         `json:"protocol_version"`
	Capabilities    map[string]any `json:"capabilities"`
}

type createAgentSessionRequest struct {
	DeviceID            string `json:"device_id"`
	CLIType             string `json:"cli_type"`
	CommandLineRedacted string `json:"command_line_redacted"`
	WorkingDirHash      string `json:"working_dir_hash"`
	LastOutputSummary   string `json:"last_output_summary"`
}

type createAgentApprovalRequest struct {
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

type submitApprovalDecisionRequest struct {
	DecisionType string `json:"decision_type"`
	Payload      string `json:"payload"`
}

type ackApprovalDecisionRequest struct {
	ApprovalID string         `json:"approval_id"`
	DeliveryID string         `json:"delivery_id"`
	SessionID  string         `json:"session_id"`
	AckResult  string         `json:"ack_result"`
	Detail     map[string]any `json:"detail"`
}

type appError struct {
	HTTPStatus int
	Code       string
	Message    string
}

type gatePilotStore interface {
	CreateActivationCode(tenantID string, req createActivationCodeRequest, now time.Time) (string, time.Time)
	ListDevices(tenantID string) []device
	RegisterAgent(req registerAgentRequest, now time.Time) (device, string, *appError)
	CreateSession(req createAgentSessionRequest, now time.Time) (session, *appError)
	CreateApproval(req createAgentApprovalRequest, now time.Time) (approval, *appError)
	ListApprovals(tenantID string, status string) []approval
	SubmitApprovalDecision(approvalID string, req submitApprovalDecisionRequest, decidedBy map[string]string, now time.Time) (approval, *appError)
	AckApprovalDecision(req ackApprovalDecisionRequest) (map[string]any, *appError)
	ListDeviceSessions(deviceID string) []session
}

type memoryStore struct {
	mu              sync.Mutex
	activationCodes map[string]activationCode
	devices         map[string]device
	sessions        map[string]session
	approvals       map[string]approval
}

var store gatePilotStore = newMemoryStore()

func newMemoryStore() *memoryStore {
	return &memoryStore{
		activationCodes: map[string]activationCode{},
		devices:         map[string]device{},
		sessions:        map[string]session{},
		approvals:       map[string]approval{},
	}
}

func (s *memoryStore) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.activationCodes = map[string]activationCode{}
	s.devices = map[string]device{}
	s.sessions = map[string]session{}
	s.approvals = map[string]approval{}
}

func (s *memoryStore) CreateActivationCode(tenantID string, req createActivationCodeRequest, now time.Time) (string, time.Time) {
	if req.Name == "" {
		req.Name = "New Device"
	}
	if req.ExpiresInSeconds <= 0 {
		req.ExpiresInSeconds = 600
	}

	code := "GP-" + randomHex(3) + "-" + randomHex(3)
	expiresAt := now.Add(time.Duration(req.ExpiresInSeconds) * time.Second)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.activationCodes[code] = activationCode{
		TenantID:  tenantID,
		Name:      req.Name,
		Code:      code,
		ExpiresAt: expiresAt,
	}
	return code, expiresAt
}

func (s *memoryStore) ListDevices(tenantID string) []device {
	items := []device{}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range s.devices {
		if item.TenantID == tenantID {
			items = append(items, item)
		}
	}
	return items
}

func (s *memoryStore) RegisterAgent(req registerAgentRequest, now time.Time) (device, string, *appError) {
	s.mu.Lock()
	defer s.mu.Unlock()

	code, ok := s.activationCodes[req.ActivationCode]
	if !ok || code.Consumed || now.After(code.ExpiresAt) {
		return device{}, "", &appError{HTTPStatus: http.StatusUnprocessableEntity, Code: "activation_code_invalid", Message: "activation code is invalid"}
	}

	deviceID := randomUUID()
	deviceToken := "dt_" + randomHex(24)
	created := now.Format(time.RFC3339)
	item := device{
		DeviceID:  deviceID,
		TenantID:  code.TenantID,
		Name:      firstNonEmpty(req.DeviceName, code.Name),
		Platform:  req.Platform,
		Arch:      req.Arch,
		Status:    "active",
		LastSeen:  created,
		CreatedAt: created,
	}
	s.devices[deviceID] = item
	code.Consumed = true
	s.activationCodes[req.ActivationCode] = code
	return item, deviceToken, nil
}

func (s *memoryStore) CreateSession(req createAgentSessionRequest, now time.Time) (session, *appError) {
	if req.CLIType == "" {
		req.CLIType = "custom"
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	deviceItem, ok := s.devices[req.DeviceID]
	if !ok {
		return session{}, &appError{HTTPStatus: http.StatusNotFound, Code: "device_offline", Message: "device not found"}
	}

	sessionID := randomUUID()
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
	s.sessions[sessionID] = item
	return item, nil
}

func (s *memoryStore) CreateApproval(req createAgentApprovalRequest, now time.Time) (approval, *appError) {
	if req.EventType == "" {
		req.EventType = "permission_request"
	}
	if req.RiskLevel == "" {
		req.RiskLevel = "high"
	}
	if req.ExpiresIn <= 0 {
		req.ExpiresIn = 300
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	sessionItem, ok := s.sessions[req.SessionID]
	if !ok || sessionItem.DeviceID != req.DeviceID {
		return approval{}, &appError{HTTPStatus: http.StatusNotFound, Code: "agent_session_not_found", Message: "session not found"}
	}

	approvalID := randomUUID()
	item := approval{
		ApprovalID:     approvalID,
		TenantID:       sessionItem.TenantID,
		DeviceID:       req.DeviceID,
		SessionID:      req.SessionID,
		CLIType:        firstNonEmpty(req.CLIType, sessionItem.CLIType),
		EventType:      req.EventType,
		RiskLevel:      req.RiskLevel,
		PromptText:     firstNonEmpty(req.PromptText, "allow command execution?"),
		ContextBefore:  req.ContextBefore,
		Status:         "waiting_decision",
		DeliveryStatus: "pending",
		CreatedAt:      now.Format(time.RFC3339),
		ExpiresAt:      now.Add(time.Duration(req.ExpiresIn) * time.Second).Format(time.RFC3339),
	}
	s.approvals[approvalID] = item
	sessionItem.Status = "waiting_approval"
	sessionItem.PendingApprovals++
	s.sessions[req.SessionID] = sessionItem
	return item, nil
}

func (s *memoryStore) ListApprovals(tenantID string, status string) []approval {
	items := []approval{}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range s.approvals {
		if item.TenantID != tenantID {
			continue
		}
		if status != "" && item.Status != status {
			continue
		}
		items = append(items, item)
	}
	return items
}

func (s *memoryStore) SubmitApprovalDecision(approvalID string, req submitApprovalDecisionRequest, decidedBy map[string]string, now time.Time) (approval, *appError) {
	if req.DecisionType == "" {
		req.DecisionType = "approve"
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.approvals[approvalID]
	if !ok {
		return approval{}, &appError{HTTPStatus: http.StatusNotFound, Code: "approval_not_found", Message: "approval not found"}
	}
	if item.Status != "waiting_decision" {
		return approval{}, &appError{HTTPStatus: http.StatusConflict, Code: "approval_already_decided", Message: "approval already decided"}
	}

	// 用户提交决策只代表控制面已接收，真正完成必须等待 Agent 回写 CLI 后 ACK。
	item.Status = "delivering"
	item.DecisionType = req.DecisionType
	item.DecisionPayload = req.Payload
	item.DeliveryID = randomUUID()
	item.DeliveryStatus = "sent"
	item.DecidedBy = decidedBy
	item.DecidedAt = now.Format(time.RFC3339)
	s.approvals[approvalID] = item

	if sessionItem, ok := s.sessions[item.SessionID]; ok {
		sessionItem.Status = "waiting_approval"
		sessionItem.LastOutputSummary = "approval " + req.DecisionType + " delivering"
		s.sessions[item.SessionID] = sessionItem
	}
	return item, nil
}

func (s *memoryStore) AckApprovalDecision(req ackApprovalDecisionRequest) (map[string]any, *appError) {
	if req.AckResult == "" {
		req.AckResult = "written"
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.approvals[req.ApprovalID]
	if !ok {
		return nil, &appError{HTTPStatus: http.StatusNotFound, Code: "approval_not_found", Message: "approval not found"}
	}
	if item.Status == "delivered" && item.DeliveryStatus == "acked" {
		return approvalAckData(item), nil
	}
	if item.Status != "delivering" || item.SessionID != req.SessionID || item.DeliveryID != req.DeliveryID {
		return nil, &appError{HTTPStatus: http.StatusConflict, Code: "delivery_failed", Message: "delivery ack does not match an active delivery"}
	}

	switch req.AckResult {
	case "written", "accepted":
		item.Status = "delivered"
		item.DeliveryStatus = "acked"
	default:
		item.Status = "delivery_failed"
		item.DeliveryStatus = "failed"
	}
	s.approvals[item.ApprovalID] = item

	if sessionItem, ok := s.sessions[item.SessionID]; ok {
		sessionItem.Status = "running"
		if sessionItem.PendingApprovals > 0 {
			sessionItem.PendingApprovals--
		}
		sessionItem.LastOutputSummary = "approval " + item.DecisionType + " " + item.Status
		s.sessions[item.SessionID] = sessionItem
	}
	return approvalAckData(item), nil
}

func (s *memoryStore) ListDeviceSessions(deviceID string) []session {
	items := []session{}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range s.sessions {
		if item.DeviceID == deviceID {
			items = append(items, item)
		}
	}
	return items
}

func approvalAckData(item approval) map[string]any {
	return map[string]any{
		"approval_id":     item.ApprovalID,
		"delivery_id":     item.DeliveryID,
		"status":          item.Status,
		"delivery_status": item.DeliveryStatus,
	}
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
	mux.HandleFunc("/api/v1/agent/approval-acks", agentApprovalAcksHandler)
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
	var req createActivationCodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, "message_schema_invalid", err.Error())
		return
	}
	code, expiresAt := store.CreateActivationCode(tenantID, req, time.Now().UTC())

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
	writeJSON(w, envelope{
		Data: map[string]any{
			"items":       store.ListDevices(tenantID),
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

	var req registerAgentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, "message_schema_invalid", err.Error())
		return
	}
	item, deviceToken, appErr := store.RegisterAgent(req, time.Now().UTC())
	if appErr != nil {
		writeAppError(w, r, appErr)
		return
	}

	writeStatusJSON(w, http.StatusCreated, envelope{
		Data: map[string]any{
			"device_id":     item.DeviceID,
			"device_token":  deviceToken,
			"server_ws_url": "ws://" + r.Host + "/ws/agent",
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

	var req createAgentSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, "message_schema_invalid", err.Error())
		return
	}
	item, appErr := store.CreateSession(req, time.Now().UTC())
	if appErr != nil {
		writeAppError(w, r, appErr)
		return
	}

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

	var req createAgentApprovalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, "message_schema_invalid", err.Error())
		return
	}
	item, appErr := store.CreateApproval(req, time.Now().UTC())
	if appErr != nil {
		writeAppError(w, r, appErr)
		return
	}

	writeStatusJSON(w, http.StatusCreated, envelope{
		Data:      item,
		RequestID: requestID(r),
		TraceID:   traceID(r),
	})
}

func listApprovalsHandler(w http.ResponseWriter, r *http.Request, tenantID string) {
	status := r.URL.Query().Get("status")

	writeJSON(w, envelope{
		Data: map[string]any{
			"items":       store.ListApprovals(tenantID, status),
			"next_cursor": nil,
			"has_more":    false,
		},
		RequestID: requestID(r),
		TraceID:   traceID(r),
	})
}

func submitApprovalDecisionHandler(w http.ResponseWriter, r *http.Request, approvalID string) {
	var req submitApprovalDecisionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, "message_schema_invalid", err.Error())
		return
	}
	decidedBy := map[string]string{
		"actor_type":         "user",
		"actor_id":           "00000000-0000-0000-0000-000000000001",
		"display_name":       "Local Owner",
		"client_instance_id": firstNonEmpty(r.Header.Get("X-Client-Instance-Id"), "00000000-0000-0000-0000-000000000200"),
		"client_type":        "web",
	}
	item, appErr := store.SubmitApprovalDecision(approvalID, req, decidedBy, time.Now().UTC())
	if appErr != nil {
		writeAppError(w, r, appErr)
		return
	}

	writeJSON(w, envelope{
		Data:      item,
		RequestID: requestID(r),
		TraceID:   traceID(r),
	})
}

func agentApprovalAcksHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ackApprovalDecisionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, "message_schema_invalid", err.Error())
		return
	}
	data, appErr := store.AckApprovalDecision(req)
	if appErr != nil {
		writeAppError(w, r, appErr)
		return
	}

	writeJSON(w, envelope{
		Data:      data,
		RequestID: requestID(r),
		TraceID:   traceID(r),
	})
}

func listDeviceSessionsHandler(w http.ResponseWriter, r *http.Request, deviceID string) {
	writeJSON(w, envelope{
		Data: map[string]any{
			"items":       store.ListDeviceSessions(deviceID),
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

func writeAppError(w http.ResponseWriter, r *http.Request, err *appError) {
	writeError(w, r, err.HTTPStatus, err.Code, err.Message)
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

func randomUUID() string {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Sprintf("00000000-0000-4000-8000-%012d", time.Now().UnixNano()%1_000_000_000_000)
	}
	buffer[6] = (buffer[6] & 0x0f) | 0x40
	buffer[8] = (buffer[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", buffer[0:4], buffer[4:6], buffer[6:8], buffer[8:10], buffer[10:16])
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
