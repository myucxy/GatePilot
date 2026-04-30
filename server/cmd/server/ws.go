package main

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	schemaVersion           = "2026-04-01"
	agentHeartbeatIntervalS = 15
)

type wsEnvelope struct {
	Type          string          `json:"type"`
	MessageID     string          `json:"message_id"`
	TraceID       string          `json:"trace_id"`
	SentAt        string          `json:"sent_at"`
	SchemaVersion string          `json:"schema_version"`
	Payload       json.RawMessage `json:"payload"`
}

type agentHelloPayload struct {
	DeviceID        string         `json:"device_id"`
	AgentVersion    string         `json:"agent_version"`
	ProtocolVersion string         `json:"protocol_version"`
	Platform        string         `json:"platform"`
	Capabilities    map[string]any `json:"capabilities"`
}

type agentHeartbeatPayload struct {
	ActiveSessions  int     `json:"active_sessions"`
	LocalQueueDepth int     `json:"local_queue_depth"`
	LastError       *string `json:"last_error"`
}

type agentConnection struct {
	deviceID string
	conn     *websocket.Conn
	mu       sync.Mutex
}

type agentConnectionHub struct {
	mu       sync.Mutex
	byDevice map[string]*agentConnection
}

type clientConnection struct {
	clientInstanceID string
	tenantID         string
	conn             *websocket.Conn
	mu               sync.Mutex
}

type clientConnectionHub struct {
	mu       sync.Mutex
	byTenant map[string]map[string]*clientConnection
}

var agentWSUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

var agentHub = &agentConnectionHub{byDevice: map[string]*agentConnection{}}
var clientHub = &clientConnectionHub{byTenant: map[string]map[string]*clientConnection{}}

func agentWebSocketHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	conn, err := agentWSUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	deviceID := ""
	var registeredConn *agentConnection
	defer func() {
		if registeredConn != nil {
			agentHub.unregister(registeredConn)
		}
	}()

	for {
		var msg wsEnvelope
		if err := conn.ReadJSON(&msg); err != nil {
			return
		}

		switch msg.Type {
		case "agent.hello":
			var payload agentHelloPayload
			if err := json.Unmarshal(msg.Payload, &payload); err != nil || payload.DeviceID == "" || payload.ProtocolVersion != schemaVersion {
				writeWSError(conn, msg.TraceID, "message_schema_invalid", "invalid agent.hello payload")
				return
			}
			deviceID = payload.DeviceID
			if appErr := store.ValidateDeviceToken(deviceID, bearerToken(r)); appErr != nil {
				writeWSError(conn, msg.TraceID, appErr.Code, appErr.Message)
				return
			}
			if appErr := store.MarkDeviceSeen(deviceID, time.Now().UTC()); appErr != nil {
				writeWSError(conn, msg.TraceID, appErr.Code, appErr.Message)
				return
			}
			registeredConn = &agentConnection{deviceID: deviceID, conn: conn}
			agentHub.register(registeredConn)
			if err := registeredConn.writeJSON(newWSEnvelope("agent.connected", msg.TraceID, map[string]any{
				"server_time":                time.Now().UTC().Format(time.RFC3339),
				"accepted_protocol_version":  schemaVersion,
				"heartbeat_interval_seconds": agentHeartbeatIntervalS,
			})); err != nil {
				return
			}
			pushPendingApprovalDecisions(registeredConn)
		case "agent.heartbeat":
			if deviceID == "" {
				writeWSError(conn, msg.TraceID, "message_schema_invalid", "agent.hello is required before heartbeat")
				return
			}
			var payload agentHeartbeatPayload
			if err := json.Unmarshal(msg.Payload, &payload); err != nil || payload.ActiveSessions < 0 || payload.LocalQueueDepth < 0 {
				writeWSError(conn, msg.TraceID, "message_schema_invalid", "invalid agent.heartbeat payload")
				return
			}
			if appErr := store.MarkDeviceSeen(deviceID, time.Now().UTC()); appErr != nil {
				writeWSError(conn, msg.TraceID, appErr.Code, appErr.Message)
				return
			}
		case "approval.decision.ack":
			if deviceID == "" {
				writeWSError(conn, msg.TraceID, "message_schema_invalid", "agent.hello is required before ack")
				return
			}
			var payload ackApprovalDecisionRequest
			if err := json.Unmarshal(msg.Payload, &payload); err != nil {
				writeWSError(conn, msg.TraceID, "message_schema_invalid", "invalid approval.decision.ack payload")
				return
			}
			if appErr := store.ValidateApprovalDeviceToken(payload.ApprovalID, bearerToken(r)); appErr != nil {
				writeWSError(conn, msg.TraceID, appErr.Code, appErr.Message)
				return
			}
			if _, appErr := store.AckApprovalDecision(payload); appErr != nil {
				writeWSError(conn, msg.TraceID, appErr.Code, appErr.Message)
				return
			}
			if item, appErr := store.GetApproval(payload.ApprovalID); appErr == nil {
				pushApprovalUpdatedToClients(item)
			}
		default:
			writeWSError(conn, msg.TraceID, "message_type_unknown", "unknown websocket message type")
			return
		}
	}
}

func clientWebSocketHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	tenantID := r.URL.Query().Get("tenant_id")
	clientInstanceID := r.URL.Query().Get("client_instance_id")
	if tenantID == "" || clientInstanceID == "" {
		http.Error(w, "tenant_id and client_instance_id are required", http.StatusBadRequest)
		return
	}
	if appErr := store.MarkClientInstanceSeen(clientInstanceID, time.Now().UTC()); appErr != nil {
		writeError(w, r, appErr.HTTPStatus, appErr.Code, appErr.Message)
		return
	}

	conn, err := agentWSUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	client := &clientConnection{tenantID: tenantID, clientInstanceID: clientInstanceID, conn: conn}
	clientHub.register(client)
	defer clientHub.unregister(client)

	if err := client.writeJSON(newWSEnvelope("client.connected", traceID(r), map[string]any{
		"server_time":        time.Now().UTC().Format(time.RFC3339),
		"client_instance_id": clientInstanceID,
		"tenant_id":          tenantID,
	})); err != nil {
		return
	}

	for {
		var msg wsEnvelope
		if err := conn.ReadJSON(&msg); err != nil {
			return
		}
		switch msg.Type {
		case "client.heartbeat":
			if appErr := store.MarkClientInstanceSeen(clientInstanceID, time.Now().UTC()); appErr != nil {
				writeWSError(conn, msg.TraceID, appErr.Code, appErr.Message)
				return
			}
		default:
			writeWSError(conn, msg.TraceID, "message_type_unknown", "unknown websocket message type")
			return
		}
	}
}

func pushApprovalDecisionToAgent(item approval) bool {
	return agentHub.push(item.DeviceID, approvalDecisionDeliverEnvelope(item))
}

func pushApprovalCreatedToClients(item approval) {
	clientHub.broadcast(item.TenantID, approvalCreatedEnvelope(item))
}

func pushApprovalUpdatedToClients(item approval) {
	clientHub.broadcast(item.TenantID, approvalUpdatedEnvelope(item))
}

func pushSessionUpdatedToClients(item session) {
	clientHub.broadcast(item.TenantID, sessionUpdatedEnvelope(item))
}

func pushPendingApprovalDecisions(agent *agentConnection) {
	for _, item := range store.ListPendingDeliveries(agent.deviceID) {
		if err := agent.writeJSON(approvalDecisionDeliverEnvelope(item)); err != nil {
			agentHub.unregister(agent)
			return
		}
	}
}

func approvalDecisionDeliverEnvelope(item approval) map[string]any {
	return newWSEnvelope("approval.decision.deliver", "tr_local", map[string]any{
		"delivery_id":   item.DeliveryID,
		"approval_id":   item.ApprovalID,
		"session_id":    item.SessionID,
		"decision_type": item.DecisionType,
		"payload":       nullableString(item.DecisionPayload),
		"expires_at":    item.ExpiresAt,
	})
}

