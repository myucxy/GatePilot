package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/myucxy/gatepilot/agent/internal/adapter"
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

func TestDetectApprovalFromReaderFindsFakePrompt(t *testing.T) {
	event, output, err := detectApprovalFromReader(strings.NewReader("GatePilot fake AI CLI\npermission_request: allow command execution? [approve/reject/reply]\nwaiting_for_input\n"), adapter.ForCLI("custom"))
	if err != nil {
		t.Fatal(err)
	}
	if event.EventType != "permission_request" || event.RiskLevel != "high" {
		t.Fatalf("event = %+v, want high permission request", event)
	}
	if !strings.Contains(output, "permission_request") {
		t.Fatalf("output = %q, want prompt text", output)
	}
}

func TestReadDecisionLineAcceptsCarriageReturn(t *testing.T) {
	got, err := readDecisionLine(strings.NewReader("approve\r"))
	if err != nil {
		t.Fatal(err)
	}
	if got != "approve" {
		t.Fatalf("decision = %q, want approve", got)
	}
}

func TestDeliveryInputTypeMapsPolicyDecisions(t *testing.T) {
	if got := deliveryInputType("policy_approve"); got != "approve" {
		t.Fatalf("policy approve maps to %q, want approve", got)
	}
	if got := deliveryInputType("policy_reject"); got != "reject" {
		t.Fatalf("policy reject maps to %q, want reject", got)
	}
}

func TestLocalDecisionInputUsesConfiguredDecision(t *testing.T) {
	decision, payload, err := localDecisionInput(localUIOptions{
		DecisionType: "approve",
		Payload:      "looks good",
	}, strings.NewReader(""), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if decision != "approve" || payload != "looks good" {
		t.Fatalf("decision=%q payload=%q, want approve payload", decision, payload)
	}
}

func TestLocalDecisionInputRejectsUnsupportedDecision(t *testing.T) {
	_, _, err := localDecisionInput(localUIOptions{DecisionType: "maybe"}, strings.NewReader(""), io.Discard)
	if err == nil {
		t.Fatal("expected unsupported decision error")
	}
}

func TestParseRunCLIOptionsSupportsLocalOnly(t *testing.T) {
	options := parseRunCLIOptions([]string{
		"--local-only",
		"--popup",
		"--decision", "reject",
		"--payload", "no",
		"--cli-type", "codex",
		"--", "codex",
	})
	if !options.LocalOnly || !options.Popup || options.Decision != "reject" || options.Payload != "no" || options.CLIType != "codex" || options.CommandLine != "codex" {
		t.Fatalf("options = %+v, want local-only codex reject", options)
	}
}

func TestLocalDecisionInputUsesPopupDecisionOverride(t *testing.T) {
	t.Setenv("GATEPILOT_AGENT_POPUP_DECISION", "reject")
	var output bytes.Buffer
	decision, payload, err := localDecisionInput(localUIOptions{
		Popup:     true,
		PopupText: "allow command?",
		Payload:   "blocked",
	}, strings.NewReader(""), &output)
	if err != nil {
		t.Fatal(err)
	}
	if decision != "reject" || payload != "blocked" {
		t.Fatalf("decision=%q payload=%q, want popup reject payload", decision, payload)
	}
	if !strings.Contains(output.String(), "local_ui.popup_decision") {
		t.Fatalf("output = %q, want popup decision event", output.String())
	}
}

func TestAgentLocalSettingsDefaultsOffline(t *testing.T) {
	settings := defaultAgentLocalSettings()
	if settings.Mode != "offline" || !settings.NotificationEnabled || settings.NotificationStyle != "mini_window" {
		t.Fatalf("settings = %+v, want offline mini-window notifications enabled", settings)
	}
}

func TestTrayConfirmApprovalUsesNotificationDecision(t *testing.T) {
	t.Setenv("GATEPILOT_AGENT_POPUP_DECISION", "approve")
	state := &trayState{settings: defaultAgentLocalSettings()}
	server := httptest.NewServer(newTrayHTTPHandler(state))
	defer server.Close()

	body, err := json.Marshal(trayApprovalRequest{
		Approval: localApproval{
			ApprovalID: "approval-1",
			CLIType:    "custom",
			EventType:  "permission_request",
			RiskLevel:  "high",
			PromptText: "allow command?",
		},
		WorkingDir: "E:\\WorkSpace\\AICodeProject\\GatePilot",
	})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(server.URL+"/api/local/approvals/confirm", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var decoded struct {
		Data trayDecisionResponse `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Data.DecisionType != "approve" || decoded.Data.Result != "selected" {
		t.Fatalf("decision = %+v, want selected approve", decoded.Data)
	}
}

func TestTraySettingsHandlerPersistsSettings(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	t.Setenv("GATEPILOT_AGENT_SETTINGS", settingsPath)
	state := &trayState{settings: defaultAgentLocalSettings()}
	server := httptest.NewServer(newTrayHTTPHandler(state))
	defer server.Close()

	body, err := json.Marshal(agentLocalSettings{
		Mode:                "offline",
		NotificationEnabled: false,
		NotificationStyle:   "none",
	})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(server.URL+"/api/local/settings", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	loaded, err := loadAgentLocalSettings()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.NotificationEnabled || loaded.NotificationStyle != "none" || loaded.HistoryRetentionDays != 30 {
		t.Fatalf("loaded settings = %+v, want persisted none notifications with defaults", loaded)
	}
}

func TestTrayLoginAndOfflineEndpointsPersistSettings(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	t.Setenv("GATEPILOT_AGENT_SETTINGS", settingsPath)

	identityServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/client-instances" {
			t.Fatalf("path = %s, want client-instances", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"client_instance_id":"client-1"}}`))
	}))
	defer identityServer.Close()

	state := &trayState{settings: defaultAgentLocalSettings()}
	server := httptest.NewServer(newTrayHTTPHandler(state))
	defer server.Close()

	loginBody := mustMarshal(agentLoginOptions{
		ServerURL: identityServer.URL,
		TenantID:  "tenant-1",
		DeviceID:  "device-1",
	})
	loginResp, err := http.Post(server.URL+"/api/local/login", "application/json", bytes.NewReader(loginBody))
	if err != nil {
		t.Fatal(err)
	}
	defer loginResp.Body.Close()
	if loginResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(loginResp.Body)
		t.Fatalf("login status = %d body=%s, want 200", loginResp.StatusCode, body)
	}
	if settings := state.currentSettings(); settings.Mode != "online" || settings.ClientInstanceID != "client-1" {
		t.Fatalf("settings = %+v, want online login", settings)
	}

	offlineResp, err := http.Post(server.URL+"/api/local/offline", "application/json", bytes.NewReader(nil))
	if err != nil {
		t.Fatal(err)
	}
	defer offlineResp.Body.Close()
	if offlineResp.StatusCode != http.StatusOK {
		t.Fatalf("offline status = %d, want 200", offlineResp.StatusCode)
	}
	if settings := state.currentSettings(); settings.Mode != "offline" || settings.ClientInstanceID != "client-1" {
		t.Fatalf("settings = %+v, want offline with login identity retained", settings)
	}

	logoutResp, err := http.Post(server.URL+"/api/local/logout", "application/json", bytes.NewReader(nil))
	if err != nil {
		t.Fatal(err)
	}
	defer logoutResp.Body.Close()
	if logoutResp.StatusCode != http.StatusOK {
		t.Fatalf("logout status = %d, want 200", logoutResp.StatusCode)
	}
	if settings := state.currentSettings(); settings.Mode != "offline" || settings.ClientInstanceID != "" || settings.DeviceID != "" {
		t.Fatalf("settings = %+v, want offline logged out", settings)
	}
}

