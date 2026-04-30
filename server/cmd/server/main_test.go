package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

const testTenantID = "00000000-0000-0000-0000-000000000100"

var testDeviceTokens sync.Map
var testApprovalTokens sync.Map

func TestDeviceSessionApprovalFlow(t *testing.T) {
	resetTestStore()
	server := httptest.NewServer(newRouter())
	defer server.Close()

	code := createTestActivationCode(t, server.URL)
	deviceID := registerTestDevice(t, server.URL, code)
	sessionID := createTestSession(t, server.URL, deviceID)
	approvalID := createTestApproval(t, server.URL, deviceID, sessionID)

	decision := postJSON(t, server.URL+"/api/v1/approvals/"+approvalID+"/decision", map[string]any{
		"decision_type": "approve",
		"payload":       "",
	}, http.StatusOK)
	if got := dataString(t, decision, "status"); got != "delivering" {
		t.Fatalf("approval status = %q, want delivering", got)
	}
	deliveryID := dataString(t, decision, "delivery_id")

	ack := postJSON(t, server.URL+"/api/v1/agent/approval-acks", map[string]any{
		"approval_id": approvalID,
		"delivery_id": deliveryID,
		"session_id":  sessionID,
		"ack_result":  "written",
		"detail":      map[string]any{"source": "unit-test"},
	}, http.StatusOK)
	if got := dataString(t, ack, "status"); got != "delivered" {
		t.Fatalf("approval status after ack = %q, want delivered", got)
	}

	sessions := getJSON(t, server.URL+"/api/v1/devices/"+deviceID+"/sessions", http.StatusOK)
	items := dataItems(t, sessions)
	if len(items) != 1 {
		t.Fatalf("session count = %d, want 1", len(items))
	}
	if got := items[0]["status"]; got != "running" {
		t.Fatalf("session status = %v, want running", got)
	}
	if got := items[0]["pending_approval_count"]; got != float64(0) {
		t.Fatalf("pending approval count = %v, want 0", got)
	}

	sessionDetail := getJSON(t, server.URL+"/api/v1/sessions/"+sessionID, http.StatusOK)
	if got := dataString(t, sessionDetail, "session_id"); got != sessionID {
		t.Fatalf("session detail id = %q, want %q", got, sessionID)
	}
}

func TestApprovalAckFailureMarksDeliveryFailed(t *testing.T) {
	resetTestStore()
	server := httptest.NewServer(newRouter())
	defer server.Close()

	code := createTestActivationCode(t, server.URL)
	deviceID := registerTestDevice(t, server.URL, code)
	sessionID := createTestSession(t, server.URL, deviceID)
	approvalID := createTestApproval(t, server.URL, deviceID, sessionID)
	decision := postJSON(t, server.URL+"/api/v1/approvals/"+approvalID+"/decision", map[string]any{
		"decision_type": "approve",
	}, http.StatusOK)

	ack := postJSON(t, server.URL+"/api/v1/agent/approval-acks", map[string]any{
		"approval_id": approvalID,
		"delivery_id": dataString(t, decision, "delivery_id"),
		"session_id":  sessionID,
		"ack_result":  "write_failed",
		"detail":      map[string]any{"source": "unit-test"},
	}, http.StatusOK)
	if got := dataString(t, ack, "status"); got != "delivery_failed" {
		t.Fatalf("approval status after failed ack = %q, want delivery_failed", got)
	}
	if got := dataString(t, ack, "delivery_status"); got != "failed" {
		t.Fatalf("delivery status after failed ack = %q, want failed", got)
	}
}

func TestAuditLogsCaptureDecisionAndAck(t *testing.T) {
	resetTestStore()
	server := httptest.NewServer(newRouter())
	defer server.Close()

	code := createTestActivationCode(t, server.URL)
	deviceID := registerTestDevice(t, server.URL, code)
	sessionID := createTestSession(t, server.URL, deviceID)
	approvalID := createTestApproval(t, server.URL, deviceID, sessionID)
	decision := postJSON(t, server.URL+"/api/v1/approvals/"+approvalID+"/decision", map[string]any{
		"decision_type": "approve",
	}, http.StatusOK)
	postJSON(t, server.URL+"/api/v1/agent/approval-acks", map[string]any{
		"approval_id": approvalID,
		"delivery_id": dataString(t, decision, "delivery_id"),
		"session_id":  sessionID,
		"ack_result":  "written",
		"detail":      map[string]any{"source": "unit-test"},
	}, http.StatusOK)

	body := getJSON(t, server.URL+"/api/v1/tenants/"+testTenantID+"/audit-logs", http.StatusOK)
	items := dataItems(t, body)
	actions := map[string]bool{}
	for _, item := range items {
		actions[item["action"].(string)] = true
	}
	if !actions["approval.decision"] || !actions["approval.delivery_ack"] {
		t.Fatalf("audit actions = %v, want decision and delivery_ack", actions)
	}
}

