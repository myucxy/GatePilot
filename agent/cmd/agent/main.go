package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/myucxy/gatepilot/agent/internal/adapter"
	"github.com/myucxy/gatepilot/agent/internal/localqueue"
)

const (
	version         = "0.1.0-dev"
	protocolVersion = "2026-04-01"
)

type agentConfig struct {
	DeviceID    string `json:"device_id"`
	DeviceToken string `json:"device_token"`
	ServerURL   string `json:"server_url"`
	ServerWSURL string `json:"server_ws_url"`
}

var deliveryDecisionWriter io.Writer = io.Discard

func main() {
	command := "version"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}

	// M0 只保留稳定命令入口，后续 register/run/daemon 会在这里扩展子命令。
	switch command {
	case "version":
		printVersion()
	case "register":
		registerDevice(os.Args[2:])
	case "create-session":
		createSession(os.Args[2:])
	case "detect-approval":
		detectApproval(os.Args[2:])
	case "ack-decision":
		ackDecision(os.Args[2:])
	case "supersede-approval":
		supersedeApproval(os.Args[2:])
	case "connect":
		connectAgent(os.Args[2:])
	case "local-ui":
		runLocalUI(os.Args[2:])
	case "flush-queue":
		flushQueue(os.Args[2:])
	case "run":
		runManagedCLI(os.Args[2:])
	case "run-fake":
		runFakeCLI()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", command)
		os.Exit(2)
	}
}

func runManagedCLI(args []string) {
	config, err := loadAgentConfig()
	if err != nil || config.DeviceID == "" {
		fmt.Fprintln(os.Stderr, "agent is not registered; run register first or set GATEPILOT_AGENT_CONFIG")
		os.Exit(2)
	}

	cliType := "custom"
	commandLine := "gatepilot fake"
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--cli-type":
			if i+1 < len(args) {
				cliType = args[i+1]
				i++
			}
		case "--":
			if i+1 < len(args) {
				commandLine = strings.Join(args[i+1:], " ")
			}
			i = len(args)
		}
	}
	cliType = adapter.NormalizeCLIType(cliType)
	cliAdapter := adapter.ForCLI(cliType)

	cmd := exec.Command(os.Args[0], "run-fake")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	event, outputText, err := detectApprovalFromReader(stdout, cliAdapter)
	if err != nil {
		_ = cmd.Process.Kill()
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	sessionBody := mustMarshal(map[string]any{
		"device_id":             config.DeviceID,
		"cli_type":              cliType,
		"command_line_redacted": commandLine,
		"working_dir_hash":      "sha256:local",
		"last_output_summary":   "fake CLI session started",
	})
	sessionResp, err := postJSONWithToken(config.ServerURL+"/api/v1/agent/sessions", sessionBody, config.DeviceToken)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create managed session failed: %v\n", err)
		os.Exit(1)
	}
	sessionID := responseDataString(sessionResp, "session_id")
	if err := appendOutputChunk(config.ServerURL, config.DeviceID, config.DeviceToken, sessionID, 1, "stdout", outputText); err != nil {
		fmt.Fprintf(os.Stderr, "append managed output failed: %v\n", err)
		os.Exit(1)
	}

	queuedEvent := localqueue.ApprovalEvent{
		EventID:          "evt_" + fmt.Sprintf("%d", time.Now().UnixNano()),
		DeviceID:         config.DeviceID,
		SessionID:        sessionID,
		CLIType:          cliType,
		EventType:        event.EventType,
		RiskLevel:        event.RiskLevel,
		PromptText:       event.PromptText,
		ContextBefore:    event.ContextBefore,
		IdempotencyKey:   approvalIdempotencyKey(config.DeviceID, sessionID, cliType, event.PromptText, event.ContextBefore),
		SuggestedActions: event.SuggestedActions,
		ExpiresInSeconds: 300,
		CreatedAt:        time.Now().UTC(),
	}
	approvalResp, err := postQueuedApproval(config.ServerURL, config.DeviceToken, queuedEvent)
	if err != nil {
		if queueErr := enqueueApprovalForRetry(queuedEvent); queueErr != nil {
			fmt.Fprintf(os.Stderr, "create managed approval failed: %v; queue failed: %v\n", err, queueErr)
			os.Exit(1)
		}
		fmt.Println(mustJSON(map[string]any{
			"session_id": sessionID,
			"event_id":   queuedEvent.EventID,
			"queued":     true,
		}))
		return
	}

	deliveryDecisionWriter = stdin
	defer func() {
		deliveryDecisionWriter = io.Discard
	}()
	fmt.Println(mustJSON(map[string]any{
		"session_id":  sessionID,
		"approval_id": responseDataString(approvalResp, "approval_id"),
		"status":      "waiting_decision",
	}))
	connectAgent([]string{"--device-id", config.DeviceID, "--wait-delivery"})
	_ = stdin.Close()
	if err := cmd.Wait(); err != nil {
		fmt.Fprintf(os.Stderr, "managed CLI exited after decision: %v\n", err)
		os.Exit(1)
	}
	exitCode := 0
	if err := updateSessionStatus(config.ServerURL, config.DeviceID, config.DeviceToken, sessionID, "completed", &exitCode, "fake CLI completed"); err != nil {
		fmt.Fprintf(os.Stderr, "update managed session failed: %v\n", err)
		os.Exit(1)
	}
}

