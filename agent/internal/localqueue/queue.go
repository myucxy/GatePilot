package localqueue

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

type ApprovalEvent struct {
	EventID          string    `json:"event_id"`
	DeviceID         string    `json:"device_id"`
	SessionID        string    `json:"session_id"`
	CLIType          string    `json:"cli_type"`
	EventType        string    `json:"event_type"`
	RiskLevel        string    `json:"risk_level"`
	PromptText       string    `json:"prompt_text"`
	ContextBefore    string    `json:"context_before"`
	IdempotencyKey   string    `json:"idempotency_key"`
	SuggestedActions []string  `json:"suggested_actions"`
	ExpiresInSeconds int       `json:"expires_in_seconds"`
	CreatedAt        time.Time `json:"created_at"`
}

type Queue struct {
	path string
}

func New(path string) Queue {
	return Queue{path: path}
}

func DefaultPath() (string, error) {
	if path := os.Getenv("GATEPILOT_AGENT_QUEUE"); path != "" {
		return path, nil
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "GatePilot", "approval-queue.jsonl"), nil
}

func (q Queue) EnqueueApproval(event ApprovalEvent) error {
	if event.EventID == "" {
		return errors.New("event_id is required")
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	existing, err := q.ListApprovals()
	if err != nil {
		return err
	}
	for _, item := range existing {
		if item.EventID == event.EventID || (item.IdempotencyKey != "" && item.IdempotencyKey == event.IdempotencyKey) {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(q.path), 0700); err != nil {
		return err
	}
	file, err := os.OpenFile(q.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	body, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if _, err := file.Write(append(body, '\n')); err != nil {
		return err
	}
	return nil
}

func (q Queue) ListApprovals() ([]ApprovalEvent, error) {
	file, err := os.Open(q.path)
	if errors.Is(err, os.ErrNotExist) {
		return []ApprovalEvent{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()

	items := []ApprovalEvent{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var item ApprovalEvent
		if err := json.Unmarshal(scanner.Bytes(), &item); err == nil && item.EventID != "" {
			items = append(items, item)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (q Queue) RemoveApproval(eventID string) error {
	items, err := q.ListApprovals()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(q.path), 0700); err != nil {
		return err
	}
	tmpPath := q.path + ".tmp"
	file, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	kept := 0
	for _, item := range items {
		if item.EventID == eventID {
			continue
		}
		body, err := json.Marshal(item)
		if err != nil {
			file.Close()
			return err
		}
		if _, err := file.Write(append(body, '\n')); err != nil {
			file.Close()
			return err
		}
		kept++
	}
	if err := file.Close(); err != nil {
		return err
	}
	if kept == 0 {
		_ = os.Remove(q.path)
		return os.Remove(tmpPath)
	}
	return os.Rename(tmpPath, q.path)
}