func TestViewerCannotReadAuditLogs(t *testing.T) {
	resetTestStore()
	server := httptest.NewServer(newRouter())
	defer server.Close()

	body := getJSONWithHeaders(t, server.URL+"/api/v1/tenants/"+testTenantID+"/audit-logs", map[string]string{
		"X-Dev-Role": "viewer",
	}, http.StatusForbidden)
	errorBody, ok := body["error"].(map[string]any)
	if !ok || errorBody["code"] != "role_insufficient" {
		t.Fatalf("error code = %v, want role_insufficient", body)
	}
}

func TestActivationCodeCanOnlyBeConsumedOnce(t *testing.T) {
	resetTestStore()
	server := httptest.NewServer(newRouter())
	defer server.Close()

	code := createTestActivationCode(t, server.URL)
	registerTestDevice(t, server.URL, code)

	body := postJSON(t, server.URL+"/api/v1/agent/register", map[string]any{
		"activation_code":  code,
		"device_name":      "repeat device",
		"platform":         "windows",
		"arch":             "amd64",
		"agent_version":    version,
		"protocol_version": "2026-04-01",
	}, http.StatusUnprocessableEntity)
	errorBody, ok := body["error"].(map[string]any)
	if !ok || errorBody["code"] != "activation_code_invalid" {
		t.Fatalf("error code = %v, want activation_code_invalid", body)
	}
}

func TestAgentSessionRequiresDeviceToken(t *testing.T) {
	resetTestStore()
	server := httptest.NewServer(newRouter())
	defer server.Close()

	code := createTestActivationCode(t, server.URL)
	deviceID := registerTestDevice(t, server.URL, code)
	body := postJSONWithHeaders(t, server.URL+"/api/v1/agent/sessions", map[string]any{
		"device_id":             deviceID,
		"cli_type":              "custom",
		"command_line_redacted": "gatepilot fake",
		"working_dir_hash":      "sha256:test",
	}, map[string]string{
		"Idempotency-Key": randomUUID(),
		"Authorization":   "Bearer invalid",
	}, http.StatusUnauthorized)

	errorBody, ok := body["error"].(map[string]any)
	if !ok || errorBody["code"] != "device_token_invalid" {
		t.Fatalf("error code = %v, want device_token_invalid", body)
	}
}

func TestApprovalDecisionRejectsSecondDecision(t *testing.T) {
	resetTestStore()
	server := httptest.NewServer(newRouter())
	defer server.Close()

	code := createTestActivationCode(t, server.URL)
	deviceID := registerTestDevice(t, server.URL, code)
	sessionID := createTestSession(t, server.URL, deviceID)
	approvalID := createTestApproval(t, server.URL, deviceID, sessionID)

	postJSON(t, server.URL+"/api/v1/approvals/"+approvalID+"/decision", map[string]any{
		"decision_type": "approve",
	}, http.StatusOK)
	body := postJSON(t, server.URL+"/api/v1/approvals/"+approvalID+"/decision", map[string]any{
		"decision_type": "reject",
	}, http.StatusConflict)

	errorBody, ok := body["error"].(map[string]any)
	if !ok || errorBody["code"] != "approval_already_decided" {
		t.Fatalf("error code = %v, want approval_already_decided", body)
	}
}

func TestViewerCannotSubmitApprovalDecision(t *testing.T) {
	resetTestStore()
	server := httptest.NewServer(newRouter())
	defer server.Close()

	code := createTestActivationCode(t, server.URL)
	deviceID := registerTestDevice(t, server.URL, code)
	sessionID := createTestSession(t, server.URL, deviceID)
	approvalID := createTestApproval(t, server.URL, deviceID, sessionID)

	body := postJSONWithHeaders(t, server.URL+"/api/v1/approvals/"+approvalID+"/decision", map[string]any{
		"decision_type": "approve",
		"payload":       "",
	}, map[string]string{
		"Idempotency-Key": randomUUID(),
		"X-Dev-Role":      "viewer",
	}, http.StatusForbidden)

	errorBody, ok := body["error"].(map[string]any)
	if !ok || errorBody["code"] != "role_insufficient" {
		t.Fatalf("error code = %v, want role_insufficient", body)
	}
}

