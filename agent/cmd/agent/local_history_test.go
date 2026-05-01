package main

import (
	"path/filepath"
	"testing"
	"time"
)

func TestLocalHistoryStoresSessionApprovalAndDecision(t *testing.T) {
	t.Setenv("GATEPILOT_AGENT_HISTORY", filepath.Join(t.TempDir(), "history.json"))
	startedAt := time.Now().UTC().Format(time.RFC3339)

	if err := upsertLocalSession(localSessionRecord{
		SessionID:           "session-1",
		CLIType:             "custom",
		CommandLineRedacted: "fake-ai-cli",
		WorkingDir:          "E:\\WorkSpace",
		WorkingDirHash:      "sha256:test",
		Status:              "running",
		StartedAt:           startedAt,
		LastOutputSummary:   "started",
		ControlAddr:         "127.0.0.1:1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := appendLocalOutput(localOutputRecord{
		SessionID:       "session-1",
		SequenceNo:      1,
		StreamType:      "stdout",
		ContentRedacted: "prompt",
		ContentHash:     "sha256:prompt",
		CreatedAt:       startedAt,
	}); err != nil {
		t.Fatal(err)
	}
	if err := upsertLocalApproval(localApprovalRecord{
		ApprovalID: "approval-1",
		SessionID:  "session-1",
		CLIType:    "custom",
		EventType:  "permission_request",
		RiskLevel:  "high",
		PromptText: "allow?",
		Status:     "waiting_decision",
		CreatedAt:  startedAt,
	}); err != nil {
		t.Fatal(err)
	}
	if err := appendLocalDecision(localDecisionRecord{
		ApprovalID:   "approval-1",
		SessionID:    "session-1",
		DecisionType: "approve",
		BytesWritten: 8,
		Result:       "written",
		CreatedAt:    startedAt,
	}); err != nil {
		t.Fatal(err)
	}
	if err := upsertLocalSession(localSessionRecord{
		SessionID:         "session-1",
		Status:            "completed",
		EndedAt:           startedAt,
		LastOutputSummary: "done",
		ControlAddr:       "",
	}); err != nil {
		t.Fatal(err)
	}

	sessions, err := listLocalSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Status != "completed" || sessions[0].ControlAddr != "" {
		t.Fatalf("sessions = %+v, want completed session without control addr", sessions)
	}
	detail, ok, err := localSessionDetail("session-1")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("session detail missing")
	}
	if len(detail["output"].([]localOutputRecord)) != 1 || len(detail["approvals"].([]localApprovalRecord)) != 1 || len(detail["decisions"].([]localDecisionRecord)) != 1 {
		t.Fatalf("detail = %+v, want output approval and decision", detail)
	}
}