func detectApprovalFromReader(reader io.Reader, cliAdapter adapter.CLIAdapter) (adapter.DetectedEvent, string, error) {
	scanner := bufio.NewScanner(reader)
	recentLines := []string{}
	var visible strings.Builder
	sequence := int64(0)
	for scanner.Scan() {
		line := scanner.Text()
		fmt.Println(line)
		if visible.Len() > 0 {
			visible.WriteString("\n")
		}
		visible.WriteString(line)
		recentLines = append(recentLines, line)
		if len(recentLines) > 8 {
			recentLines = recentLines[len(recentLines)-8:]
		}
		sequence++
		events := cliAdapter.Detect(adapter.TerminalSnapshot{
			SequenceNo:  sequence,
			VisibleText: visible.String(),
			CursorLine:  line,
			RecentLines: recentLines,
		})
		if len(events) > 0 {
			return events[0], visible.String(), nil
		}
	}
	if err := scanner.Err(); err != nil {
		return adapter.DetectedEvent{}, visible.String(), err
	}
	return adapter.DetectedEvent{}, visible.String(), fmt.Errorf("managed CLI prompt was not detected")
}

func flushQueue(args []string) {
	config, err := loadAgentConfig()
	if err != nil || config.DeviceID == "" {
		fmt.Fprintln(os.Stderr, "agent is not registered; run register first or set GATEPILOT_AGENT_CONFIG")
		os.Exit(2)
	}
	count, err := flushQueuedApprovals(config.ServerURL, config.DeviceToken)
	if err != nil {
		fmt.Fprintf(os.Stderr, "flush queue failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(mustJSON(map[string]any{
		"flushed": count,
	}))
}

func connectAgent(args []string) {
	deviceID := ""
	once := false
	waitDelivery := false
	readyFile := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--device-id":
			if i+1 < len(args) {
				deviceID = args[i+1]
				i++
			}
		case "--once":
			once = true
		case "--wait-delivery":
			waitDelivery = true
		case "--ready-file":
			if i+1 < len(args) {
				readyFile = args[i+1]
				i++
			}
		}
	}
	if deviceID == "" {
		fmt.Fprintln(os.Stderr, "missing --device-id")
		os.Exit(2)
	}

	wsURL := getenv("GATEPILOT_SERVER_WS_URL", "")
	if wsURL == "" {
		if config, err := loadAgentConfig(); err == nil && config.ServerWSURL != "" && config.DeviceID == deviceID {
			wsURL = config.ServerWSURL
		}
	}
	if wsURL == "" {
		serverURL := getenv("GATEPILOT_SERVER_URL", "http://127.0.0.1:8080")
		wsURL = httpURLToWS(serverURL) + "/ws/agent"
	}

	headers := http.Header{}
	if token := deviceTokenFor(deviceID); token != "" {
		headers.Set("Authorization", "Bearer "+token)
	}
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer conn.Close()

	traceID := "tr_agent_" + fmt.Sprintf("%d", time.Now().UnixNano())
	if err := conn.WriteJSON(newWSEnvelope("agent.hello", traceID, map[string]any{
		"device_id":        deviceID,
		"agent_version":    version,
		"protocol_version": protocolVersion,
		"platform":         runtime.GOOS,
		"capabilities": map[string]any{
			"pty":         runtime.GOOS != "windows",
			"conpty":      runtime.GOOS == "windows",
			"local_store": "sqlite",
		},
	})); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	var connected map[string]any
	if err := conn.ReadJSON(&connected); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if !waitDelivery {
		fmt.Println(mustJSON(connected))
	}
	if readyFile != "" {
		if err := os.WriteFile(readyFile, []byte("ready"), 0600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}

	if serverURL := serverURLForDevice(deviceID); serverURL != "" {
		if _, err := flushQueuedApprovals(serverURL, deviceTokenFor(deviceID)); err != nil {
			fmt.Fprintf(os.Stderr, "queue flush warning: %v\n", err)
		}
	}

	if err := conn.WriteJSON(newWSEnvelope("agent.heartbeat", traceID, map[string]any{
		"active_sessions":   0,
		"local_queue_depth": approvalQueueDepth(),
		"last_error":        nil,
	})); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if once {
		return
	}
	if waitDelivery {
		waitForDelivery(conn, traceID)
		return
	}

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		if err := conn.WriteJSON(newWSEnvelope("agent.heartbeat", traceID, map[string]any{
			"active_sessions":   0,
			"local_queue_depth": approvalQueueDepth(),
			"last_error":        nil,
		})); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}