func TestApproverRequiresDeviceGrant(t *testing.T) {
	resetTestStore()
	server := httptest.NewServer(newRouter())
	defer server.Close()

	code := createTestActivationCode(t, server.URL)
	deviceID := registerTestDevice(t, server.URL, code)
	sessionID := createTestSession(t, server.URL, deviceID)
	approvalID := createTestApproval(t, server.URL, deviceID, sessionID)

	body := postJSONWithHeaders(t, server.URL+"/api/v1/approvals/"+approvalID+"/decision", map[string]any{
		"decision_type": "approve",
		"payload":       "",
	}, map[string]string{
		"Idempotency-Key": randomUUID(),
		"X-Dev-Role":      "approver",
		"X-Dev-User-Id":   "00000000-0000-0000-0000-000000000099",
	}, http.StatusForbidden)

	errorBody, ok := body["error"].(map[string]any)
	if !ok || errorBody["code"] != "device_access_denied" {
		t.Fatalf("error code = %v, want device_access_denied", body)
	}
}

func TestApproverCanSubmitApprovalDecisionWithDeviceGrant(t *testing.T) {
	resetTestStore()
	server := httptest.NewServer(newRouter())
	defer server.Close()

	code := createTestActivationCode(t, server.URL)
	deviceID := registerTestDevice(t, server.URL, code)
	sessionID := createTestSession(t, server.URL, deviceID)
	approvalID := createTestApproval(t, server.URL, deviceID, sessionID)
	approverID := "00000000-0000-0000-0000-000000000099"

	postJSON(t, server.URL+"/api/v1/devices/"+deviceID+"/grants", map[string]any{
		"user_id":    approverID,
		"permission": "approve",
	}, http.StatusCreated)

	body := postJSONWithHeaders(t, server.URL+"/api/v1/approvals/"+approvalID+"/decision", map[string]any{
		"decision_type": "approve",
		"payload":       "",
	}, map[string]string{
		"Idempotency-Key": randomUUID(),
		"X-Dev-Role":      "approver",
		"X-Dev-User-Id":   approverID,
	}, http.StatusOK)
	if got := dataString(t, body, "status"); got != "delivering" {
		t.Fatalf("approval status = %q, want delivering", got)
	}
}

func TestApprovalDecisionIdempotencyReplaysFirstResult(t *testing.T) {
	resetTestStore()
	server := httptest.NewServer(newRouter())
	defer server.Close()

	code := createTestActivationCode(t, server.URL)
	deviceID := registerTestDevice(t, server.URL, code)
	sessionID := createTestSession(t, server.URL, deviceID)
	approvalID := createTestApproval(t, server.URL, deviceID, sessionID)

	headers := map[string]string{"Idempotency-Key": "same-decision-key"}
	first := postJSONWithHeaders(t, server.URL+"/api/v1/approvals/"+approvalID+"/decision", map[string]any{
		"decision_type": "approve",
		"payload":       "",
	}, headers, http.StatusOK)
	replay := postJSONWithHeaders(t, server.URL+"/api/v1/approvals/"+approvalID+"/decision", map[string]any{
		"decision_type": "approve",
		"payload":       "",
	}, headers, http.StatusOK)

	if got, want := dataString(t, replay, "delivery_id"), dataString(t, first, "delivery_id"); got != want {
		t.Fatalf("replayed delivery_id = %q, want %q", got, want)
	}
}

func TestApprovalDecisionIdempotencyRejectsParameterConflict(t *testing.T) {
	resetTestStore()
	server := httptest.NewServer(newRouter())
	defer server.Close()

	code := createTestActivationCode(t, server.URL)
	deviceID := registerTestDevice(t, server.URL, code)
	sessionID := createTestSession(t, server.URL, deviceID)
	approvalID := createTestApproval(t, server.URL, deviceID, sessionID)

	headers := map[string]string{"Idempotency-Key": "conflicting-decision-key"}
	postJSONWithHeaders(t, server.URL+"/api/v1/approvals/"+approvalID+"/decision", map[string]any{
		"decision_type": "approve",
		"payload":       "",
	}, headers, http.StatusOK)
	body := postJSONWithHeaders(t, server.URL+"/api/v1/approvals/"+approvalID+"/decision", map[string]any{
		"decision_type": "reject",
		"payload":       "",
	}, headers, http.StatusConflict)

	errorBody, ok := body["error"].(map[string]any)
	if !ok || errorBody["code"] != "approval_decision_conflict" {
		t.Fatalf("error code = %v, want approval_decision_conflict", body)
	}
}

func TestActivationCodeIdempotencyReplaysFirstCode(t *testing.T) {
	resetTestStore()
	server := httptest.NewServer(newRouter())
	defer server.Close()

	headers := map[string]string{"Idempotency-Key": "activation-key"}
	first := postJSONWithHeaders(t, server.URL+"/api/v1/tenants/"+testTenantID+"/device-activation-codes", map[string]any{
		"name":               "test device",
		"expires_in_seconds": 600,
	}, headers, http.StatusCreated)
	replay := postJSONWithHeaders(t, server.URL+"/api/v1/tenants/"+testTenantID+"/device-activation-codes", map[string]any{
		"name":               "test device",
		"expires_in_seconds": 600,
	}, headers, http.StatusCreated)

	if got, want := dataString(t, replay, "activation_code"), dataString(t, first, "activation_code"); got != want {
		t.Fatalf("replayed activation_code = %q, want %q", got, want)
	}
}

