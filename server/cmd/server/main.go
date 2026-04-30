package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
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

type activationCodeReplay struct {
	Signature string
	Code      string
	ExpiresAt time.Time
}

type approvalDecisionReplay struct {
	Signature string
	Item      approval
}

type clientInstanceReplay struct {
	Signature string
	Item      clientInstance
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
	IdempotencyKey  string            `json:"-"`
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

type clientInstance struct {
	ClientInstanceID string `json:"client_instance_id"`
	TenantID         string `json:"tenant_id"`
	UserID           string `json:"user_id"`
	ClientType       string `json:"client_type"`
	DeviceID         string `json:"device_id,omitempty"`
	DisplayName      string `json:"display_name"`
	AppVersion       string `json:"app_version"`
	Platform         string `json:"platform"`
	PushProvider     string `json:"push_provider,omitempty"`
	Status           string `json:"status"`
	LastSeenAt       string `json:"last_seen_at"`
	CreatedAt        string `json:"created_at"`
}

type deviceGrant struct {
	GrantID    string `json:"grant_id"`
	TenantID   string `json:"tenant_id"`
	DeviceID   string `json:"device_id"`
	UserID     string `json:"user_id"`
	Permission string `json:"permission"`
	GrantedBy  string `json:"granted_by_user_id"`
	CreatedAt  string `json:"created_at"`
	RevokedAt  string `json:"revoked_at,omitempty"`
}

type auditLog struct {
	AuditID      int64          `json:"audit_id"`
	TenantID     string         `json:"tenant_id"`
	ActorType    string         `json:"actor_type"`
	ActorID      string         `json:"actor_id"`
	Action       string         `json:"action"`
	ResourceType string         `json:"resource_type"`
	ResourceID   string         `json:"resource_id"`
	Result       string         `json:"result"`
	TraceID      string         `json:"trace_id"`
	Detail       map[string]any `json:"detail"`
	CreatedAt    string         `json:"created_at"`
}

type outputChunk struct {
	ChunkID         int64  `json:"chunk_id"`
	TenantID        string `json:"tenant_id"`
	SessionID       string `json:"session_id"`
	SequenceNo      int64  `json:"sequence_no"`
	StreamType      string `json:"stream_type"`
	ContentRedacted string `json:"content_redacted"`
	ContentHash     string `json:"content_hash"`
	CreatedAt       string `json:"created_at"`
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
	DeviceID       string   `json:"device_id"`
	SessionID      string   `json:"session_id"`
	CLIType        string   `json:"cli_type"`
	EventType      string   `json:"event_type"`
	RiskLevel      string   `json:"risk_level"`
	PromptText     string   `json:"prompt_text"`
	ContextBefore  string   `json:"context_before"`
	IdempotencyKey string   `json:"idempotency_key"`
	Suggested      []string `json:"suggested_actions"`
	ExpiresIn      int      `json:"expires_in_seconds"`
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

type registerClientInstanceRequest struct {
	TenantID    string `json:"tenant_id"`
	ClientType  string `json:"client_type"`
	DeviceID    string `json:"device_id"`
	DisplayName string `json:"display_name"`
	AppVersion  string `json:"app_version"`
	Platform    string `json:"platform"`
}

type createDeviceGrantRequest struct {
	UserID     string `json:"user_id"`
	Permission string `json:"permission"`
}

type registerPushTokenRequest struct {
	Provider string `json:"provider"`
	Token    string `json:"token"`
}

type createOutputChunkRequest struct {
	DeviceID        string `json:"device_id"`
	SessionID       string `json:"session_id"`
	SequenceNo      int64  `json:"sequence_no"`
	StreamType      string `json:"stream_type"`
	ContentRedacted string `json:"content_redacted"`
	ContentHash     string `json:"content_hash"`
}

type appError struct {
	HTTPStatus int
	Code       string
	Message    string
}

type storeHealthChecker interface {
	ReadyCheck() error
}

type gatePilotStore interface {
	RegisterClientInstance(req registerClientInstanceRequest, userID string, idempotencyKey string, now time.Time) (clientInstance, *appError)
	CreateActivationCode(tenantID string, req createActivationCodeRequest, idempotencyKey string, now time.Time) (string, time.Time, *appError)
	ListDevices(tenantID string) []device
	RegisterAgent(req registerAgentRequest, now time.Time) (device, string, *appError)
	CreateSession(req createAgentSessionRequest, now time.Time) (session, *appError)
	CreateApproval(req createAgentApprovalRequest, now time.Time) (approval, *appError)
	GetApproval(approvalID string) (approval, *appError)
	ListApprovals(tenantID string, status string) []approval
	SubmitApprovalDecision(approvalID string, req submitApprovalDecisionRequest, idempotencyKey string, decidedBy map[string]string, now time.Time) (approval, *appError)
	AckApprovalDecision(req ackApprovalDecisionRequest) (map[string]any, *appError)
	CreateDeviceGrant(deviceID string, req createDeviceGrantRequest, grantedBy string, now time.Time) (deviceGrant, *appError)
	ListDeviceGrants(deviceID string) []deviceGrant
	RevokeDeviceGrant(deviceID string, grantID string, revokedBy string, now time.Time) (deviceGrant, *appError)
	CanApproveDevice(tenantID string, deviceID string, userID string) bool
	RegisterPushToken(clientInstanceID string, req registerPushTokenRequest, now time.Time) (clientInstance, *appError)
	ExpireApprovals(now time.Time) []approval
	MarkStaleDevicesOffline(now time.Time, offlineAfter time.Duration) []device
	AppendAuditLog(item auditLog, now time.Time)
	ListAuditLogs(tenantID string) []auditLog
	GetSession(sessionID string) (session, *appError)
	ListDeviceSessions(deviceID string) []session
	AppendOutputChunk(req createOutputChunkRequest, now time.Time) (outputChunk, *appError)
	ListOutputChunks(sessionID string) []outputChunk
	MarkDeviceSeen(deviceID string, now time.Time) *appError
	MarkClientInstanceSeen(clientInstanceID string, now time.Time) *appError
	ValidateDeviceToken(deviceID string, token string) *appError
	ValidateApprovalDeviceToken(approvalID string, token string) *appError
	ListPendingDeliveries(deviceID string) []approval
}

type memoryStore struct {
	mu                       sync.Mutex
	activationCodes          map[string]activationCode
	activationCodeReplayByID map[string]activationCodeReplay
	clientInstanceReplay     map[string]clientInstanceReplay
	approvalDecisionReplay   map[string]approvalDecisionReplay
	deviceTokenHashes        map[string]string
	clientInstances          map[string]clientInstance
	approvalNotifications    map[string][]string
	deviceGrants             map[string]deviceGrant
	auditLogs                []auditLog
	outputChunks             map[string]map[int64]outputChunk
	nextOutputChunkID        int64
	devices                  map[string]device
	sessions                 map[string]session
	approvals                map[string]approval
}

var store gatePilotStore = newMemoryStore()

func newMemoryStore() *memoryStore {
	return &memoryStore{
		activationCodes:          map[string]activationCode{},
		activationCodeReplayByID: map[string]activationCodeReplay{},
		clientInstanceReplay:     map[string]clientInstanceReplay{},
		approvalDecisionReplay:   map[string]approvalDecisionReplay{},
		deviceTokenHashes:        map[string]string{},
		clientInstances:          map[string]clientInstance{},
		approvalNotifications:    map[string][]string{},
		deviceGrants:             map[string]deviceGrant{},
		auditLogs:                []auditLog{},
		outputChunks:             map[string]map[int64]outputChunk{},
		nextOutputChunkID:        1,
		devices:                  map[string]device{},
		sessions:                 map[string]session{},
		approvals:                map[string]approval{},
	}
}

func (s *memoryStore) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.activationCodes = map[string]activationCode{}
	s.activationCodeReplayByID = map[string]activationCodeReplay{}
	s.clientInstanceReplay = map[string]clientInstanceReplay{}
	s.approvalDecisionReplay = map[string]approvalDecisionReplay{}
	s.deviceTokenHashes = map[string]string{}
	s.clientInstances = map[string]clientInstance{}
	s.approvalNotifications = map[string][]string{}
	s.deviceGrants = map[string]deviceGrant{}
	s.auditLogs = []auditLog{}
	s.outputChunks = map[string]map[int64]outputChunk{}
	s.nextOutputChunkID = 1
	s.devices = map[string]device{}
	s.sessions = map[string]session{}
	s.approvals = map[string]approval{}
}

func (s *memoryStore) ReadyCheck() error {
	return nil
}

func (s *memoryStore) RegisterClientInstance(req registerClientInstanceRequest, userID string, idempotencyKey string, now time.Time) (clientInstance, *appError) {
	if req.TenantID == "" {
		return clientInstance{}, &appError{HTTPStatus: http.StatusBadRequest, Code: "message_schema_invalid", Message: "tenant_id is required"}
	}
	if req.ClientType == "" {
		return clientInstance{}, &appError{HTTPStatus: http.StatusBadRequest, Code: "message_schema_invalid", Message: "client_type is required"}
	}
	if req.DisplayName == "" {
		req.DisplayName = req.ClientType
	}
	if req.AppVersion == "" {
		req.AppVersion = version
	}
	if req.Platform == "" {
		req.Platform = "browser"
	}
	if idempotencyKey == "" {
		return clientInstance{}, &appError{HTTPStatus: http.StatusBadRequest, Code: "idempotency_key_required", Message: "Idempotency-Key header is required"}
	}

	replayKey := req.TenantID + ":" + userID + ":" + idempotencyKey
	signature := fmt.Sprintf("%s:%s:%s:%s:%s:%s", req.TenantID, req.ClientType, req.DeviceID, req.DisplayName, req.AppVersion, req.Platform)

	s.mu.Lock()
	defer s.mu.Unlock()
	if replay, ok := s.clientInstanceReplay[replayKey]; ok {
		if replay.Signature != signature {
			return clientInstance{}, &appError{HTTPStatus: http.StatusConflict, Code: "approval_decision_conflict", Message: "idempotency key reused with different parameters"}
		}
		return replay.Item, nil
	}

	nowString := now.Format(time.RFC3339)
	item := clientInstance{
		ClientInstanceID: randomUUID(),
		TenantID:         req.TenantID,
		UserID:           userID,
		ClientType:       req.ClientType,
		DeviceID:         req.DeviceID,
		DisplayName:      req.DisplayName,
		AppVersion:       req.AppVersion,
		Platform:         req.Platform,
		Status:           "active",
		LastSeenAt:       nowString,
		CreatedAt:        nowString,
	}
	s.clientInstances[item.ClientInstanceID] = item
	s.clientInstanceReplay[replayKey] = clientInstanceReplay{Signature: signature, Item: item}
	return item, nil
}

func (s *memoryStore) RegisterPushToken(clientInstanceID string, req registerPushTokenRequest, now time.Time) (clientInstance, *appError) {
	if req.Provider == "" || req.Token == "" {
		return clientInstance{}, &appError{HTTPStatus: http.StatusBadRequest, Code: "message_schema_invalid", Message: "provider and token are required"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.clientInstances[clientInstanceID]
	if !ok {
		return clientInstance{}, &appError{HTTPStatus: http.StatusNotFound, Code: "client_instance_not_found", Message: "client instance not found"}
	}
	item.PushProvider = req.Provider
	item.LastSeenAt = now.Format(time.RFC3339)
	s.clientInstances[clientInstanceID] = item
	return item, nil
}

func (s *memoryStore) CreateActivationCode(tenantID string, req createActivationCodeRequest, idempotencyKey string, now time.Time) (string, time.Time, *appError) {
	if req.Name == "" {
		req.Name = "New Device"
	}
	if req.ExpiresInSeconds <= 0 {
		req.ExpiresInSeconds = 600
	}

	if idempotencyKey == "" {
		return "", time.Time{}, &appError{HTTPStatus: http.StatusBadRequest, Code: "idempotency_key_required", Message: "Idempotency-Key header is required"}
	}

	replayKey := tenantID + ":" + idempotencyKey
	signature := fmt.Sprintf("%s:%d", req.Name, req.ExpiresInSeconds)

	s.mu.Lock()
	defer s.mu.Unlock()
	if replay, ok := s.activationCodeReplayByID[replayKey]; ok {
		if replay.Signature != signature {
			return "", time.Time{}, &appError{HTTPStatus: http.StatusConflict, Code: "approval_decision_conflict", Message: "idempotency key reused with different parameters"}
		}
		return replay.Code, replay.ExpiresAt, nil
	}

	code := "GP-" + randomHex(3) + "-" + randomHex(3)
	expiresAt := now.Add(time.Duration(req.ExpiresInSeconds) * time.Second)

	s.activationCodes[code] = activationCode{
		TenantID:  tenantID,
		Name:      req.Name,
		Code:      code,
		ExpiresAt: expiresAt,
	}
	s.activationCodeReplayByID[replayKey] = activationCodeReplay{
		Signature: signature,
		Code:      code,
		ExpiresAt: expiresAt,
	}
	return code, expiresAt, nil
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
	s.deviceTokenHashes[deviceID] = sha256Hex(deviceToken)
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
	if req.IdempotencyKey != "" {
		for _, existing := range s.approvals {
			if existing.TenantID == sessionItem.TenantID && existing.IdempotencyKey == req.IdempotencyKey {
				return existing, nil
			}
		}
	}

	approvalID := randomUUID()
	item := approval{
		ApprovalID:     approvalID,
		IdempotencyKey: req.IdempotencyKey,
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
	for _, client := range s.clientInstances {
		if client.TenantID == sessionItem.TenantID && client.Status == "active" {
			s.approvalNotifications[approvalID] = append(s.approvalNotifications[approvalID], client.ClientInstanceID)
		}
	}
	sessionItem.Status = "waiting_approval"
	sessionItem.PendingApprovals++
	s.sessions[req.SessionID] = sessionItem
	return item, nil
}

func (s *memoryStore) GetApproval(approvalID string) (approval, *appError) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.approvals[approvalID]
	if !ok {
		return approval{}, &appError{HTTPStatus: http.StatusNotFound, Code: "approval_not_found", Message: "approval not found"}
	}
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

func (s *memoryStore) SubmitApprovalDecision(approvalID string, req submitApprovalDecisionRequest, idempotencyKey string, decidedBy map[string]string, now time.Time) (approval, *appError) {
	if req.DecisionType == "" {
		req.DecisionType = "approve"
	}
	if idempotencyKey == "" {
		return approval{}, &appError{HTTPStatus: http.StatusBadRequest, Code: "idempotency_key_required", Message: "Idempotency-Key header is required"}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	replayKey := approvalID + ":" + idempotencyKey
	signature := req.DecisionType + ":" + req.Payload
	if replay, ok := s.approvalDecisionReplay[replayKey]; ok {
		if replay.Signature != signature {
			return approval{}, &appError{HTTPStatus: http.StatusConflict, Code: "approval_decision_conflict", Message: "Idempotency-Key reused with different decision parameters"}
		}
		return replay.Item, nil
	}

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
	s.approvalDecisionReplay[replayKey] = approvalDecisionReplay{
		Signature: signature,
		Item:      item,
	}

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

func (s *memoryStore) CreateDeviceGrant(deviceID string, req createDeviceGrantRequest, grantedBy string, now time.Time) (deviceGrant, *appError) {
	if req.UserID == "" {
		return deviceGrant{}, &appError{HTTPStatus: http.StatusBadRequest, Code: "message_schema_invalid", Message: "user_id is required"}
	}
	if req.Permission == "" {
		req.Permission = "view"
	}
	switch req.Permission {
	case "view", "approve", "admin":
	default:
		return deviceGrant{}, &appError{HTTPStatus: http.StatusBadRequest, Code: "message_schema_invalid", Message: "permission must be view, approve, or admin"}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	deviceItem, ok := s.devices[deviceID]
	if !ok {
		return deviceGrant{}, &appError{HTTPStatus: http.StatusNotFound, Code: "device_offline", Message: "device not found"}
	}
	key := deviceID + ":" + req.UserID + ":" + req.Permission
	if existing, ok := s.deviceGrants[key]; ok && existing.RevokedAt == "" {
		return existing, nil
	}
	item := deviceGrant{
		GrantID:    randomUUID(),
		TenantID:   deviceItem.TenantID,
		DeviceID:   deviceID,
		UserID:     req.UserID,
		Permission: req.Permission,
		GrantedBy:  grantedBy,
		CreatedAt:  now.Format(time.RFC3339),
	}
	s.deviceGrants[key] = item
	return item, nil
}

func (s *memoryStore) ListDeviceGrants(deviceID string) []deviceGrant {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := []deviceGrant{}
	for _, grant := range s.deviceGrants {
		if grant.DeviceID == deviceID && grant.RevokedAt == "" {
			items = append(items, grant)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].CreatedAt > items[j].CreatedAt
	})
	return items
}

func (s *memoryStore) RevokeDeviceGrant(deviceID string, grantID string, revokedBy string, now time.Time) (deviceGrant, *appError) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.devices[deviceID]; !ok {
		return deviceGrant{}, &appError{HTTPStatus: http.StatusNotFound, Code: "device_offline", Message: "device not found"}
	}
	for key, grant := range s.deviceGrants {
		if grant.DeviceID == deviceID && grant.GrantID == grantID {
			if grant.RevokedAt == "" {
				grant.RevokedAt = now.Format(time.RFC3339)
				s.deviceGrants[key] = grant
			}
			return grant, nil
		}
	}
	return deviceGrant{}, &appError{HTTPStatus: http.StatusNotFound, Code: "device_grant_not_found", Message: "device grant not found"}
}

func (s *memoryStore) CanApproveDevice(tenantID string, deviceID string, userID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, grant := range s.deviceGrants {
		if grant.TenantID == tenantID && grant.DeviceID == deviceID && grant.UserID == userID && grant.RevokedAt == "" {
			return grant.Permission == "approve" || grant.Permission == "admin"
		}
	}
	return false
}

func (s *memoryStore) ExpireApprovals(now time.Time) []approval {
	s.mu.Lock()
	defer s.mu.Unlock()
	expired := []approval{}
	for _, item := range s.approvals {
		if item.Status != "waiting_decision" {
			continue
		}
		expiresAt, err := time.Parse(time.RFC3339, item.ExpiresAt)
		if err != nil || now.Before(expiresAt) {
			continue
		}
		item.Status = "delivering"
		item.DecisionType = "reject"
		item.DecisionPayload = "timeout"
		item.DeliveryID = randomUUID()
		item.DeliveryStatus = "sent"
		item.DecidedAt = now.Format(time.RFC3339)
		item.DecidedBy = map[string]string{
			"actor_type":   "system",
			"actor_id":     "timeout-worker",
			"display_name": "Timeout Worker",
			"client_type":  "worker",
		}
		s.approvals[item.ApprovalID] = item
		if sessionItem, ok := s.sessions[item.SessionID]; ok {
			sessionItem.Status = "waiting_approval"
			sessionItem.LastOutputSummary = "approval timeout reject delivering"
			s.sessions[item.SessionID] = sessionItem
		}
		s.auditLogs = append(s.auditLogs, auditLog{
			AuditID:      int64(len(s.auditLogs) + 1),
			TenantID:     item.TenantID,
			ActorType:    "system",
			ActorID:      "timeout-worker",
			Action:       "approval.timeout_reject",
			ResourceType: "approval",
			ResourceID:   item.ApprovalID,
			Result:       "success",
			TraceID:      "tr_worker",
			Detail:       map[string]any{"delivery_id": item.DeliveryID},
			CreatedAt:    now.Format(time.RFC3339),
		})
		expired = append(expired, item)
	}
	return expired
}

func (s *memoryStore) MarkStaleDevicesOffline(now time.Time, offlineAfter time.Duration) []device {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := []device{}
	for _, item := range s.devices {
		if item.Status != "active" {
			continue
		}
		lastSeen, err := time.Parse(time.RFC3339, item.LastSeen)
		if err != nil || now.Sub(lastSeen) < offlineAfter {
			continue
		}
		item.Status = "offline"
		s.devices[item.DeviceID] = item
		changed = append(changed, item)
	}
	return changed
}

func (s *memoryStore) AppendAuditLog(item auditLog, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item.AuditID = int64(len(s.auditLogs) + 1)
	if item.CreatedAt == "" {
		item.CreatedAt = now.Format(time.RFC3339)
	}
	if item.Detail == nil {
		item.Detail = map[string]any{}
	}
	s.auditLogs = append(s.auditLogs, item)
}

func (s *memoryStore) ListAuditLogs(tenantID string) []auditLog {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := []auditLog{}
	for i := len(s.auditLogs) - 1; i >= 0; i-- {
		if s.auditLogs[i].TenantID == tenantID {
			items = append(items, s.auditLogs[i])
		}
	}
	return items
}

func (s *memoryStore) GetSession(sessionID string) (session, *appError) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.sessions[sessionID]
	if !ok {
		return session{}, &appError{HTTPStatus: http.StatusNotFound, Code: "agent_session_not_found", Message: "session not found"}
	}
	return item, nil
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

func (s *memoryStore) AppendOutputChunk(req createOutputChunkRequest, now time.Time) (outputChunk, *appError) {
	if req.SessionID == "" || req.DeviceID == "" || req.SequenceNo <= 0 {
		return outputChunk{}, &appError{HTTPStatus: http.StatusBadRequest, Code: "message_schema_invalid", Message: "device_id, session_id, and positive sequence_no are required"}
	}
	if req.StreamType == "" {
		req.StreamType = "stdout"
	}
	switch req.StreamType {
	case "stdout", "stderr", "system":
	default:
		return outputChunk{}, &appError{HTTPStatus: http.StatusBadRequest, Code: "message_schema_invalid", Message: "stream_type must be stdout, stderr, or system"}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	sessionItem, ok := s.sessions[req.SessionID]
	if !ok || sessionItem.DeviceID != req.DeviceID {
		return outputChunk{}, &appError{HTTPStatus: http.StatusNotFound, Code: "agent_session_not_found", Message: "session not found"}
	}
	if s.outputChunks[req.SessionID] == nil {
		s.outputChunks[req.SessionID] = map[int64]outputChunk{}
	}
	if existing, ok := s.outputChunks[req.SessionID][req.SequenceNo]; ok {
		if existing.StreamType != req.StreamType || existing.ContentRedacted != req.ContentRedacted || existing.ContentHash != req.ContentHash {
			return outputChunk{}, &appError{HTTPStatus: http.StatusConflict, Code: "output_chunk_conflict", Message: "sequence_no already exists with different content"}
		}
		return existing, nil
	}
	item := outputChunk{
		ChunkID:         s.nextOutputChunkID,
		TenantID:        sessionItem.TenantID,
		SessionID:       req.SessionID,
		SequenceNo:      req.SequenceNo,
		StreamType:      req.StreamType,
		ContentRedacted: req.ContentRedacted,
		ContentHash:     req.ContentHash,
		CreatedAt:       now.Format(time.RFC3339),
	}
	s.nextOutputChunkID++
	s.outputChunks[req.SessionID][req.SequenceNo] = item
	sessionItem.LastOutputSummary = summarizeOutput(req.ContentRedacted)
	s.sessions[req.SessionID] = sessionItem
	return item, nil
}

func (s *memoryStore) ListOutputChunks(sessionID string) []outputChunk {
	s.mu.Lock()
	defer s.mu.Unlock()
	itemsBySequence := s.outputChunks[sessionID]
	items := make([]outputChunk, 0, len(itemsBySequence))
	for _, item := range itemsBySequence {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].SequenceNo < items[j].SequenceNo
	})
	return items
}

func (s *memoryStore) MarkDeviceSeen(deviceID string, now time.Time) *appError {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.devices[deviceID]
	if !ok {
		return &appError{HTTPStatus: http.StatusNotFound, Code: "device_offline", Message: "device not found"}
	}
	item.Status = "active"
	item.LastSeen = now.Format(time.RFC3339)
	s.devices[deviceID] = item
	return nil
}

func (s *memoryStore) MarkClientInstanceSeen(clientInstanceID string, now time.Time) *appError {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.clientInstances[clientInstanceID]
	if !ok {
		return &appError{HTTPStatus: http.StatusNotFound, Code: "client_instance_not_found", Message: "client instance not found"}
	}
	item.Status = "active"
	item.LastSeenAt = now.Format(time.RFC3339)
	s.clientInstances[clientInstanceID] = item
	return nil
}

func (s *memoryStore) ValidateDeviceToken(deviceID string, token string) *appError {
	s.mu.Lock()
	defer s.mu.Unlock()
	if token == "" {
		return &appError{HTTPStatus: http.StatusUnauthorized, Code: "device_token_invalid", Message: "device token is invalid"}
	}
	tokenHash, ok := s.deviceTokenHashes[deviceID]
	if !ok || tokenHash != sha256Hex(token) {
		return &appError{HTTPStatus: http.StatusUnauthorized, Code: "device_token_invalid", Message: "device token is invalid"}
	}
	return nil
}

func (s *memoryStore) ValidateApprovalDeviceToken(approvalID string, token string) *appError {
	s.mu.Lock()
	item, ok := s.approvals[approvalID]
	s.mu.Unlock()
	if !ok {
		return &appError{HTTPStatus: http.StatusNotFound, Code: "approval_not_found", Message: "approval not found"}
	}
	return s.ValidateDeviceToken(item.DeviceID, token)
}

func (s *memoryStore) ListPendingDeliveries(deviceID string) []approval {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := []approval{}
	for _, item := range s.approvals {
		if item.DeviceID == deviceID && item.Status == "delivering" && item.DeliveryStatus == "sent" {
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
	if err := configureStore(); err != nil {
		log.Fatal(err)
	}
	startWorkersFromEnv()

	// M0 阶段先暴露健康检查和当前用户接口，后续模块按 docs/03-detailed-design.md 拆入 domain service。
	log.Printf("gatepilot server listening on %s", addr)
	if err := http.ListenAndServe(addr, newRouter()); err != nil {
		log.Fatal(err)
	}
}

func configureStore() error {
	if getenv("GATEPILOT_STORE", "memory") != "postgres" {
		return nil
	}
	databaseURL := firstNonEmpty(os.Getenv("GATEPILOT_DATABASE_URL"), os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		return fmt.Errorf("GATEPILOT_STORE=postgres requires GATEPILOT_DATABASE_URL or DATABASE_URL")
	}
	postgresStore, err := newPostgresStore(databaseURL)
	if err != nil {
		return err
	}
	store = postgresStore
	return nil
}

func newRouter() http.Handler {
	// 路由集中在这里，测试可以直接复用同一套 HTTP 行为，避免脚本和单测走出不同契约。
	mux := http.NewServeMux()
	mux.HandleFunc("/health/live", liveHealthHandler)
	mux.HandleFunc("/health/ready", readyHealthHandler)
	mux.HandleFunc("/metrics", metricsHandler)
	mux.HandleFunc("/api/v1/healthz", healthHandler)
	mux.HandleFunc("/api/v1/me", meHandler)
	mux.HandleFunc("/api/v1/client-instances", clientInstancesHandler)
	mux.HandleFunc("/api/v1/client-instances/", clientInstanceScopedHandler)
	mux.HandleFunc("/api/v1/agent/register", agentRegisterHandler)
	mux.HandleFunc("/api/v1/agent/sessions", agentSessionsHandler)
	mux.HandleFunc("/api/v1/agent/approvals", agentApprovalsHandler)
	mux.HandleFunc("/api/v1/agent/approval-acks", agentApprovalAcksHandler)
	mux.HandleFunc("/api/v1/agent/output-chunks", agentOutputChunksHandler)
	mux.HandleFunc("/api/v1/approvals/", approvalScopedHandler)
	mux.HandleFunc("/api/v1/devices/", deviceScopedHandler)
	mux.HandleFunc("/api/v1/sessions/", sessionScopedHandler)
	mux.HandleFunc("/api/v1/tenants/", tenantScopedHandler)
	mux.HandleFunc("/ws/agent", agentWebSocketHandler)
	mux.HandleFunc("/ws/client", clientWebSocketHandler)

	return requestLog(cors(mux))
}

func sessionScopedHandler(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/sessions/"), "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	sessionID := parts[0]
	switch {
	case len(parts) == 1 && r.Method == http.MethodGet:
		item, appErr := store.GetSession(sessionID)
		if appErr != nil {
			writeAppError(w, r, appErr)
			return
		}
		writeJSON(w, envelope{
			Data:      item,
			RequestID: requestID(r),
			TraceID:   traceID(r),
		})
	case len(parts) == 2 && parts[1] == "output-chunks" && r.Method == http.MethodGet:
		listOutputChunksHandler(w, r, sessionID)
	default:
		http.NotFound(w, r)
		return
	}
}

func startWorkersFromEnv() {
	value := os.Getenv("GATEPILOT_WORKER_INTERVAL_SECONDS")
	if value == "" || value == "0" {
		return
	}
	seconds, err := strconv.Atoi(value)
	if err != nil || seconds <= 0 {
		log.Printf("invalid GATEPILOT_WORKER_INTERVAL_SECONDS=%q", value)
		return
	}
	go func() {
		ticker := time.NewTicker(time.Duration(seconds) * time.Second)
		defer ticker.Stop()
		for {
			runExpiryWorkerOnce()
			<-ticker.C
		}
	}()
}

func runExpiryWorkerOnce() {
	for _, item := range store.ExpireApprovals(time.Now().UTC()) {
		pushApprovalDecisionToAgent(item)
		pushApprovalUpdatedToClients(item)
		if sessionItem, appErr := store.GetSession(item.SessionID); appErr == nil {
			pushSessionUpdatedToClients(sessionItem)
		}
	}
	offlineAfter := 3 * time.Minute
	if value := os.Getenv("GATEPILOT_DEVICE_OFFLINE_SECONDS"); value != "" {
		if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
			offlineAfter = time.Duration(seconds) * time.Second
		}
	}
	for _, item := range store.MarkStaleDevicesOffline(time.Now().UTC(), offlineAfter) {
		clientHub.broadcast(item.TenantID, newWSEnvelope("device.status_changed", "tr_worker", map[string]any{
			"tenant_id":    item.TenantID,
			"device_id":    item.DeviceID,
			"status":       item.Status,
			"last_seen_at": item.LastSeen,
		}))
	}
}

func clientInstanceScopedHandler(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/client-instances/"), "/")
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	clientInstanceID := parts[0]
	resource := parts[1]
	switch {
	case resource == "push-token" && r.Method == http.MethodPost:
		registerPushTokenHandler(w, r, clientInstanceID)
	default:
		http.NotFound(w, r)
	}
}

func registerPushTokenHandler(w http.ResponseWriter, r *http.Request, clientInstanceID string) {
	var req registerPushTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, "message_schema_invalid", err.Error())
		return
	}
	item, appErr := store.RegisterPushToken(clientInstanceID, req, time.Now().UTC())
	if appErr != nil {
		writeAppError(w, r, appErr)
		return
	}
	writeJSON(w, envelope{
		Data: map[string]string{
			"client_instance_id": item.ClientInstanceID,
			"push_provider":      item.PushProvider,
			"status":             item.Status,
		},
		RequestID: requestID(r),
		TraceID:   traceID(r),
	})
}

func clientInstancesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req registerClientInstanceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, "message_schema_invalid", err.Error())
		return
	}
	item, appErr := store.RegisterClientInstance(req, devUserID(r), r.Header.Get("Idempotency-Key"), time.Now().UTC())
	if appErr != nil {
		writeAppError(w, r, appErr)
		return
	}
	writeStatusJSON(w, http.StatusCreated, envelope{
		Data: map[string]string{
			"client_instance_id": item.ClientInstanceID,
		},
		RequestID: requestID(r),
		TraceID:   traceID(r),
	})
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
	case resource == "grants" && len(parts) == 2 && r.Method == http.MethodGet:
		listDeviceGrantsHandler(w, r, deviceID)
	case resource == "grants" && r.Method == http.MethodPost:
		createDeviceGrantHandler(w, r, deviceID)
	case resource == "grants" && len(parts) == 3 && r.Method == http.MethodDelete:
		revokeDeviceGrantHandler(w, r, deviceID, parts[2])
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

func liveHealthHandler(w http.ResponseWriter, r *http.Request) {
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

func readyHealthHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if checker, ok := store.(storeHealthChecker); ok {
		if err := checker.ReadyCheck(); err != nil {
			writeError(w, r, http.StatusServiceUnavailable, "dependency_unavailable", err.Error())
			return
		}
	}
	writeJSON(w, envelope{
		Data: map[string]any{
			"status": "ok",
			"checks": map[string]string{
				"store": "ok",
			},
		},
		RequestID: requestID(r),
		TraceID:   traceID(r),
	})
}

func metricsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	_, _ = fmt.Fprintf(w, "gatepilot_build_info{version=%q} 1\n", version)
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
					"role":        devRole(r),
					"permissions": devPermissions(r),
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
	case resource == "audit-logs" && r.Method == http.MethodGet:
		listAuditLogsHandler(w, r, tenantID)
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
	code, expiresAt, appErr := store.CreateActivationCode(tenantID, req, r.Header.Get("Idempotency-Key"), time.Now().UTC())
	if appErr != nil {
		writeAppError(w, r, appErr)
		return
	}

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
	if appErr := store.ValidateDeviceToken(req.DeviceID, bearerToken(r)); appErr != nil {
		writeAppError(w, r, appErr)
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
	if appErr := store.ValidateDeviceToken(req.DeviceID, bearerToken(r)); appErr != nil {
		writeAppError(w, r, appErr)
		return
	}
	item, appErr := store.CreateApproval(req, time.Now().UTC())
	if appErr != nil {
		writeAppError(w, r, appErr)
		return
	}
	pushApprovalCreatedToClients(item)
	if sessionItem, appErr := store.GetSession(item.SessionID); appErr == nil {
		pushSessionUpdatedToClients(sessionItem)
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

func listAuditLogsHandler(w http.ResponseWriter, r *http.Request, tenantID string) {
	if devRole(r) != "owner" && devRole(r) != "admin" {
		writeError(w, r, http.StatusForbidden, "role_insufficient", "role cannot view audit logs")
		return
	}
	writeJSON(w, envelope{
		Data: map[string]any{
			"items":       store.ListAuditLogs(tenantID),
			"next_cursor": nil,
			"has_more":    false,
		},
		RequestID: requestID(r),
		TraceID:   traceID(r),
	})
}

func submitApprovalDecisionHandler(w http.ResponseWriter, r *http.Request, approvalID string) {
	item, appErr := store.GetApproval(approvalID)
	if appErr != nil {
		writeAppError(w, r, appErr)
		return
	}
	if appErr := authorizeApprovalDecision(r, item); appErr != nil {
		writeAppError(w, r, appErr)
		return
	}

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
	item, appErr = store.SubmitApprovalDecision(approvalID, req, r.Header.Get("Idempotency-Key"), decidedBy, time.Now().UTC())
	if appErr != nil {
		writeAppError(w, r, appErr)
		return
	}
	pushApprovalDecisionToAgent(item)
	pushApprovalUpdatedToClients(item)
	if sessionItem, appErr := store.GetSession(item.SessionID); appErr == nil {
		pushSessionUpdatedToClients(sessionItem)
	}
	store.AppendAuditLog(auditLog{
		TenantID:     item.TenantID,
		ActorType:    decidedBy["actor_type"],
		ActorID:      decidedBy["actor_id"],
		Action:       "approval.decision",
		ResourceType: "approval",
		ResourceID:   item.ApprovalID,
		Result:       "success",
		TraceID:      traceID(r),
		Detail: map[string]any{
			"decision_type": item.DecisionType,
			"delivery_id":   item.DeliveryID,
		},
	}, time.Now().UTC())

	writeJSON(w, envelope{
		Data:      item,
		RequestID: requestID(r),
		TraceID:   traceID(r),
	})
}

func devUserID(r *http.Request) string {
	return firstNonEmpty(r.Header.Get("X-Dev-User-Id"), "00000000-0000-0000-0000-000000000001")
}

func authorizeApprovalDecision(r *http.Request, item approval) *appError {
	switch devRole(r) {
	case "owner", "admin":
		return nil
	case "approver":
		if store.CanApproveDevice(item.TenantID, item.DeviceID, devUserID(r)) {
			return nil
		}
		return &appError{HTTPStatus: http.StatusForbidden, Code: "device_access_denied", Message: "approver does not have approve grant for this device"}
	default:
		return &appError{HTTPStatus: http.StatusForbidden, Code: "role_insufficient", Message: "role cannot submit approval decisions"}
	}
}

func createDeviceGrantHandler(w http.ResponseWriter, r *http.Request, deviceID string) {
	if devRole(r) != "owner" && devRole(r) != "admin" {
		writeError(w, r, http.StatusForbidden, "role_insufficient", "role cannot manage device grants")
		return
	}
	var req createDeviceGrantRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, "message_schema_invalid", err.Error())
		return
	}
	item, appErr := store.CreateDeviceGrant(deviceID, req, devUserID(r), time.Now().UTC())
	if appErr != nil {
		writeAppError(w, r, appErr)
		return
	}
	store.AppendAuditLog(auditLog{
		TenantID:     item.TenantID,
		ActorType:    "user",
		ActorID:      devUserID(r),
		Action:       "device_grant.create",
		ResourceType: "device",
		ResourceID:   deviceID,
		Result:       "success",
		TraceID:      traceID(r),
		Detail: map[string]any{
			"grant_id":   item.GrantID,
			"user_id":    item.UserID,
			"permission": item.Permission,
		},
	}, time.Now().UTC())
	writeStatusJSON(w, http.StatusCreated, envelope{
		Data:      item,
		RequestID: requestID(r),
		TraceID:   traceID(r),
	})
}

func listDeviceGrantsHandler(w http.ResponseWriter, r *http.Request, deviceID string) {
	if devRole(r) != "owner" && devRole(r) != "admin" {
		writeError(w, r, http.StatusForbidden, "role_insufficient", "role cannot view device grants")
		return
	}
	writeJSON(w, envelope{
		Data: map[string]any{
			"items":       store.ListDeviceGrants(deviceID),
			"next_cursor": nil,
			"has_more":    false,
		},
		RequestID: requestID(r),
		TraceID:   traceID(r),
	})
}

func revokeDeviceGrantHandler(w http.ResponseWriter, r *http.Request, deviceID string, grantID string) {
	if devRole(r) != "owner" && devRole(r) != "admin" {
		writeError(w, r, http.StatusForbidden, "role_insufficient", "role cannot manage device grants")
		return
	}
	item, appErr := store.RevokeDeviceGrant(deviceID, grantID, devUserID(r), time.Now().UTC())
	if appErr != nil {
		writeAppError(w, r, appErr)
		return
	}
	store.AppendAuditLog(auditLog{
		TenantID:     item.TenantID,
		ActorType:    "user",
		ActorID:      devUserID(r),
		Action:       "device_grant.revoke",
		ResourceType: "device",
		ResourceID:   deviceID,
		Result:       "success",
		TraceID:      traceID(r),
		Detail: map[string]any{
			"grant_id":   item.GrantID,
			"user_id":    item.UserID,
			"permission": item.Permission,
		},
	}, time.Now().UTC())
	writeJSON(w, envelope{
		Data:      item,
		RequestID: requestID(r),
		TraceID:   traceID(r),
	})
}

func devRole(r *http.Request) string {
	role := r.Header.Get("X-Dev-Role")
	switch role {
	case "owner", "admin", "approver", "viewer":
		return role
	default:
		return "owner"
	}
}

func devPermissions(r *http.Request) []string {
	switch devRole(r) {
	case "owner", "admin":
		return []string{"tenant:admin", "device:admin", "approval:approve"}
	case "approver":
		return []string{"approval:approve"}
	default:
		return []string{"approval:read"}
	}
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
	if appErr := store.ValidateApprovalDeviceToken(req.ApprovalID, bearerToken(r)); appErr != nil {
		writeAppError(w, r, appErr)
		return
	}
	data, appErr := store.AckApprovalDecision(req)
	if appErr != nil {
		writeAppError(w, r, appErr)
		return
	}
	if item, appErr := store.GetApproval(req.ApprovalID); appErr == nil {
		pushApprovalUpdatedToClients(item)
		if sessionItem, appErr := store.GetSession(item.SessionID); appErr == nil {
			pushSessionUpdatedToClients(sessionItem)
		}
		store.AppendAuditLog(auditLog{
			TenantID:     item.TenantID,
			ActorType:    "device",
			ActorID:      item.DeviceID,
			Action:       "approval.delivery_ack",
			ResourceType: "approval",
			ResourceID:   item.ApprovalID,
			Result:       req.AckResult,
			TraceID:      traceID(r),
			Detail: map[string]any{
				"delivery_id": req.DeliveryID,
				"session_id":  req.SessionID,
			},
		}, time.Now().UTC())
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

func agentOutputChunksHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req createOutputChunkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, "message_schema_invalid", err.Error())
		return
	}
	if appErr := store.ValidateDeviceToken(req.DeviceID, bearerToken(r)); appErr != nil {
		writeAppError(w, r, appErr)
		return
	}
	item, appErr := store.AppendOutputChunk(req, time.Now().UTC())
	if appErr != nil {
		writeAppError(w, r, appErr)
		return
	}
	if sessionItem, appErr := store.GetSession(req.SessionID); appErr == nil {
		pushSessionUpdatedToClients(sessionItem)
	}
	writeStatusJSON(w, http.StatusCreated, envelope{
		Data:      item,
		RequestID: requestID(r),
		TraceID:   traceID(r),
	})
}

func listOutputChunksHandler(w http.ResponseWriter, r *http.Request, sessionID string) {
	if _, appErr := store.GetSession(sessionID); appErr != nil {
		writeAppError(w, r, appErr)
		return
	}
	writeJSON(w, envelope{
		Data: map[string]any{
			"items":       store.ListOutputChunks(sessionID),
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
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key, X-Request-Id, X-Client-Instance-Id, X-Dev-Role, X-Dev-User-Id")
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

func bearerToken(r *http.Request) string {
	value := r.Header.Get("Authorization")
	if strings.HasPrefix(value, "Bearer ") {
		return strings.TrimPrefix(value, "Bearer ")
	}
	return ""
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

func summarizeOutput(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "output chunk received"
	}
	if len(value) <= 160 {
		return value
	}
	return value[:160]
}
