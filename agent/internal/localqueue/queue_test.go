package localqueue

import (
	"path/filepath"
	"testing"
	"time"
)

func TestQueueEnqueueListAndRemoveApproval(t *testing.T) {
	queue := New(filepath.Join(t.TempDir(), "queue.jsonl"))
	event := ApprovalEvent{
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
		CreatedAt:        time.Unix(100, 0).UTC(),
	}

	if err := queue.EnqueueApproval(event); err != nil {
		t.Fatal(err)
	}
	if err := queue.EnqueueApproval(event); err != nil {
		t.Fatal(err)
	}
	items, err := queue.ListApprovals()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].EventID != "evt_1" {
		t.Fatalf("items = %+v, want one event", items)
	}
	if err := queue.RemoveApproval("evt_1"); err != nil {
		t.Fatal(err)
	}
	items, err = queue.ListApprovals()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("items after remove = %+v, want empty", items)
	}
}