func TestAgentApprovalIdempotencyReplaysFirstApproval(t *testing.T) {
	resetTestStore()
	server := httptest.NewServer(newRouter())
	defer server.Close()

	code := createTestActivationCode(t, server.URL)
	deviceID := registerTestDevice(t, server.URL, code)
	sessionID := createTestSession(t, server.URL, deviceID)

	first := createTestApprovalBody(t, server.URL, deviceID, sessionID, "approval-key")
	replay := createTestApprovalBody(t, server.URL, deviceID, sessionID, "approval-key")
	if got, want := dataString(t, replay, "approval_id"), dataString(t, first, "approval_id"); got != want {
		t.Fatalf("replayed approval_id = %q, want %q", got, want)
	}
}

func TestClientInstanceRegistrationIdempotencyReplaysFirstInstance(t *testing.T) {
	resetTestStore()
	server := httptest.NewServer(newRouter())
	defer server.Close()

	headers := map[string]string{"Idempotency-Key": "client-instance-key"}
	first := postJSONWithHeaders(t, server.URL+"/api/v1/client-instances", map[string]any{
		"tenant_id":    testTenantID,
		"client_type":  "web",
		"display_name": "Browser",
		"app_version":  version,
		"platform":     "browser",
	}, headers, http.StatusCreated)
	replay := postJSONWithHeaders(t, server.URL+"/api/v1/client-instances", map[string]any{
		"tenant_id":    testTenantID,
		"client_type":  "web",
		"display_name": "Browser",
		"app_version":  version,
		"platform":     "browser",
	}, headers, http.StatusCreated)

	if got, want := dataString(t, replay, "client_instance_id"), dataString(t, first, "client_instance_id"); got != want {
		t.Fatalf("replayed client_instance_id = %q, want %q", got, want)
	}
}

func TestClientInstancePushTokenRegistration(t *testing.T) {
	resetTestStore()
	server := httptest.NewServer(newRouter())
	defer server.Close()

	clientInstanceID := registerTestClientInstance(t, server.URL)
	body := postJSON(t, server.URL+"/api/v1/client-instances/"+clientInstanceID+"/push-token", map[string]any{
		"provider": "fcm",
		"token":    "push-token-secret",
	}, http.StatusOK)
	if got := dataString(t, body, "push_provider"); got != "fcm" {
		t.Fatalf("push_provider = %q, want fcm", got)
	}
}

func TestExpiryWorkerRejectsExpiredApprovalsForDelivery(t *testing.T) {
	resetTestStore()
	server := httptest.NewServer(newRouter())
	defer server.Close()

	code := createTestActivationCode(t, server.URL)
	deviceID := registerTestDevice(t, server.URL, code)
	sessionID := createTestSession(t, server.URL, deviceID)
	approvalID := createTestApproval(t, server.URL, deviceID, sessionID)

	memory, ok := store.(*memoryStore)
	if !ok {
		t.Fatal("test requires memory store")
	}
	memory.mu.Lock()
	item := memory.approvals[approvalID]
	item.ExpiresAt = time.Now().UTC().Add(-time.Second).Format(time.RFC3339)
	memory.approvals[approvalID] = item
	memory.mu.Unlock()

	runExpiryWorkerOnce()
	approvals := getJSON(t, server.URL+"/api/v1/tenants/"+testTenantID+"/approvals", http.StatusOK)
	for _, item := range dataItems(t, approvals) {
		if item["approval_id"] == approvalID {
			if item["status"] != "delivering" || item["decision_type"] != "reject" || item["delivery_status"] != "sent" {
				t.Fatalf("expired approval = %v, want delivering reject sent", item)
			}
			return
		}
	}
	t.Fatalf("expired approval %s not found", approvalID)
}

