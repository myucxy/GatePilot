package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"
)

func TestPostgresDeliveryRetryMarksExhaustedDeliveryFailed(t *testing.T) {
	withPostgresTestStore(t)

	server := httptest.NewServer(newRouter())
	defer server.Close()

	code := createTestActivationCode(t, server.URL)
	deviceID := registerTestDevice(t, server.URL, code)
	sessionID := createTestSession(t, server.URL, deviceID)
	approvalID := createTestApproval(t, server.URL, deviceID, sessionID)
	postJSON(t, server.URL+"/api/v1/approvals/"+approvalID+"/decision", map[string]any{
		"decision_type": "approve",
	}, http.StatusOK)

	retryTime := time.Now().UTC().Add(2 * time.Minute)
	retry, failed := store.RetryDeliveries(retryTime, 30*time.Second, 2)
	if len(retry) != 1 || retry[0].ApprovalID != approvalID || retry[0].DeliveryAttempts != 2 || len(failed) != 0 {
		t.Fatalf("retry=%+v failed=%+v, want one postgres retry attempt", retry, failed)
	}

	retry, failed = store.RetryDeliveries(retryTime.Add(2*time.Minute), 30*time.Second, 2)
	if len(retry) != 0 || len(failed) != 1 || failed[0].Status != "delivery_failed" || failed[0].DeliveryStatus != "failed" {
		t.Fatalf("retry=%+v failed=%+v, want exhausted postgres failed delivery", retry, failed)
	}

	detail := getJSON(t, server.URL+"/api/v1/sessions/"+sessionID, http.StatusOK)
	sessionData := detail["data"].(map[string]any)
	if sessionData["status"] != "running" || sessionData["pending_approval_count"] != float64(0) {
		t.Fatalf("session after exhausted postgres delivery = %v, want running with no pending approvals", sessionData)
	}

	audit := getJSON(t, server.URL+"/api/v1/tenants/"+testTenantID+"/audit-logs?action=delivery.retry_exhausted", http.StatusOK)
	if items := dataItems(t, audit); len(items) != 1 || items[0]["resource_id"] != approvalID {
		t.Fatalf("audit logs = %v, want postgres retry exhausted audit", items)
	}
}

func TestPostgresApprovalSupersedeCancelsWaitingDecision(t *testing.T) {
	withPostgresTestStore(t)

	server := httptest.NewServer(newRouter())
	defer server.Close()

	code := createTestActivationCode(t, server.URL)
	deviceID := registerTestDevice(t, server.URL, code)
	sessionID := createTestSession(t, server.URL, deviceID)
	approvalID := createTestApproval(t, server.URL, deviceID, sessionID)

	body := postJSON(t, server.URL+"/api/v1/agent/approval-supersedes", map[string]any{
		"approval_id": approvalID,
		"session_id":  sessionID,
		"reason":      "operator typed locally",
		"detail": map[string]any{
			"source": "postgres-test",
		},
	}, http.StatusOK)
	data := body["data"].(map[string]any)
	if data["status"] != "cancelled_by_local_input" || data["decision_type"] != "local_input" || data["delivery_status"] != "cancelled" {
		t.Fatalf("superseded postgres approval = %v, want local cancellation", data)
	}

	detail := getJSON(t, server.URL+"/api/v1/sessions/"+sessionID, http.StatusOK)
	sessionData := detail["data"].(map[string]any)
	if sessionData["status"] != "running" || sessionData["pending_approval_count"] != float64(0) {
		t.Fatalf("session after postgres supersede = %v, want running with no pending approvals", sessionData)
	}
}

func TestPostgresPolicyRuleAutoRejectCreatesDelivery(t *testing.T) {
	withPostgresTestStore(t)

	server := httptest.NewServer(newRouter())
	defer server.Close()

	postJSON(t, server.URL+"/api/v1/tenants/"+testTenantID+"/policy-rules", map[string]any{
		"name":            "postgres reject fake command",
		"priority":        1,
		"command_pattern": "allow command",
		"decision":        "auto_reject",
		"reason":          "postgres policy",
	}, http.StatusCreated)
	code := createTestActivationCode(t, server.URL)
	deviceID := registerTestDevice(t, server.URL, code)
	sessionID := createTestSession(t, server.URL, deviceID)
	body := createTestApprovalBody(t, server.URL, deviceID, sessionID, randomUUID())
	data := body["data"].(map[string]any)
	if data["status"] != "delivering" || data["decision_type"] != "policy_reject" || data["delivery_status"] != "sent" {
		t.Fatalf("postgres policy approval = %v, want delivering policy reject", data)
	}
}

func withPostgresTestStore(t *testing.T) {
	t.Helper()
	databaseURL := os.Getenv("GATEPILOT_POSTGRES_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("GATEPILOT_POSTGRES_TEST_DATABASE_URL is not set")
	}

	postgresStore, err := newPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		postgresStore.db.Close()
	})

	previousStore := store
	previousAgentHub := agentHub
	previousClientHub := clientHub
	previousDeviceTokens := testDeviceTokens
	previousApprovalTokens := testApprovalTokens
	store = postgresStore
	agentHub = &agentConnectionHub{byDevice: map[string]*agentConnection{}}
	clientHub = &clientConnectionHub{byTenant: map[string]map[string]*clientConnection{}}
	testDeviceTokens = sync.Map{}
	testApprovalTokens = sync.Map{}
	t.Cleanup(func() {
		store = previousStore
		agentHub = previousAgentHub
		clientHub = previousClientHub
		testDeviceTokens = previousDeviceTokens
		testApprovalTokens = previousApprovalTokens
	})
}
