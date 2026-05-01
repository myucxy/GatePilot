package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type localHistory struct {
	Sessions     []localSessionRecord  `json:"sessions"`
	Output       []localOutputRecord   `json:"output"`
	Approvals    []localApprovalRecord `json:"approvals"`
	Decisions    []localDecisionRecord `json:"decisions"`
	LastModified string                `json:"last_modified"`
}

type localSessionRecord struct {
	SessionID           string `json:"session_id"`
	CLIType             string `json:"cli_type"`
	CommandLineRedacted string `json:"command_line_redacted"`
	WorkingDir          string `json:"working_dir"`
	WorkingDirHash      string `json:"working_dir_hash"`
	Status              string `json:"status"`
	StartedAt           string `json:"started_at"`
	EndedAt             string `json:"ended_at,omitempty"`
	LastOutputSummary   string `json:"last_output_summary"`
	PendingApprovals    int    `json:"pending_approval_count"`
	ControlAddr         string `json:"control_addr,omitempty"`
}

type localOutputRecord struct {
	SessionID       string `json:"session_id"`
	SequenceNo      int64  `json:"sequence_no"`
	StreamType      string `json:"stream_type"`
	ContentRedacted string `json:"content_redacted"`
	ContentHash     string `json:"content_hash"`
	CreatedAt       string `json:"created_at"`
}

type localApprovalRecord struct {
	ApprovalID    string `json:"approval_id"`
	SessionID     string `json:"session_id"`
	CLIType       string `json:"cli_type"`
	EventType     string `json:"event_type"`
	RiskLevel     string `json:"risk_level"`
	PromptText    string `json:"prompt_text"`
	ContextBefore string `json:"context_before"`
	Status        string `json:"status"`
	CreatedAt     string `json:"created_at"`
	DecidedAt     string `json:"decided_at,omitempty"`
}

type localDecisionRecord struct {
	ApprovalID      string `json:"approval_id"`
	SessionID       string `json:"session_id"`
	DecisionType    string `json:"decision_type"`
	PayloadRedacted string `json:"payload_redacted"`
	BytesWritten    int    `json:"bytes_written"`
	Result          string `json:"result"`
	CreatedAt       string `json:"created_at"`
}

func localHistoryPath() (string, error) {
	if path := os.Getenv("GATEPILOT_AGENT_HISTORY"); path != "" {
		return path, nil
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "GatePilot", "local-history.json"), nil
}

func loadLocalHistory() (localHistory, error) {
	path, err := localHistoryPath()
	if err != nil {
		return localHistory{}, err
	}
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return localHistory{}, nil
		}
		return localHistory{}, err
	}
	var history localHistory
	if err := json.Unmarshal(body, &history); err != nil {
		return localHistory{}, err
	}
	return history, nil
}

func saveLocalHistory(history localHistory) error {
	path, err := localHistoryPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	history.LastModified = time.Now().UTC().Format(time.RFC3339)
	body, err := json.MarshalIndent(history, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, body, 0600)
}

func upsertLocalSession(item localSessionRecord) error {
	history, err := loadLocalHistory()
	if err != nil {
		return err
	}
	replaced := false
	for i := range history.Sessions {
		if history.Sessions[i].SessionID == item.SessionID {
			history.Sessions[i] = mergeLocalSession(history.Sessions[i], item)
			replaced = true
			break
		}
	}
	if !replaced {
		history.Sessions = append(history.Sessions, item)
	}
	return saveLocalHistory(history)
}

func mergeLocalSession(existing localSessionRecord, update localSessionRecord) localSessionRecord {
	if update.CLIType != "" {
		existing.CLIType = update.CLIType
	}
	if update.CommandLineRedacted != "" {
		existing.CommandLineRedacted = update.CommandLineRedacted
	}
	if update.WorkingDir != "" {
		existing.WorkingDir = update.WorkingDir
	}
	if update.WorkingDirHash != "" {
		existing.WorkingDirHash = update.WorkingDirHash
	}
	if update.Status != "" {
		existing.Status = update.Status
	}
	if update.StartedAt != "" {
		existing.StartedAt = update.StartedAt
	}
	if update.EndedAt != "" {
		existing.EndedAt = update.EndedAt
	}
	if update.LastOutputSummary != "" {
		existing.LastOutputSummary = update.LastOutputSummary
	}
	if update.PendingApprovals >= 0 {
		existing.PendingApprovals = update.PendingApprovals
	}
	if update.ControlAddr != "" || update.Status != "" {
		existing.ControlAddr = update.ControlAddr
	}
	return existing
}

func appendLocalOutput(item localOutputRecord) error {
	history, err := loadLocalHistory()
	if err != nil {
		return err
	}
	history.Output = append(history.Output, item)
	return saveLocalHistory(history)
}

func upsertLocalApproval(item localApprovalRecord) error {
	history, err := loadLocalHistory()
	if err != nil {
		return err
	}
	replaced := false
	for i := range history.Approvals {
		if history.Approvals[i].ApprovalID == item.ApprovalID {
			if item.Status != "" {
				history.Approvals[i].Status = item.Status
			}
			if item.DecidedAt != "" {
				history.Approvals[i].DecidedAt = item.DecidedAt
			}
			replaced = true
			break
		}
	}
	if !replaced {
		history.Approvals = append(history.Approvals, item)
	}
	return saveLocalHistory(history)
}

func appendLocalDecision(item localDecisionRecord) error {
	history, err := loadLocalHistory()
	if err != nil {
		return err
	}
	history.Decisions = append(history.Decisions, item)
	return saveLocalHistory(history)
}

func listLocalSessions() ([]localSessionRecord, error) {
	history, err := loadLocalHistory()
	if err != nil {
		return nil, err
	}
	items := append([]localSessionRecord(nil), history.Sessions...)
	sort.Slice(items, func(i, j int) bool {
		return items[i].StartedAt > items[j].StartedAt
	})
	return items, nil
}

func localSessionDetail(sessionID string) (map[string]any, bool, error) {
	history, err := loadLocalHistory()
	if err != nil {
		return nil, false, err
	}
	var session localSessionRecord
	found := false
	for _, item := range history.Sessions {
		if item.SessionID == sessionID {
			session = item
			found = true
			break
		}
	}
	if !found {
		return nil, false, nil
	}
	output := []localOutputRecord{}
	for _, item := range history.Output {
		if item.SessionID == sessionID {
			output = append(output, item)
		}
	}
	approvals := []localApprovalRecord{}
	for _, item := range history.Approvals {
		if item.SessionID == sessionID {
			approvals = append(approvals, item)
		}
	}
	decisions := []localDecisionRecord{}
	for _, item := range history.Decisions {
		if item.SessionID == sessionID {
			decisions = append(decisions, item)
		}
	}
	return map[string]any{
		"session":   session,
		"output":    output,
		"approvals": approvals,
		"decisions": decisions,
	}, true, nil
}