func TestClientWebSocketReceivesApprovalCreatedAndUpdated(t *testing.T) {
	resetTestStore()
	server := httptest.NewServer(newRouter())
	defer server.Close()

	clientInstanceID := registerTestClientInstance(t, server.URL)
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws/client?tenant_id=" + testTenantID + "&client_instance_id=" + clientInstanceID
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	var connected map[string]any
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if err := conn.ReadJSON(&connected); err != nil {
		t.Fatal(err)
	}
	if got := connected["type"]; got != "client.connected" {
		t.Fatalf("client ws response type = %v, want client.connected", got)
	}

	code := createTestActivationCode(t, server.URL)
	deviceID := registerTestDevice(t, server.URL, code)
	sessionID := createTestSession(t, server.URL, deviceID)
	approvalID := createTestApproval(t, server.URL, deviceID, sessionID)

	var created struct {
		Type    string `json:"type"`
		Payload struct {
			ApprovalID string `json:"approval_id"`
		} `json:"payload"`
	}
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if err := conn.ReadJSON(&created); err != nil {
		t.Fatal(err)
	}
	if created.Type != "approval.created" || created.Payload.ApprovalID != approvalID {
		t.Fatalf("created event = %+v, want approval %s", created, approvalID)
	}

	var sessionChanged struct {
		Type    string `json:"type"`
		Payload struct {
			SessionID        string `json:"session_id"`
			Status           string `json:"status"`
			PendingApprovals int    `json:"pending_approval_count"`
		} `json:"payload"`
	}
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if err := conn.ReadJSON(&sessionChanged); err != nil {
		t.Fatal(err)
	}
	if sessionChanged.Type != "session.updated" || sessionChanged.Payload.SessionID != sessionID || sessionChanged.Payload.Status != "waiting_approval" || sessionChanged.Payload.PendingApprovals != 1 {
		t.Fatalf("session changed event = %+v, want waiting_approval for session %s", sessionChanged, sessionID)
	}

	postJSONWithHeaders(t, server.URL+"/api/v1/approvals/"+approvalID+"/decision", map[string]any{
		"decision_type": "approve",
		"payload":       "",
	}, map[string]string{
		"Idempotency-Key":      randomUUID(),
		"X-Client-Instance-Id": clientInstanceID,
	}, http.StatusOK)

	var updated struct {
		Type    string `json:"type"`
		Payload struct {
			ApprovalID   string `json:"approval_id"`
			Status       string `json:"status"`
			DecisionType string `json:"decision_type"`
		} `json:"payload"`
	}
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if err := conn.ReadJSON(&updated); err != nil {
		t.Fatal(err)
	}
	if updated.Type != "approval.updated" || updated.Payload.ApprovalID != approvalID || updated.Payload.Status != "delivering" || updated.Payload.DecisionType != "approve" {
		t.Fatalf("updated event = %+v, want delivering approve for approval %s", updated, approvalID)
	}
}

func TestOutputChunksAppendListAndUpdateSessionSummary(t *testing.T) {
	resetTestStore()
	server := httptest.NewServer(newRouter())
	defer server.Close()

	code := createTestActivationCode(t, server.URL)
	deviceID := registerTestDevice(t, server.URL, code)
	sessionID := createTestSession(t, server.URL, deviceID)

	chunk := postJSON(t, server.URL+"/api/v1/agent/output-chunks", map[string]any{
		"device_id":        deviceID,
		"session_id":       sessionID,
		"sequence_no":      1,
		"stream_type":      "stdout",
		"content_redacted": "GatePilot output ready",
		"content_hash":     "sha256:test-output",
	}, http.StatusCreated)
	if got := dataString(t, chunk, "content_redacted"); got != "GatePilot output ready" {
		t.Fatalf("chunk content = %q, want output", got)
	}

	body := getJSON(t, server.URL+"/api/v1/sessions/"+sessionID+"/output-chunks", http.StatusOK)
	items := dataItems(t, body)
	if len(items) != 1 || items[0]["sequence_no"] != float64(1) {
		t.Fatalf("output chunks = %v, want sequence 1", items)
	}
	sessionDetail := getJSON(t, server.URL+"/api/v1/sessions/"+sessionID, http.StatusOK)
	if got := dataString(t, sessionDetail, "last_output_summary"); got != "GatePilot output ready" {
		t.Fatalf("last output summary = %q, want chunk content", got)
	}
}

func TestDeviceGrantListAndRevoke(t *testing.T) {
	resetTestStore()
	server := httptest.NewServer(newRouter())
	defer server.Close()

	code := createTestActivationCode(t, server.URL)
	deviceID := registerTestDevice(t, server.URL, code)
	approverID := "00000000-0000-0000-0000-000000000099"

	grant := postJSON(t, server.URL+"/api/v1/devices/"+deviceID+"/grants", map[string]any{
		"user_id":    approverID,
		"permission": "approve",
	}, http.StatusCreated)
	grantID := dataString(t, grant, "grant_id")

	list := getJSON(t, server.URL+"/api/v1/devices/"+deviceID+"/grants", http.StatusOK)
	items := dataItems(t, list)
	if len(items) != 1 || items[0]["grant_id"] != grantID {
		t.Fatalf("device grants = %v, want grant %s", items, grantID)
	}

	deleteJSON(t, server.URL+"/api/v1/devices/"+deviceID+"/grants/"+grantID, http.StatusOK)
	list = getJSON(t, server.URL+"/api/v1/devices/"+deviceID+"/grants", http.StatusOK)
	if items := dataItems(t, list); len(items) != 0 {
		t.Fatalf("device grants after revoke = %v, want empty", items)
	}

	sessionID := createTestSession(t, server.URL, deviceID)
	approvalID := createTestApproval(t, server.URL, deviceID, sessionID)
	body := postJSONWithHeaders(t, server.URL+"/api/v1/approvals/"+approvalID+"/decision", map[string]any{
		"decision_type": "approve",
		"payload":       "",
	}, map[string]string{
		"Idempotency-Key": randomUUID(),
		"X-Dev-Role":      "approver",
		"X-Dev-User-Id":   approverID,
	}, http.StatusForbidden)
	errorBody, ok := body["error"].(map[string]any)
	if !ok || errorBody["code"] != "device_access_denied" {
		t.Fatalf("error code = %v, want device_access_denied", body)
	}
}