func approvalCreatedEnvelope(item approval) map[string]any {
	return newWSEnvelope("approval.created", "tr_local", map[string]any{
		"tenant_id":   item.TenantID,
		"approval_id": item.ApprovalID,
		"risk_level":  item.RiskLevel,
		"event_type":  item.EventType,
		"expires_at":  item.ExpiresAt,
	})
}

func approvalUpdatedEnvelope(item approval) map[string]any {
	return newWSEnvelope("approval.updated", "tr_local", map[string]any{
		"tenant_id":        item.TenantID,
		"approval_id":      item.ApprovalID,
		"status":           item.Status,
		"decision_type":    nullableString(item.DecisionType),
		"decided_at":       nullableString(item.DecidedAt),
		"decided_by":       nullableMap(item.DecidedBy),
		"decision_payload": nullableString(item.DecisionPayload),
		"delivery_status":  nullableString(item.DeliveryStatus),
	})
}

func sessionUpdatedEnvelope(item session) map[string]any {
	return newWSEnvelope("session.updated", "tr_local", map[string]any{
		"tenant_id":              item.TenantID,
		"session_id":             item.SessionID,
		"device_id":              item.DeviceID,
		"status":                 item.Status,
		"ended_at":               nullableString(item.EndedAt),
		"exit_code":              nullableInt(item.ExitCode),
		"pending_approval_count": item.PendingApprovals,
		"last_output_summary":    item.LastOutputSummary,
	})
}

func (h *agentConnectionHub) register(agent *agentConnection) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.byDevice[agent.deviceID] = agent
}

func (h *agentConnectionHub) unregister(agent *agentConnection) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.byDevice[agent.deviceID] == agent {
		delete(h.byDevice, agent.deviceID)
	}
}

func (h *agentConnectionHub) push(deviceID string, envelope map[string]any) bool {
	h.mu.Lock()
	agent := h.byDevice[deviceID]
	h.mu.Unlock()
	if agent == nil {
		return false
	}
	if err := agent.writeJSON(envelope); err != nil {
		h.unregister(agent)
		return false
	}
	return true
}

func (h *clientConnectionHub) register(client *clientConnection) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.byTenant[client.tenantID] == nil {
		h.byTenant[client.tenantID] = map[string]*clientConnection{}
	}
	h.byTenant[client.tenantID][client.clientInstanceID] = client
}

func (h *clientConnectionHub) unregister(client *clientConnection) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.byTenant[client.tenantID] != nil && h.byTenant[client.tenantID][client.clientInstanceID] == client {
		delete(h.byTenant[client.tenantID], client.clientInstanceID)
	}
	if len(h.byTenant[client.tenantID]) == 0 {
		delete(h.byTenant, client.tenantID)
	}
}

func (h *clientConnectionHub) broadcast(tenantID string, envelope map[string]any) {
	h.mu.Lock()
	clients := make([]*clientConnection, 0, len(h.byTenant[tenantID]))
	for _, client := range h.byTenant[tenantID] {
		clients = append(clients, client)
	}
	h.mu.Unlock()

	for _, client := range clients {
		if err := client.writeJSON(envelope); err != nil {
			h.unregister(client)
		}
	}
}

func (a *agentConnection) writeJSON(value any) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.conn.WriteJSON(value)
}

func (c *clientConnection) writeJSON(value any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.WriteJSON(value)
}

func newWSEnvelope(messageType string, traceID string, payload any) map[string]any {
	if traceID == "" {
		traceID = "tr_local"
	}
	return map[string]any{
		"type":           messageType,
		"message_id":     randomUUID(),
		"trace_id":       traceID,
		"sent_at":        time.Now().UTC().Format(time.RFC3339),
		"schema_version": schemaVersion,
		"payload":        payload,
	}
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableMap(value map[string]string) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

func nullableInt(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}

func writeWSError(conn *websocket.Conn, traceID string, code string, message string) {
	_ = conn.WriteJSON(newWSEnvelope("error", traceID, map[string]any{
		"code":    code,
		"message": message,
	}))
}
