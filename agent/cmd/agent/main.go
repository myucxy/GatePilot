package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/myucxy/gatepilot/agent/internal/adapter"
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
	case "connect":
		connectAgent(os.Args[2:])
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

	fmt.Println("GatePilot fake AI CLI")
	fmt.Println("permission_request: allow command execution? [approve/reject/reply]")
	detected := cliAdapter.Detect(adapter.TerminalSnapshot{
		SessionID:   "",
		SequenceNo:  1,
		VisibleText: "GatePilot fake AI CLI\npermission_request: allow command execution? [approve/reject/reply]",
		CursorLine:  "permission_request: allow command execution? [approve/reject/reply]",
		RecentLines: []string{"GatePilot fake AI CLI", "permission_request: allow command execution? [approve/reject/reply]"},
	})
	if len(detected) == 0 {
		fmt.Fprintln(os.Stderr, "managed CLI prompt was not detected")
		os.Exit(1)
	}
	event := detected[0]

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
	outputText := "GatePilot fake AI CLI\npermission_request: allow command execution? [approve/reject/reply]"
	if err := appendOutputChunk(config.ServerURL, config.DeviceID, config.DeviceToken, sessionID, 1, "stdout", outputText); err != nil {
		fmt.Fprintf(os.Stderr, "append managed output failed: %v\n", err)
		os.Exit(1)
	}

	approvalBody := mustMarshal(map[string]any{
		"device_id":          config.DeviceID,
		"session_id":         sessionID,
		"cli_type":           cliType,
		"event_type":         event.EventType,
		"risk_level":         event.RiskLevel,
		"prompt_text":        event.PromptText,
		"context_before":     event.ContextBefore,
		"idempotency_key":    approvalIdempotencyKey(config.DeviceID, sessionID, cliType, event.PromptText, event.ContextBefore),
		"suggested_actions":  event.SuggestedActions,
		"expires_in_seconds": 300,
	})
	approvalResp, err := postJSONWithToken(config.ServerURL+"/api/v1/agent/approvals", approvalBody, config.DeviceToken)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create managed approval failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(mustJSON(map[string]any{
		"session_id":  sessionID,
		"approval_id": responseDataString(approvalResp, "approval_id"),
		"status":      "waiting_decision",
	}))
	connectAgent([]string{"--device-id", config.DeviceID, "--wait-delivery"})
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

	if err := conn.WriteJSON(newWSEnvelope("agent.heartbeat", traceID, map[string]any{
		"active_sessions":   0,
		"local_queue_depth": 0,
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
			"local_queue_depth": 0,
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
			Type:    delivery.DecisionType,
			Payload: stringValue(delivery.Payload),
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		ackPayload := map[string]any{
			"delivery_id": delivery.DeliveryID,
			"approval_id": delivery.ApprovalID,
			"session_id":  delivery.SessionID,
			"ack_result":  "written",
			"detail": map[string]any{
				"source":        "agent-websocket",
				"decision_type": delivery.DecisionType,
				"bytes_written": len(decisionInput),
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
			"ack_result":  "written",
		}))
		return
	}
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
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
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