func waitForDelivery(conn *websocket.Conn, traceID string) {
	for {
		var msg struct {
			Type    string          `json:"type"`
			Payload json.RawMessage `json:"payload"`
		}
		if err := conn.ReadJSON(&msg); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if msg.Type != "approval.decision.deliver" {
			continue
		}

		var delivery struct {
			DeliveryID   string  `json:"delivery_id"`
			ApprovalID   string  `json:"approval_id"`
			SessionID    string  `json:"session_id"`
			DecisionType string  `json:"decision_type"`
			Payload      *string `json:"payload"`
		}
		if err := json.Unmarshal(msg.Payload, &delivery); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		decisionInput, err := adapter.ForCLI("custom").BuildDecisionInput(adapter.ApprovalEvent{}, adapter.Decision{
			Type:    deliveryInputType(delivery.DecisionType),
			Payload: stringValue(delivery.Payload),
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		bytesWritten := len(decisionInput)
		ackResult := "written"
		if deliveryDecisionWriter != nil {
			n, err := deliveryDecisionWriter.Write(decisionInput)
			bytesWritten = n
			if err != nil {
				ackResult = "write_failed"
			}
		}
		ackPayload := map[string]any{
			"delivery_id": delivery.DeliveryID,
			"approval_id": delivery.ApprovalID,
			"session_id":  delivery.SessionID,
			"ack_result":  ackResult,
			"detail": map[string]any{
				"source":        "agent-websocket",
				"decision_type": delivery.DecisionType,
				"bytes_written": bytesWritten,
			},
		}
		if err := conn.WriteJSON(newWSEnvelope("approval.decision.ack", traceID, ackPayload)); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(mustJSON(map[string]any{
			"delivery_id": delivery.DeliveryID,
			"approval_id": delivery.ApprovalID,
			"session_id":  delivery.SessionID,
			"ack_result":  ackResult,
		}))
		return
	}
}

func deliveryInputType(decisionType string) string {
	switch decisionType {
	case "policy_approve":
		return "approve"
	case "policy_reject", "timeout_reject":
		return "reject"
	default:
		return decisionType
	}
}

type localUIOptions struct {
	DeviceID         string
	TenantID         string
	ClientInstanceID string
	DecisionType     string
	Payload          string
	Once             bool
	ReadyFile        string
	TimeoutSeconds   int
}

type localApproval struct {
	ApprovalID    string `json:"approval_id"`
	TenantID      string `json:"tenant_id"`
	DeviceID      string `json:"device_id"`
	SessionID     string `json:"session_id"`
	CLIType       string `json:"cli_type"`
	EventType     string `json:"event_type"`
	RiskLevel     string `json:"risk_level"`
	PromptText    string `json:"prompt_text"`
	ContextBefore string `json:"context_before"`
	ExpiresAt     string `json:"expires_at"`
}

type localClientEvent struct {
	Type       string
	ApprovalID string
}

func runLocalUI(args []string) {
	options := parseLocalUIOptions(args)
	config, _ := loadAgentConfig()
	if options.DeviceID == "" {
		options.DeviceID = config.DeviceID
	}
	if options.DeviceID == "" {
		fmt.Fprintln(os.Stderr, "missing --device-id and no registered agent config found")
		os.Exit(2)
	}
	serverURL := getenv("GATEPILOT_SERVER_URL", config.ServerURL)
	if serverURL == "" {
		serverURL = "http://127.0.0.1:8080"
	}
	deviceToken := deviceTokenFor(options.DeviceID)
	if options.TenantID == "" {
		tenantID, err := tenantIDForDevice(serverURL, options.DeviceID, deviceToken)
		if err != nil {
			fmt.Fprintf(os.Stderr, "resolve tenant failed: %v\n", err)
			os.Exit(1)
		}
		options.TenantID = tenantID
	}
	if options.ClientInstanceID == "" {
		clientID, err := registerAgentDesktopClient(serverURL, options.TenantID, options.DeviceID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "register local UI failed: %v\n", err)
			os.Exit(1)
		}
		options.ClientInstanceID = clientID
	}
	localEvents, closeLocalEvents, err := startLocalClientNotifications(serverURL, options.TenantID, options.ClientInstanceID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "local notification websocket warning: %v\n", err)
	} else {
		defer closeLocalEvents()
	}
	if options.ReadyFile != "" {
		if err := os.WriteFile(options.ReadyFile, []byte("ready"), 0600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	fmt.Println(mustJSON(map[string]any{
		"type":               "local_ui.connected",
		"tenant_id":          options.TenantID,
		"device_id":          options.DeviceID,
		"client_instance_id": options.ClientInstanceID,
		"client_type":        "agent_desktop",
	}))

	seen := map[string]bool{}
	for {
		approval, err := waitForLocalApproval(serverURL, options.TenantID, options.DeviceID, deviceToken, seen, options.TimeoutSeconds, localEvents)
		if err != nil {
			fmt.Fprintf(os.Stderr, "wait local approval failed: %v\n", err)
			os.Exit(1)
		}
		seen[approval.ApprovalID] = true
		notifyLocalApproval(os.Stdout, approval)
		decisionType, payload, err := localDecisionInput(options, os.Stdin, os.Stdout)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		respBody, err := submitLocalApprovalDecision(serverURL, approval.ApprovalID, options.ClientInstanceID, decisionType, payload)
		if err != nil {
			fmt.Fprintf(os.Stderr, "submit local decision failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(mustJSON(map[string]any{
			"type":               "local_ui.decision_submitted",
			"approval_id":        approval.ApprovalID,
			"session_id":         approval.SessionID,
			"decision_type":      decisionType,
			"client_instance_id": options.ClientInstanceID,
			"client_type":        "agent_desktop",
			"delivery_id":        responseDataString(respBody, "delivery_id"),
		}))
		if options.Once {
			return
		}
	}
}

func parseLocalUIOptions(args []string) localUIOptions {
	options := localUIOptions{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--device-id":
			if i+1 < len(args) {
				options.DeviceID = args[i+1]
				i++
			}
		case "--tenant-id":
			if i+1 < len(args) {
				options.TenantID = args[i+1]
				i++
			}
		case "--client-instance-id":
			if i+1 < len(args) {
				options.ClientInstanceID = args[i+1]
				i++
			}
		case "--decision":
			if i+1 < len(args) {
				options.DecisionType = args[i+1]
				i++
			}
		case "--payload":
			if i+1 < len(args) {
				options.Payload = args[i+1]
				i++
			}
		case "--once", "--confirm-once":
			options.Once = true
		case "--ready-file":
			if i+1 < len(args) {
				options.ReadyFile = args[i+1]
				i++
			}
		case "--timeout-seconds":
			if i+1 < len(args) {
				fmt.Sscanf(args[i+1], "%d", &options.TimeoutSeconds)
				i++
			}
		}
	}
	return options
}

