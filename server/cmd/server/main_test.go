package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

const testTenantID = "00000000-0000-0000-0000-000000000100"

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
	if got := dataString(t, decision, "status"); got != "delivered" {
		t.Fatalf("approval status = %q, want delivered", got)
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

func createTestActivationCode(t *testing.T, baseURL string) string {
	t.Helper()
	body := postJSON(t, baseURL+"/api/v1/tenants/"+testTenantID+"/device-activation-codes", map[string]any{
		"name":               "test device",
		"expires_in_seconds": 600,
	}, http.StatusCreated)
	return dataString(t, body, "activation_code")
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
	return dataString(t, body, "device_id")
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
	body := postJSON(t, baseURL+"/api/v1/agent/approvals", map[string]any{
		"device_id":          deviceID,
		"session_id":         sessionID,
		"cli_type":           "custom",
		"event_type":         "permission_request",
		"risk_level":         "high",
		"prompt_text":        "permission_request: allow command execution?",
		"context_before":     "GatePilot fake AI CLI",
		"suggested_actions":  []string{"approve", "reject", "reply"},
		"expires_in_seconds": 300,
	}, http.StatusCreated)
	return dataString(t, body, "approval_id")
}

func resetTestStore() {
	// 每个测试隔离内存状态，避免激活码消费和审批状态相互影响。
	store.mu.Lock()
	defer store.mu.Unlock()
	store.activationCodes = map[string]activationCode{}
	store.devices = map[string]device{}
	store.sessions = map[string]session{}
	store.approvals = map[string]approval{}
}

func getJSON(t *testing.T, url string, wantStatus int) map[string]any {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return decodeJSONResponse(t, resp, wantStatus)
}

func postJSON(t *testing.T, url string, payload map[string]any, wantStatus int) map[string]any {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
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