func TestAgentWebSocketHelloAndHeartbeat(t *testing.T) {
	resetTestStore()
	server := httptest.NewServer(newRouter())
	defer server.Close()

	code := createTestActivationCode(t, server.URL)
	deviceID := registerTestDevice(t, server.URL, code)

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws/agent"
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+testTokenForDevice(t, deviceID))
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if err := conn.WriteJSON(testWSEnvelope("agent.hello", map[string]any{
		"device_id":        deviceID,
		"agent_version":    version,
		"protocol_version": "2026-04-01",
		"platform":         "windows",
		"capabilities":     map[string]any{"conpty": true},
	})); err != nil {
		t.Fatal(err)
	}

	var connected map[string]any
	if err := conn.ReadJSON(&connected); err != nil {
		t.Fatal(err)
	}
	if got := connected["type"]; got != "agent.connected" {
		t.Fatalf("ws response type = %v, want agent.connected", got)
	}

	if err := conn.WriteJSON(testWSEnvelope("agent.heartbeat", map[string]any{
		"active_sessions":   0,
		"local_queue_depth": 0,
		"last_error":        nil,
	})); err != nil {
		t.Fatal(err)
	}
}

func TestStaleDeviceWorkerMarksDeviceOffline(t *testing.T) {
	resetTestStore()
	server := httptest.NewServer(newRouter())
	defer server.Close()

	code := createTestActivationCode(t, server.URL)
	deviceID := registerTestDevice(t, server.URL, code)

	memory, ok := store.(*memoryStore)
	if !ok {
		t.Fatal("test requires memory store")
	}
	memory.mu.Lock()
	item := memory.devices[deviceID]
	item.LastSeen = time.Now().UTC().Add(-5 * time.Minute).Format(time.RFC3339)
	memory.devices[deviceID] = item
	memory.mu.Unlock()

	changed := store.MarkStaleDevicesOffline(time.Now().UTC(), 3*time.Minute)
	if len(changed) != 1 || changed[0].DeviceID != deviceID || changed[0].Status != "offline" {
		t.Fatalf("changed devices = %+v, want offline device %s", changed, deviceID)
	}
	devices := getJSON(t, server.URL+"/api/v1/tenants/"+testTenantID+"/devices", http.StatusOK)
	items := dataItems(t, devices)
	if items[0]["status"] != "offline" {
		t.Fatalf("device status = %v, want offline", items[0]["status"])
	}
}