func tenantIDForDevice(serverURL string, deviceID string, token string) (string, error) {
	body, err := getJSONWithToken(serverURL+"/api/v1/devices/"+url.PathEscape(deviceID), token)
	if err != nil {
		return "", err
	}
	return responseDataString(body, "tenant_id"), nil
}

func registerAgentDesktopClient(serverURL string, tenantID string, deviceID string) (string, error) {
	body := mustMarshal(map[string]any{
		"tenant_id":    tenantID,
		"client_type":  "agent_desktop",
		"device_id":    deviceID,
		"display_name": hostname() + " Agent",
		"app_version":  version,
		"platform":     runtime.GOOS,
	})
	respBody, err := postJSONWithHeaders(serverURL+"/api/v1/client-instances", body, "", map[string]string{
		"Idempotency-Key": "agent-desktop-" + deviceID,
	})
	if err != nil {
		return "", err
	}
	clientID := responseDataString(respBody, "client_instance_id")
	if clientID == "" {
		return "", fmt.Errorf("client_instance_id missing in response")
	}
	return clientID, nil
}

func startLocalClientNotifications(serverURL string, tenantID string, clientInstanceID string) (<-chan localClientEvent, func(), error) {
	wsURL := httpURLToWS(serverURL) + "/ws/client?tenant_id=" + url.QueryEscape(tenantID) + "&client_instance_id=" + url.QueryEscape(clientInstanceID)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		return nil, nil, err
	}
	events := make(chan localClientEvent, 8)
	go func() {
		defer close(events)
		defer conn.Close()
		for {
			var msg struct {
				Type    string          `json:"type"`
				Payload json.RawMessage `json:"payload"`
			}
			if err := conn.ReadJSON(&msg); err != nil {
				return
			}
			event := localClientEvent{Type: msg.Type}
			var payload struct {
				ApprovalID string `json:"approval_id"`
			}
			if err := json.Unmarshal(msg.Payload, &payload); err == nil {
				event.ApprovalID = payload.ApprovalID
			}
			select {
			case events <- event:
			default:
			}
		}
	}()
	return events, func() { _ = conn.Close() }, nil
}