func TestTraySessionHistoryEndpoints(t *testing.T) {
	t.Setenv("GATEPILOT_AGENT_HISTORY", filepath.Join(t.TempDir(), "history.json"))
	if err := upsertLocalSession(localSessionRecord{
		SessionID:           "session-1",
		CLIType:             "custom",
		CommandLineRedacted: "fake-ai-cli",
		Status:              "completed",
		StartedAt:           "2026-05-01T00:00:00Z",
		LastOutputSummary:   "done",
	}); err != nil {
		t.Fatal(err)
	}
	state := &trayState{settings: defaultAgentLocalSettings()}
	server := httptest.NewServer(newTrayHTTPHandler(state))
	defer server.Close()

	listResp, err := http.Get(server.URL + "/api/local/sessions")
	if err != nil {
		t.Fatal(err)
	}
	defer listResp.Body.Close()
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d, want 200", listResp.StatusCode)
	}
	var listBody struct {
		Data struct {
			Items []localSessionRecord `json:"items"`
		} `json:"data"`
	}
	if err := json.NewDecoder(listResp.Body).Decode(&listBody); err != nil {
		t.Fatal(err)
	}
	if len(listBody.Data.Items) != 1 || listBody.Data.Items[0].SessionID != "session-1" {
		t.Fatalf("sessions = %+v, want session-1", listBody.Data.Items)
	}

	detailResp, err := http.Get(server.URL + "/api/local/sessions/session-1")
	if err != nil {
		t.Fatal(err)
	}
	defer detailResp.Body.Close()
	if detailResp.StatusCode != http.StatusOK {
		t.Fatalf("detail status = %d, want 200", detailResp.StatusCode)
	}
}