func TestApprovalDecisionIsDeliveredOverAgentWebSocket(t *testing.T) {
	resetTestStore()
	server := httptest.NewServer(newRouter())
	defer server.Close()

	code := createTestActivationCode(t, server.URL)
	deviceID := registerTestDevice(t, server.URL, code)
	sessionID := createTestSession(t, server.URL, deviceID)
	approvalID := createTestApproval(t, server.URL, deviceID, sessionID)

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws/agent"
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+testTokenForDevice(t, deviceID))
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if err := conn.WriteJSON(testWSEnvelope("agent.hello", map[string]any{
		"device_id":        deviceID,
		"agent_version":    version,
		"protocol_version": "2026-04-01",
		"platform":         "windows",
		"capabilities":     map[string]any{"conpty": true},
	})); err != nil {
		t.Fatal(err)
	}
	var connected map[string]any
	if err := conn.ReadJSON(&connected); err != nil {
		t.Fatal(err)
	}

	decision := postJSON(t, server.URL+"/api/v1/approvals/"+approvalID+"/decision", map[string]any{
		"decision_type": "approve",
		"payload":       "",
	}, http.StatusOK)
	deliveryID := dataString(t, decision, "delivery_id")

	var deliver struct {
		Type    string `json:"type"`
		Payload struct {
			DeliveryID string `json:"delivery_id"`
			ApprovalID string `json:"approval_id"`
			SessionID  string `json:"session_id"`
		} `json:"payload"`
	}
	if err := conn.ReadJSON(&deliver); err != nil {
		t.Fatal(err)
	}
	if deliver.Type != "approval.decision.deliver" || deliver.Payload.DeliveryID != deliveryID {
		t.Fatalf("deliver = %+v, want delivery %s", deliver, deliveryID)
	}

	if err := conn.WriteJSON(testWSEnvelope("approval.decision.ack", map[string]any{
		"delivery_id": deliver.Payload.DeliveryID,
		"approval_id": deliver.Payload.ApprovalID,
		"session_id":  deliver.Payload.SessionID,
		"ack_result":  "written",
		"detail":      map[string]any{"source": "unit-test-ws"},
	})); err != nil {
		t.Fatal(err)
	}

	var approvals map[string]any
	for i := 0; i < 10; i++ {
		approvals = getJSON(t, server.URL+"/api/v1/tenants/"+testTenantID+"/approvals", http.StatusOK)
		for _, item := range dataItems(t, approvals) {
			if item["approval_id"] == approvalID && item["status"] == "delivered" {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("approval was not delivered after websocket ack: %v", approvals)
}

func TestPendingApprovalDecisionIsDeliveredWhenAgentReconnects(t *testing.T) {
	resetTestStore()
	server := httptest.NewServer(newRouter())
	defer server.Close()

	code := createTestActivationCode(t, server.URL)
	deviceID := registerTestDevice(t, server.URL, code)
	sessionID := createTestSession(t, server.URL, deviceID)
	approvalID := createTestApproval(t, server.URL, deviceID, sessionID)

	decision := postJSON(t, server.URL+"/api/v1/approvals/"+approvalID+"/decision", map[string]any{
		"decision_type": "approve",
		"payload":       "",
	}, http.StatusOK)
	deliveryID := dataString(t, decision, "delivery_id")

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws/agent"
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+testTokenForDevice(t, deviceID))
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if err := conn.WriteJSON(testWSEnvelope("agent.hello", map[string]any{
		"device_id":        deviceID,
		"agent_version":    version,
		"protocol_version": "2026-04-01",
		"platform":         "windows",
		"capabilities":     map[string]any{"conpty": true},
	})); err != nil {
		t.Fatal(err)
	}
	var connected map[string]any
	if err := conn.ReadJSON(&connected); err != nil {
		t.Fatal(err)
	}

	var deliver struct {
		Type    string `json:"type"`
		Payload struct {
			DeliveryID string `json:"delivery_id"`
		} `json:"payload"`
	}
	if err := conn.ReadJSON(&deliver); err != nil {
		t.Fatal(err)
	}
	if deliver.Type != "approval.decision.deliver" || deliver.Payload.DeliveryID != deliveryID {
		t.Fatalf("reconnect deliver = %+v, want delivery %s", deliver, deliveryID)
	}
}

func createTestActivationCode(t *testing.T, baseURL string) string {
	t.Helper()
	body := postJSON(t, baseURL+"/api/v1/tenants/"+testTenantID+"/device-activation-codes", map[string]any{
		"name":               "test device",
		"expires_in_seconds": 600,
	}, http.StatusCreated)
	return dataString(t, body, "activation_code")
}

func registerTestClientInstance(t *testing.T, baseURL string) string {
	t.Helper()
	body := postJSON(t, baseURL+"/api/v1/client-instances", map[string]any{
		"tenant_id":    testTenantID,
		"client_type":  "web",
		"display_name": "Test Browser",
		"app_version":  version,
		"platform":     "browser",
	}, http.StatusCreated)
	return dataString(t, body, "client_instance_id")
}

func testWSEnvelope(messageType string, payload map[string]any) map[string]any {
	return map[string]any{
		"type":           messageType,
		"message_id":     randomUUID(),
		"trace_id":       "tr_test",
		"sent_at":        time.Now().UTC().Format(time.RFC3339),
		"schema_version": "2026-04-01",
		"payload":        payload,
	}
}

func registerTestDevice(t *testing.T, baseURL string, code string) string {
	t.Helper()
	body := postJSON(t, baseURL+"/api/v1/agent/register", map[string]any{
		"activation_code":  code,
		"device_name":      "test device",
		"platform":         "windows",
		"arch":             "amd64",
		"agent_version":    version,
		"protocol_version": "2026-04-01",
		"capabilities": map[string]any{
			"conpty": true,
		},
	}, http.StatusCreated)
	deviceID := dataString(t, body, "device_id")
	testDeviceTokens.Store(deviceID, dataString(t, body, "device_token"))
	return deviceID
}

func createTestSession(t *testing.T, baseURL string, deviceID string) string {
	t.Helper()
	body := postJSON(t, baseURL+"/api/v1/agent/sessions", map[string]any{
		"device_id":             deviceID,
		"cli_type":              "custom",
		"command_line_redacted": "gatepilot fake",
		"working_dir_hash":      "sha256:test",
		"last_output_summary":   "fake CLI session started",
	}, http.StatusCreated)
	return dataString(t, body, "session_id")
}

func createTestApproval(t *testing.T, baseURL string, deviceID string, sessionID string) string {
	t.Helper()
	body := createTestApprovalBody(t, baseURL, deviceID, sessionID, randomUUID())
	return dataString(t, body, "approval_id")
}

func createTestApprovalBody(t *testing.T, baseURL string, deviceID string, sessionID string, idempotencyKey string) map[string]any {
	t.Helper()
	body := postJSON(t, baseURL+"/api/v1/agent/approvals", map[string]any{
		"device_id":          deviceID,
		"session_id":         sessionID,
		"cli_type":           "custom",
		"event_type":         "permission_request",
		"risk_level":         "high",
		"prompt_text":        "permission_request: allow command execution?",
		"context_before":     "GatePilot fake AI CLI",
		"idempotency_key":    idempotencyKey,
		"suggested_actions":  []string{"approve", "reject", "reply"},
		"expires_in_seconds": 300,
	}, http.StatusCreated)
	testApprovalTokens.Store(dataString(t, body, "approval_id"), testTokenForDevice(t, deviceID))
	return body
}

func resetTestStore() {
	testDeviceTokens = sync.Map{}
	testApprovalTokens = sync.Map{}
	agentHub = &agentConnectionHub{byDevice: map[string]*agentConnection{}}
	clientHub = &clientConnectionHub{byTenant: map[string]map[string]*clientConnection{}}
	// 每个测试隔离内存状态，避免激活码消费和审批状态相互影响。
	if resettable, ok := store.(interface{ Reset() }); ok {
		resettable.Reset()
		return
	}
	store = newMemoryStore()
}

func getJSON(t *testing.T, url string, wantStatus int) map[string]any {
	t.Helper()
	return getJSONWithHeaders(t, url, map[string]string{}, wantStatus)
}

func getJSONWithHeaders(t *testing.T, url string, headers map[string]string, wantStatus int) map[string]any {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return decodeJSONResponse(t, resp, wantStatus)
}

func postJSON(t *testing.T, url string, payload map[string]any, wantStatus int) map[string]any {
	t.Helper()
	return postJSONWithHeaders(t, url, payload, map[string]string{
		"Idempotency-Key": randomUUID(),
	}, wantStatus)
}

func postJSONWithHeaders(t *testing.T, url string, payload map[string]any, headers map[string]string, wantStatus int) map[string]any {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	if req.Header.Get("Authorization") == "" {
		if deviceID, ok := payload["device_id"].(string); ok {
			req.Header.Set("Authorization", "Bearer "+testTokenForDevice(t, deviceID))
		}
		if approvalID, ok := payload["approval_id"].(string); ok {
			req.Header.Set("Authorization", "Bearer "+testTokenForApproval(t, approvalID))
		}
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return decodeJSONResponse(t, resp, wantStatus)
}

func deleteJSON(t *testing.T, url string, wantStatus int) map[string]any {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return decodeJSONResponse(t, resp, wantStatus)
}

func decodeJSONResponse(t *testing.T, resp *http.Response, wantStatus int) map[string]any {
	t.Helper()
	if resp.StatusCode != wantStatus {
		t.Fatalf("status = %d, want %d", resp.StatusCode, wantStatus)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body
}

func testTokenForDevice(t *testing.T, deviceID string) string {
	t.Helper()
	value, ok := testDeviceTokens.Load(deviceID)
	if !ok {
		t.Fatalf("missing test token for device %s", deviceID)
	}
	return value.(string)
}

func testTokenForApproval(t *testing.T, approvalID string) string {
	t.Helper()
	value, ok := testApprovalTokens.Load(approvalID)
	if !ok {
		t.Fatalf("missing test token for approval %s", approvalID)
	}
	return value.(string)
}

func dataString(t *testing.T, body map[string]any, key string) string {
	t.Helper()
	data, ok := body["data"].(map[string]any)
	if !ok {
		t.Fatalf("missing data object: %v", body)
	}
	value, ok := data[key].(string)
	if !ok || value == "" {
		t.Fatalf("missing data.%s: %v", key, body)
	}
	return value
}

func dataItems(t *testing.T, body map[string]any) []map[string]any {
	t.Helper()
	data, ok := body["data"].(map[string]any)
	if !ok {
		t.Fatalf("missing data object: %v", body)
	}
	rawItems, ok := data["items"].([]any)
	if !ok {
		t.Fatalf("missing data.items: %v", body)
	}
	items := make([]map[string]any, 0, len(rawItems))
	for _, raw := range rawItems {
		item, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("invalid item: %v", raw)
		}
		items = append(items, item)
	}
	return items
}