func waitForLocalApproval(serverURL string, tenantID string, deviceID string, token string, seen map[string]bool, timeoutSeconds int, events <-chan localClientEvent) (localApproval, error) {
	deadline := time.Time{}
	if timeoutSeconds > 0 {
		deadline = time.Now().Add(time.Duration(timeoutSeconds) * time.Second)
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		items, err := listWaitingLocalApprovals(serverURL, tenantID, deviceID, token)
		if err != nil {
			return localApproval{}, err
		}
		for _, item := range items {
			if !seen[item.ApprovalID] {
				return item, nil
			}
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			return localApproval{}, fmt.Errorf("timed out waiting for local approval")
		}
		select {
		case _, ok := <-events:
			if !ok {
				events = nil
			}
		case <-ticker.C:
		}
	}
}

func listWaitingLocalApprovals(serverURL string, tenantID string, deviceID string, token string) ([]localApproval, error) {
	body, err := getJSONWithToken(serverURL+"/api/v1/tenants/"+url.PathEscape(tenantID)+"/approvals?status=waiting_decision", token)
	if err != nil {
		return nil, err
	}
	var response struct {
		Data struct {
			Items []localApproval `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, err
	}
	filtered := []localApproval{}
	for _, item := range response.Data.Items {
		if item.DeviceID == deviceID {
			filtered = append(filtered, item)
		}
	}
	return filtered, nil
}

func notifyLocalApproval(writer io.Writer, approval localApproval) {
	fmt.Fprintln(writer, mustJSON(map[string]any{
		"type":           "local_ui.approval_notification",
		"approval_id":    approval.ApprovalID,
		"session_id":     approval.SessionID,
		"device_id":      approval.DeviceID,
		"risk_level":     approval.RiskLevel,
		"event_type":     approval.EventType,
		"prompt_text":    approval.PromptText,
		"context_before": approval.ContextBefore,
		"expires_at":     approval.ExpiresAt,
	}))
}

func localDecisionInput(options localUIOptions, reader io.Reader, writer io.Writer) (string, string, error) {
	decisionType := strings.TrimSpace(options.DecisionType)
	if decisionType == "" {
		fmt.Fprint(writer, "Decision [approve/reject/reply]: ")
		line, err := readDecisionLine(reader)
		if err != nil {
			return "", "", err
		}
		decisionType = line
	}
	switch decisionType {
	case "approve", "reject", "reply":
	default:
		return "", "", fmt.Errorf("unsupported local decision %q", decisionType)
	}
	return decisionType, options.Payload, nil
}

func submitLocalApprovalDecision(serverURL string, approvalID string, clientInstanceID string, decisionType string, payload string) ([]byte, error) {
	body := mustMarshal(map[string]any{
		"decision_type": decisionType,
		"payload":       payload,
	})
	return postJSONWithHeaders(serverURL+"/api/v1/approvals/"+url.PathEscape(approvalID)+"/decision", body, "", map[string]string{
		"Idempotency-Key":      "agent-local-" + approvalID + "-" + fmt.Sprintf("%d", time.Now().UnixNano()),
		"X-Client-Instance-Id": clientInstanceID,
	})
}

func ackDecision(args []string) {
	approvalID := ""
	deliveryID := ""
	sessionID := ""
	ackResult := "written"
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--approval-id":
			if i+1 < len(args) {
				approvalID = args[i+1]
				i++
			}
		case "--delivery-id":
			if i+1 < len(args) {
				deliveryID = args[i+1]
				i++
			}
		case "--session-id":
			if i+1 < len(args) {
				sessionID = args[i+1]
				i++
			}
		case "--ack-result":
			if i+1 < len(args) {
				ackResult = args[i+1]
				i++
			}
		}
	}
	if approvalID == "" || deliveryID == "" || sessionID == "" {
		fmt.Fprintln(os.Stderr, "missing --approval-id, --delivery-id or --session-id")
		os.Exit(2)
	}

	serverURL := getenv("GATEPILOT_SERVER_URL", "http://127.0.0.1:8080")
	payload := map[string]any{
		"approval_id": approvalID,
		"delivery_id": deliveryID,
		"session_id":  sessionID,
		"ack_result":  ackResult,
		"detail": map[string]any{
			"source": "fake-cli",
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// 决策 ACK 先走 HTTP 占位链路，后续替换为 Agent WebSocket 的 approval.decision.ack 消息。
	respBody, err := postJSONWithToken(serverURL+"/api/v1/agent/approval-acks", body, deviceTokenFor(""))
	if err != nil {
		fmt.Fprintf(os.Stderr, "ack decision failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(respBody))
}

func supersedeApproval(args []string) {
	approvalID := ""
	sessionID := ""
	reason := "local_input"
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--approval-id":
			if i+1 < len(args) {
				approvalID = args[i+1]
				i++
			}
		case "--session-id":
			if i+1 < len(args) {
				sessionID = args[i+1]
				i++
			}
		case "--reason":
			if i+1 < len(args) {
				reason = args[i+1]
				i++
			}
		}
	}
	if approvalID == "" || sessionID == "" {
		fmt.Fprintln(os.Stderr, "missing --approval-id or --session-id")
		os.Exit(2)
	}

	serverURL := getenv("GATEPILOT_SERVER_URL", "http://127.0.0.1:8080")
	payload := map[string]any{
		"approval_id": approvalID,
		"session_id":  sessionID,
		"reason":      reason,
		"detail": map[string]any{
			"source": "local-terminal",
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	respBody, err := postJSONWithToken(serverURL+"/api/v1/agent/approval-supersedes", body, deviceTokenFor(""))
	if err != nil {
		fmt.Fprintf(os.Stderr, "supersede approval failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(respBody))
}

func printVersion() {
	fmt.Printf("GatePilot Agent %s\n", version)
	fmt.Printf("Protocol %s\n", protocolVersion)
	fmt.Printf("Runtime %s/%s\n", runtime.GOOS, runtime.GOARCH)
}

func runFakeCLI() {
	// fake CLI 用于端到端联调，不依赖真实 AI CLI，输出必须和 testdata 样本保持一致。
	fmt.Println("GatePilot fake AI CLI")
	fmt.Println("permission_request: allow command execution? [approve/reject/reply]")
	fmt.Println("waiting_for_input")
	decision, err := readDecisionLine(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if decision == "" {
		fmt.Fprintln(os.Stderr, "empty fake CLI decision")
		os.Exit(1)
	}
	fmt.Printf("received_decision: %s\n", decision)
}

func readDecisionLine(reader io.Reader) (string, error) {
	buffered := bufio.NewReader(reader)
	var input strings.Builder
	for {
		b, err := buffered.ReadByte()
		if err != nil {
			if input.Len() > 0 {
				break
			}
			return "", err
		}
		if b == '\r' || b == '\n' {
			break
		}
		input.WriteByte(b)
	}
	return strings.TrimSpace(input.String()), nil
}

func detectApproval(args []string) {
	deviceID := ""
	sessionID := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--device-id":
			if i+1 < len(args) {
				deviceID = args[i+1]
				i++
			}
		case "--session-id":
			if i+1 < len(args) {
				sessionID = args[i+1]
				i++
			}
		}
	}
	if deviceID == "" || sessionID == "" {
		fmt.Fprintln(os.Stderr, "missing --device-id or --session-id")
		os.Exit(2)
	}

	serverURL := getenv("GATEPILOT_SERVER_URL", "http://127.0.0.1:8080")
	cliType := adapter.NormalizeCLIType("custom")
	cliAdapter := adapter.ForCLI(cliType)
	detected := cliAdapter.Detect(adapter.TerminalSnapshot{
		SessionID:   sessionID,
		SequenceNo:  1,
		VisibleText: "GatePilot fake AI CLI\npermission_request: allow command execution? [approve/reject/reply]\nwaiting_for_input",
		CursorLine:  "waiting_for_input",
		RecentLines: []string{
			"GatePilot fake AI CLI",
			"permission_request: allow command execution? [approve/reject/reply]",
			"waiting_for_input",
		},
	})
	if len(detected) == 0 {
		fmt.Fprintln(os.Stderr, "fake approval prompt was not detected")
		os.Exit(1)
	}
	event := detected[0]
	payload := map[string]any{
		"device_id":          deviceID,
		"session_id":         sessionID,
		"cli_type":           cliType,
		"event_type":         event.EventType,
		"risk_level":         event.RiskLevel,
		"prompt_text":        event.PromptText,
		"context_before":     event.ContextBefore,
		"idempotency_key":    approvalIdempotencyKey(deviceID, sessionID, cliType, event.PromptText, event.ContextBefore),
		"suggested_actions":  event.SuggestedActions,
		"expires_in_seconds": 300,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// 审批检测先走 HTTP 占位链路，后续替换为 Agent WebSocket 的 approval.detected 消息。
	respBody, err := postJSONWithToken(serverURL+"/api/v1/agent/approvals", body, deviceTokenFor(deviceID))
	if err != nil {
		fmt.Fprintf(os.Stderr, "detect approval failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(respBody))
}

func approvalIdempotencyKey(deviceID, sessionID, cliType, promptText, contextBefore string) string {
	stableInputs := struct {
		DeviceID      string `json:"device_id"`
		SessionID     string `json:"session_id"`
		CliType       string `json:"cli_type"`
		PromptText    string `json:"prompt_text"`
		ContextBefore string `json:"context_before"`
	}{
		DeviceID:      deviceID,
		SessionID:     sessionID,
		CliType:       cliType,
		PromptText:    promptText,
		ContextBefore: contextBefore,
	}
	body, err := json.Marshal(stableInputs)
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func createSession(args []string) {
	deviceID := ""
	cliType := "custom"
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--device-id":
			if i+1 < len(args) {
				deviceID = args[i+1]
				i++
			}
		case "--cli-type":
			if i+1 < len(args) {
				cliType = args[i+1]
				i++
			}
		}
	}
	if deviceID == "" {
		fmt.Fprintln(os.Stderr, "missing --device-id")
		os.Exit(2)
	}

	serverURL := getenv("GATEPILOT_SERVER_URL", "http://127.0.0.1:8080")
	payload := map[string]any{
		"device_id":             deviceID,
		"cli_type":              cliType,
		"command_line_redacted": "gatepilot fake",
		"working_dir_hash":      "sha256:local",
		"last_output_summary":   "fake CLI session started",
	}

	body, err := json.Marshal(payload)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// 会话创建先走 HTTP 占位链路，后续替换为 Agent WebSocket 的 session.created 消息。
	respBody, err := postJSONWithToken(serverURL+"/api/v1/agent/sessions", body, deviceTokenFor(deviceID))
	if err != nil {
		fmt.Fprintf(os.Stderr, "create session failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(respBody))
}

func registerDevice(args []string) {
	code := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--activation-code" && i+1 < len(args) {
			code = args[i+1]
			i++
		}
	}
	if code == "" {
		fmt.Fprintln(os.Stderr, "missing --activation-code")
		os.Exit(2)
	}

	serverURL := getenv("GATEPILOT_SERVER_URL", "http://127.0.0.1:8080")
	payload := map[string]any{
		"activation_code":  code,
		"device_name":      hostname(),
		"platform":         runtime.GOOS,
		"arch":             runtime.GOARCH,
		"agent_version":    version,
		"protocol_version": protocolVersion,
		"capabilities": map[string]any{
			"pty":         runtime.GOOS != "windows",
			"conpty":      runtime.GOOS == "windows",
			"local_store": "sqlite",
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// 注册动作只通过服务端 API 完成，后续会把返回的 device_token 写入系统安全存储。
	client := http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(serverURL+"/api/v1/agent/register", "application/json", bytes.NewReader(body))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		fmt.Fprintf(os.Stderr, "register failed: %s\n%s\n", resp.Status, string(respBody))
		os.Exit(1)
	}
	if err := saveRegisteredConfig(serverURL, respBody); err != nil {
		fmt.Fprintf(os.Stderr, "save config failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(respBody))
}

func postJSONWithToken(url string, body []byte, token string) ([]byte, error) {
	return postJSONWithHeaders(url, body, token, nil)
}

func postJSONWithHeaders(url string, body []byte, token string, headers map[string]string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	client := http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s\n%s", resp.Status, string(respBody))
	}
	return respBody, nil
}

func getJSONWithToken(url string, token string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s\n%s", resp.Status, string(respBody))
	}
	return respBody, nil
}

func postQueuedApproval(serverURL string, token string, event localqueue.ApprovalEvent) ([]byte, error) {
	body, err := json.Marshal(approvalEventPayload(event))
	if err != nil {
		return nil, err
	}
	return postJSONWithToken(serverURL+"/api/v1/agent/approvals", body, token)
}

func approvalEventPayload(event localqueue.ApprovalEvent) map[string]any {
	expiresIn := event.ExpiresInSeconds
	if expiresIn <= 0 {
		expiresIn = 300
	}
	return map[string]any{
		"device_id":          event.DeviceID,
		"session_id":         event.SessionID,
		"cli_type":           event.CLIType,
		"event_type":         event.EventType,
		"risk_level":         event.RiskLevel,
		"prompt_text":        event.PromptText,
		"context_before":     event.ContextBefore,
		"idempotency_key":    event.IdempotencyKey,
		"suggested_actions":  event.SuggestedActions,
		"expires_in_seconds": expiresIn,
	}
}

func enqueueApprovalForRetry(event localqueue.ApprovalEvent) error {
	queue, err := approvalQueue()
	if err != nil {
		return err
	}
	return queue.EnqueueApproval(event)
}

func flushQueuedApprovals(serverURL string, token string) (int, error) {
	queue, err := approvalQueue()
	if err != nil {
		return 0, err
	}
	items, err := queue.ListApprovals()
	if err != nil {
		return 0, err
	}
	flushed := 0
	for _, item := range items {
		if _, err := postQueuedApproval(serverURL, token, item); err != nil {
			return flushed, err
		}
		if err := queue.RemoveApproval(item.EventID); err != nil {
			return flushed, err
		}
		flushed++
	}
	return flushed, nil
}

func approvalQueueDepth() int {
	queue, err := approvalQueue()
	if err != nil {
		return 0
	}
	items, err := queue.ListApprovals()
	if err != nil {
		return 0
	}
	return len(items)
}

func approvalQueue() (localqueue.Queue, error) {
	path, err := localqueue.DefaultPath()
	if err != nil {
		return localqueue.Queue{}, err
	}
	return localqueue.New(path), nil
}

func appendOutputChunk(serverURL string, deviceID string, deviceToken string, sessionID string, sequenceNo int64, streamType string, content string) error {
	payload := map[string]any{
		"device_id":        deviceID,
		"session_id":       sessionID,
		"sequence_no":      sequenceNo,
		"stream_type":      streamType,
		"content_redacted": content,
		"content_hash":     "sha256:" + sha256String(content),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = postJSONWithToken(serverURL+"/api/v1/agent/output-chunks", body, deviceToken)
	return err
}

func updateSessionStatus(serverURL string, deviceID string, deviceToken string, sessionID string, status string, exitCode *int, summary string) error {
	payload := map[string]any{
		"device_id":           deviceID,
		"session_id":          sessionID,
		"status":              status,
		"last_output_summary": summary,
	}
	if exitCode != nil {
		payload["exit_code"] = *exitCode
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = postJSONWithToken(serverURL+"/api/v1/agent/session-updates", body, deviceToken)
	return err
}

func mustMarshal(value any) []byte {
	body, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return body
}

func responseDataString(body []byte, key string) string {
	var response struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return ""
	}
	if value, ok := response.Data[key].(string); ok {
		return value
	}
	return ""
}

func saveRegisteredConfig(serverURL string, responseBody []byte) error {
	var response struct {
		Data agentConfig `json:"data"`
	}
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return err
	}
	response.Data.ServerURL = serverURL
	return saveAgentConfig(response.Data)
}

func saveAgentConfig(config agentConfig) error {
	path, err := agentConfigPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	body, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, body, 0600)
}

func loadAgentConfig() (agentConfig, error) {
	path, err := agentConfigPath()
	if err != nil {
		return agentConfig{}, err
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return agentConfig{}, err
	}
	var config agentConfig
	if err := json.Unmarshal(body, &config); err != nil {
		return agentConfig{}, err
	}
	return config, nil
}

func agentConfigPath() (string, error) {
	if path := os.Getenv("GATEPILOT_AGENT_CONFIG"); path != "" {
		return path, nil
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "GatePilot", "agent.json"), nil
}

func deviceTokenFor(deviceID string) string {
	if token := os.Getenv("GATEPILOT_DEVICE_TOKEN"); token != "" {
		return token
	}
	config, err := loadAgentConfig()
	if err != nil {
		return ""
	}
	if deviceID == "" || config.DeviceID == deviceID {
		return config.DeviceToken
	}
	return ""
}

func serverURLForDevice(deviceID string) string {
	if serverURL := os.Getenv("GATEPILOT_SERVER_URL"); serverURL != "" {
		return serverURL
	}
	config, err := loadAgentConfig()
	if err != nil {
		return ""
	}
	if deviceID == "" || config.DeviceID == deviceID {
		return config.ServerURL
	}
	return ""
}

func hostname() string {
	name, err := os.Hostname()
	if err != nil || name == "" {
		return "GatePilot Agent"
	}
	return name
}

func newWSEnvelope(messageType string, traceID string, payload map[string]any) map[string]any {
	return map[string]any{
		"type":           messageType,
		"message_id":     "msg_" + fmt.Sprintf("%d", time.Now().UnixNano()),
		"trace_id":       traceID,
		"sent_at":        time.Now().UTC().Format(time.RFC3339),
		"schema_version": protocolVersion,
		"payload":        payload,
	}
}

func httpURLToWS(url string) string {
	switch {
	case strings.HasPrefix(url, "https://"):
		return "wss://" + strings.TrimPrefix(url, "https://")
	case strings.HasPrefix(url, "http://"):
		return "ws://" + strings.TrimPrefix(url, "http://")
	default:
		return url
	}
}

func mustJSON(value any) string {
	body, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(body)
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func sha256String(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
