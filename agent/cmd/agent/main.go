package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"time"
)

const (
	version         = "0.1.0-dev"
	protocolVersion = "2026-04-01"
)

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
	case "run-fake":
		runFakeCLI()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", command)
		os.Exit(2)
	}
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
	payload := map[string]any{
		"device_id":          deviceID,
		"session_id":         sessionID,
		"cli_type":           "custom",
		"event_type":         "permission_request",
		"risk_level":         "high",
		"prompt_text":        "permission_request: allow command execution?",
		"context_before":     "GatePilot fake AI CLI",
		"suggested_actions":  []string{"approve", "reject", "reply"},
		"expires_in_seconds": 300,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// 审批检测先走 HTTP 占位链路，后续替换为 Agent WebSocket 的 approval.detected 消息。
	client := http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(serverURL+"/api/v1/agent/approvals", "application/json", bytes.NewReader(body))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		fmt.Fprintf(os.Stderr, "detect approval failed: %s\n%s\n", resp.Status, string(respBody))
		os.Exit(1)
	}
	fmt.Println(string(respBody))
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
	client := http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(serverURL+"/api/v1/agent/sessions", "application/json", bytes.NewReader(body))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		fmt.Fprintf(os.Stderr, "create session failed: %s\n%s\n", resp.Status, string(respBody))
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
	fmt.Println(string(respBody))
}

func hostname() string {
	name, err := os.Hostname()
	if err != nil || name == "" {
		return "GatePilot Agent"
	}
	return name
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
