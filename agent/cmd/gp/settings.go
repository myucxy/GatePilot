package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/myucxy/gatepilot/agent/internal/adapter"
)

type agentLocalSettings struct {
	Mode                 string         `json:"mode"`
	StartOnLogin         bool           `json:"start_on_login"`
	NotificationEnabled  bool           `json:"notification_enabled"`
	NotificationStyle    string         `json:"notification_style"`
	HistoryRetentionDays int            `json:"history_retention_days"`
	CaptureOutputMode    string         `json:"capture_output_mode"`
	DefaultCLIType       string         `json:"default_cli_type"`
	ServerURL            string         `json:"server_url"`
	TenantID             string         `json:"tenant_id"`
	DeviceID             string         `json:"device_id"`
	ClientInstanceID     string         `json:"client_instance_id"`
	AITools              []aiToolConfig `json:"ai_tools"`
}

type aiToolConfig struct {
	ToolID                  string `json:"tool_id"`
	ToolType                string `json:"tool_type"`
	DisplayName             string `json:"display_name"`
	Enabled                 bool   `json:"enabled"`
	HomeDir                 string `json:"home_dir"`
	HistoryPath             string `json:"history_path"`
	SessionsDir             string `json:"sessions_dir"`
	ExecutablePath          string `json:"executable_path"`
	ContinueCommandTemplate string `json:"continue_command_template"`
}

func defaultAgentLocalSettings() agentLocalSettings {
	return agentLocalSettings{
		Mode:                 "offline",
		NotificationEnabled:  true,
		NotificationStyle:    "mini_window",
		HistoryRetentionDays: 30,
		CaptureOutputMode:    "summary_only",
		DefaultCLIType:       "custom",
		AITools:              defaultAIToolConfigs(),
	}
}

func agentSettingsPath() (string, error) {
	if path := os.Getenv("GATEPILOT_AGENT_SETTINGS"); path != "" {
		return path, nil
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "GatePilot", "settings.json"), nil
}

func loadAgentLocalSettings() (agentLocalSettings, error) {
	path, err := agentSettingsPath()
	if err != nil {
		return agentLocalSettings{}, err
	}
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return defaultAgentLocalSettings(), nil
		}
		return agentLocalSettings{}, err
	}
	settings := defaultAgentLocalSettings()
	if err := json.Unmarshal(body, &settings); err != nil {
		return agentLocalSettings{}, err
	}
	if settings.HistoryRetentionDays <= 0 {
		settings.HistoryRetentionDays = 30
	}
	if settings.CaptureOutputMode == "" {
		settings.CaptureOutputMode = "summary_only"
	}
	settings.AITools = normalizeAIToolConfigs(settings.AITools)
	if len(settings.AITools) == 0 {
		settings.AITools = defaultAIToolConfigs()
	}
	return settings, nil
}

func configuredExecutable(toolType string) string {
	executable := toolType
	settings, err := loadAgentLocalSettings()
	if err == nil {
		for _, cfg := range configuredAITools(settings) {
			if cfg.ToolType == toolType && strings.TrimSpace(cfg.ExecutablePath) != "" {
				return cfg.ExecutablePath
			}
		}
	}
	return executable
}

func defaultAIToolConfigs() []aiToolConfig {
	home, _ := os.UserHomeDir()
	configs := []aiToolConfig{}
	if home != "" {
		configs = append(configs,
			aiToolConfig{
				ToolID:                  "codex",
				ToolType:                "codex",
				DisplayName:             "Codex",
				Enabled:                 true,
				HomeDir:                 filepath.Join(home, ".codex"),
				HistoryPath:             filepath.Join(home, ".codex", "history.jsonl"),
				SessionsDir:             filepath.Join(home, ".codex", "sessions"),
				ExecutablePath:          "codex",
				ContinueCommandTemplate: "codex resume {session_id}",
			},
			aiToolConfig{
				ToolID:                  "claude",
				ToolType:                "claude",
				DisplayName:             "Claude Code",
				Enabled:                 true,
				HomeDir:                 filepath.Join(home, ".claude"),
				HistoryPath:             filepath.Join(home, ".claude", "history.jsonl"),
				SessionsDir:             filepath.Join(home, ".claude", "sessions"),
				ExecutablePath:          "claude",
				ContinueCommandTemplate: "claude --resume {session_id}",
			},
		)
	}
	return normalizeAIToolConfigs(configs)
}

func configuredAITools(settings agentLocalSettings) []aiToolConfig {
	items := []aiToolConfig{}
	for _, cfg := range normalizeAIToolConfigs(settings.AITools) {
		if cfg.Enabled {
			items = append(items, cfg)
		}
	}
	return items
}

func normalizeAIToolConfigs(configs []aiToolConfig) []aiToolConfig {
	normalized := []aiToolConfig{}
	seen := map[string]bool{}
	for _, cfg := range configs {
		cfg.ToolType = normalizeAIToolType(cfg.ToolType)
		if cfg.ToolType == "" {
			cfg.ToolType = normalizeAIToolType(cfg.ToolID)
		}
		if cfg.ToolType == "" {
			continue
		}
		cfg.ToolID = strings.TrimSpace(cfg.ToolID)
		if cfg.ToolID == "" {
			cfg.ToolID = cfg.ToolType
		}
		cfg.ToolID = strings.ToLower(strings.ReplaceAll(cfg.ToolID, " ", "_"))
		if seen[cfg.ToolID] {
			continue
		}
		seen[cfg.ToolID] = true
		cfg.HomeDir = cleanOptionalPath(cfg.HomeDir)
		cfg.HistoryPath = cleanOptionalPath(cfg.HistoryPath)
		cfg.SessionsDir = cleanOptionalPath(cfg.SessionsDir)
		cfg.ExecutablePath = strings.TrimSpace(cfg.ExecutablePath)
		cfg.ContinueCommandTemplate = strings.TrimSpace(cfg.ContinueCommandTemplate)
		normalized = append(normalized, cfg)
	}
	return normalized
}

func normalizeAIToolType(value string) string {
	switch adapter.NormalizeCLIType(value) {
	case "codex":
		return "codex"
	case "claude_code":
		return "claude"
	default:
		return strings.TrimSpace(strings.ToLower(value))
	}
}

func cleanOptionalPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if strings.HasPrefix(path, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(path, "~"), string(filepath.Separator)))
		}
	}
	return filepath.Clean(path)
}
