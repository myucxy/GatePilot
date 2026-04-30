package main

import "testing"

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