func TestSendLocalSessionInputWritesToActiveHost(t *testing.T) {
	t.Setenv("GATEPILOT_AGENT_HISTORY", filepath.Join(t.TempDir(), "history.json"))
	var sink bytes.Buffer
	host, err := startLocalSessionHost("session-1", &sink)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	if err := upsertLocalSession(localSessionRecord{
		SessionID:           "session-1",
		CLIType:             "custom",
		CommandLineRedacted: "fake-ai-cli",
		Status:              "running",
		StartedAt:           "2026-05-01T00:00:00Z",
		ControlAddr:         localHostAddress(host),
	}); err != nil {
		t.Fatal(err)
	}
	if err := sendLocalSessionInput("session-1", "continue"); err != nil {
		t.Fatal(err)
	}
	if got := sink.String(); got != "continue\r" {
		t.Fatalf("input = %q, want continue carriage return", got)
	}
	detail, ok, err := localSessionDetail("session-1")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("missing session detail")
	}
	decisions := detail["decisions"].([]localDecisionRecord)
	if len(decisions) != 1 || decisions[0].DecisionType != "reply" || decisions[0].Result != "manual_input" {
		t.Fatalf("decisions = %+v, want manual reply record", decisions)
	}
}

func TestConfirmLocalApprovalWritesDecisionInput(t *testing.T) {
	var decisionSink bytes.Buffer
	var output bytes.Buffer
	ackResult, bytesWritten, decisionType, _, err := confirmLocalApproval(&decisionSink, adapter.ForCLI("custom"), adapter.DetectedEvent{
		EventType:     "permission_request",
		RiskLevel:     "high",
		PromptText:    "allow command?",
		ContextBefore: "context",
	}, localUIOptions{DecisionType: "approve"}, strings.NewReader(""), &output)
	if err != nil {
		t.Fatal(err)
	}
	if ackResult != "written" || decisionType != "approve" || bytesWritten == 0 || strings.TrimSpace(decisionSink.String()) != "approve" {
		t.Fatalf("ack=%q type=%q bytes=%d decision=%q, want approve written", ackResult, decisionType, bytesWritten, decisionSink.String())
	}
	if !strings.Contains(output.String(), "local_ui.approval_notification") || !strings.Contains(output.String(), "local_only.decision_written") {
		t.Fatalf("output = %q, want local notification and decision written events", output.String())
	}
}

func TestRegisterAgentDesktopClientPostsClientInstance(t *testing.T) {
	received := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/client-instances" {
			t.Fatalf("path = %s, want client-instances", r.URL.Path)
		}
		if got := r.Header.Get("Idempotency-Key"); got != "agent-desktop-device-1" {
			t.Fatalf("idempotency key = %q, want stable agent desktop key", got)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["client_type"] != "agent_desktop" || payload["device_id"] != "device-1" || payload["tenant_id"] != "tenant-1" {
			t.Fatalf("payload = %v, want agent desktop registration", payload)
		}
		received++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"client_instance_id":"client-1"}}`))
	}))
	defer server.Close()

	clientID, err := registerAgentDesktopClient(server.URL, "tenant-1", "device-1")
	if err != nil {
		t.Fatal(err)
	}
	if clientID != "client-1" || received != 1 {
		t.Fatalf("clientID=%q received=%d, want client-1 once", clientID, received)
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

func TestUpdateSessionStatusPostsLifecyclePayload(t *testing.T) {
	received := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/agent/session-updates" {
			t.Fatalf("path = %s, want session-updates", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token-1" {
			t.Fatalf("authorization = %q, want bearer token", got)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["device_id"] != "device-1" || payload["session_id"] != "session-1" || payload["status"] != "completed" {
			t.Fatalf("payload = %v, want completed session update", payload)
		}
		if payload["exit_code"] != float64(0) {
			t.Fatalf("exit_code = %v, want 0", payload["exit_code"])
		}
		if payload["last_output_summary"] != "fake CLI completed" {
			t.Fatalf("summary = %v, want final summary", payload["last_output_summary"])
		}
		received++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"session_id":"session-1","status":"completed"}}`))
	}))
	defer server.Close()

	exitCode := 0
	if err := updateSessionStatus(server.URL, "device-1", "token-1", "session-1", "completed", &exitCode, "fake CLI completed"); err != nil {
		t.Fatal(err)
	}
	if received != 1 {
		t.Fatalf("received = %d, want 1", received)
	}
}
