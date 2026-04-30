package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/myucxy/gatepilot/agent/internal/localqueue"
)

func TestApprovalIdempotencyKeyIsStable(t *testing.T) {
	got := approvalIdempotencyKey(
		"device-123",
		"session-456",
		"custom",
		"permission_request: allow command execution?",
		"GatePilot fake AI CLI",
	)
	want := "c8dc3fa55c9996c4de90827d19cc8c406da11394a467c4489064f240e192c115"

	if got != want {
		t.Fatalf("approvalIdempotencyKey() = %q, want %q", got, want)
	}
	if len(got) != 64 {
		t.Fatalf("approvalIdempotencyKey() length = %d, want 64", len(got))
	}
}

func TestApprovalIdempotencyKeyChangesWithStableInputs(t *testing.T) {
	base := approvalIdempotencyKey(
		"device-123",
		"session-456",
		"custom",
		"permission_request: allow command execution?",
		"GatePilot fake AI CLI",
	)

	tests := []struct {
		name          string
		deviceID      string
		sessionID     string
		cliType       string
		promptText    string
		contextBefore string
	}{
		{
			name:          "device",
			deviceID:      "device-999",
			sessionID:     "session-456",
			cliType:       "custom",
			promptText:    "permission_request: allow command execution?",
			contextBefore: "GatePilot fake AI CLI",
		},
		{
			name:          "session",
			deviceID:      "device-123",
			sessionID:     "session-999",
			cliType:       "custom",
			promptText:    "permission_request: allow command execution?",
			contextBefore: "GatePilot fake AI CLI",
		},
		{
			name:          "cli type",
			deviceID:      "device-123",
			sessionID:     "session-456",
			cliType:       "codex",
			promptText:    "permission_request: allow command execution?",
			contextBefore: "GatePilot fake AI CLI",
		},
		{
			name:          "prompt",
			deviceID:      "device-123",
			sessionID:     "session-456",
			cliType:       "custom",
			promptText:    "permission_request: allow file write?",
			contextBefore: "GatePilot fake AI CLI",
		},
		{
			name:          "context",
			deviceID:      "device-123",
			sessionID:     "session-456",
			cliType:       "custom",
			promptText:    "permission_request: allow command execution?",
			contextBefore: "different context",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := approvalIdempotencyKey(tt.deviceID, tt.sessionID, tt.cliType, tt.promptText, tt.contextBefore)
			if got == base {
				t.Fatalf("approvalIdempotencyKey() = base key %q after changing %s", got, tt.name)
			}
		})
	}
}

func TestSHA256String(t *testing.T) {
	got := sha256String("GatePilot output ready")
	want := "5ebdeb186e2c69d8384030b47254f8de4407fff2a694129e9d61008eb27c8ce1"
	if got != want {
		t.Fatalf("sha256String() = %q, want %q", got, want)
	}
}

func TestApprovalEventPayloadDefaultsExpiry(t *testing.T) {
	payload := approvalEventPayload(localqueue.ApprovalEvent{
		DeviceID:       "device-1",
		SessionID:      "session-1",
		CLIType:        "custom",
		EventType:      "permission_request",
		RiskLevel:      "high",
		PromptText:     "allow command?",
		ContextBefore:  "context",
		IdempotencyKey: "idem-1",
	})
	if got := payload["expires_in_seconds"]; got != 300 {
		t.Fatalf("expires_in_seconds = %v, want 300", got)
	}
	if got := payload["idempotency_key"]; got != "idem-1" {
		t.Fatalf("idempotency_key = %v, want idem-1", got)
	}
}

func TestFlushQueuedApprovalsPostsAndRemovesEvents(t *testing.T) {
	queuePath := filepath.Join(t.TempDir(), "queue.jsonl")
	t.Setenv("GATEPILOT_AGENT_QUEUE", queuePath)

	queue := localqueue.New(queuePath)
	if err := queue.EnqueueApproval(localqueue.ApprovalEvent{
		EventID:          "evt_1",
		DeviceID:         "device-1",
		SessionID:        "session-1",
		CLIType:          "custom",
		EventType:        "permission_request",
		RiskLevel:        "high",
		PromptText:       "allow command?",
		ContextBefore:    "context",
		IdempotencyKey:   "idem-1",
		SuggestedActions: []string{"approve", "reject"},
		ExpiresInSeconds: 300,
	}); err != nil {
		t.Fatal(err)
	}

	received := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/agent/approvals" {
			t.Fatalf("path = %s, want approvals", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token-1" {
			t.Fatalf("authorization = %q, want bearer token", got)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["idempotency_key"] != "idem-1" {
			t.Fatalf("payload = %v, want idempotency key", payload)
		}
		received++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"approval_id":"approval-1"}}`))
	}))
	defer server.Close()

	flushed, err := flushQueuedApprovals(server.URL, "token-1")
	if err != nil {
		t.Fatal(err)
	}
	if flushed != 1 || received != 1 {
		t.Fatalf("flushed=%d received=%d, want 1", flushed, received)
	}
	items, err := queue.ListApprovals()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("queue items = %+v, want empty", items)
	}
}
