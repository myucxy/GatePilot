package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type postgresStore struct {
	db *sql.DB
}

func newPostgresStore(databaseURL string) (*postgresStore, error) {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return &postgresStore{db: db}, nil
}

func (s *postgresStore) RegisterClientInstance(req registerClientInstanceRequest, userID string, idempotencyKey string, now time.Time) (clientInstance, *appError) {
	if req.TenantID == "" {
		return clientInstance{}, &appError{HTTPStatus: http.StatusBadRequest, Code: "message_schema_invalid", Message: "tenant_id is required"}
	}
	if req.ClientType == "" {
		return clientInstance{}, &appError{HTTPStatus: http.StatusBadRequest, Code: "message_schema_invalid", Message: "client_type is required"}
	}
	if req.DisplayName == "" {
		req.DisplayName = req.ClientType
	}
	if req.AppVersion == "" {
		req.AppVersion = version
	}
	if req.Platform == "" {
		req.Platform = "browser"
	}
	if idempotencyKey == "" {
		return clientInstance{}, &appError{HTTPStatus: http.StatusBadRequest, Code: "idempotency_key_required", Message: "Idempotency-Key header is required"}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return clientInstance{}, internalStoreError(err)
	}
	defer tx.Rollback()

	signature := fmt.Sprintf("%s:%s:%s:%s:%s:%s:%s", req.TenantID, userID, req.ClientType, req.DeviceID, req.DisplayName, req.AppVersion, req.Platform)
	replayScope := "client-instance:" + req.TenantID + ":" + userID
	if replay, ok, appErr := readIdempotencyReplay(ctx, tx, replayScope, idempotencyKey, signature); appErr != nil {
		return clientInstance{}, appErr
	} else if ok {
		var item clientInstance
		if err := json.Unmarshal(replay, &item); err != nil {
			return clientInstance{}, internalStoreError(err)
		}
		return item, nil
	}

	if err := ensureTenant(ctx, tx, req.TenantID); err != nil {
		return clientInstance{}, internalStoreError(err)
	}
	item := clientInstance{
		ClientInstanceID: randomUUID(),
		TenantID:         req.TenantID,
		UserID:           userID,
		ClientType:       req.ClientType,
		DeviceID:         req.DeviceID,
		DisplayName:      req.DisplayName,
		AppVersion:       req.AppVersion,
		Platform:         req.Platform,
		Status:           "active",
		LastSeenAt:       now.Format(time.RFC3339),
		CreatedAt:        now.Format(time.RFC3339),
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO client_instances(id, tenant_id, user_id, client_type, device_id, display_name, app_version, platform, status, last_seen_at, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'active', $9, $9, $9)`,
		item.ClientInstanceID, item.TenantID, item.UserID, item.ClientType, nullableUUID(item.DeviceID), item.DisplayName, item.AppVersion, item.Platform, now)
	if err != nil {
		return clientInstance{}, internalStoreError(err)
	}
	responseJSON, err := json.Marshal(item)
	if err != nil {
		return clientInstance{}, internalStoreError(err)
	}
	if err := writeIdempotencyReplay(ctx, tx, replayScope, idempotencyKey, signature, responseJSON); err != nil {
		return clientInstance{}, internalStoreError(err)
	}
	if err := tx.Commit(); err != nil {
		return clientInstance{}, internalStoreError(err)
	}
	return item, nil
}

func (s *postgresStore) RegisterPushToken(clientInstanceID string, req registerPushTokenRequest, now time.Time) (clientInstance, *appError) {
	if req.Provider == "" || req.Token == "" {
		return clientInstance{}, &appError{HTTPStatus: http.StatusBadRequest, Code: "message_schema_invalid", Message: "provider and token are required"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	row := s.db.QueryRowContext(ctx, `
UPDATE client_instances
SET push_provider = $1,
    push_token_ciphertext = $2,
    last_seen_at = $3,
    updated_at = $3
WHERE id = $4
RETURNING id::text, tenant_id::text, user_id::text, client_type, COALESCE(device_id::text, ''), display_name, app_version, platform, push_provider, status, last_seen_at, created_at`,
		req.Provider, sha256Hex(req.Token), now, clientInstanceID)
	var item clientInstance
	var lastSeenAt, createdAt time.Time
	err := row.Scan(&item.ClientInstanceID, &item.TenantID, &item.UserID, &item.ClientType, &item.DeviceID, &item.DisplayName, &item.AppVersion, &item.Platform, &item.PushProvider, &item.Status, &lastSeenAt, &createdAt)
	if err == sql.ErrNoRows {
		return clientInstance{}, &appError{HTTPStatus: http.StatusNotFound, Code: "client_instance_not_found", Message: "client instance not found"}
	}
	if err != nil {
		return clientInstance{}, internalStoreError(err)
	}
	item.LastSeenAt = lastSeenAt.Format(time.RFC3339)
	item.CreatedAt = createdAt.Format(time.RFC3339)
	return item, nil
}

func (s *postgresStore) CreateActivationCode(tenantID string, req createActivationCodeRequest, idempotencyKey string, now time.Time) (string, time.Time, *appError) {
	if req.Name == "" {
		req.Name = "New Device"
	}
	if req.ExpiresInSeconds <= 0 {
		req.ExpiresInSeconds = 600
	}
	if idempotencyKey == "" {
		return "", time.Time{}, &appError{HTTPStatus: http.StatusBadRequest, Code: "idempotency_key_required", Message: "Idempotency-Key header is required"}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", time.Time{}, internalStoreError(err)
	}
	defer tx.Rollback()

	signature := fmt.Sprintf("%s:%d", req.Name, req.ExpiresInSeconds)
	replayScope := "activation:" + tenantID
	if replay, ok, appErr := readIdempotencyReplay(ctx, tx, replayScope, idempotencyKey, signature); appErr != nil {
		return "", time.Time{}, appErr
	} else if ok {
		var body struct {
			ActivationCode string `json:"activation_code"`
			ExpiresAt      string `json:"expires_at"`
		}
		if err := json.Unmarshal(replay, &body); err != nil {
			return "", time.Time{}, internalStoreError(err)
		}
		expiresAt, err := time.Parse(time.RFC3339, body.ExpiresAt)
		if err != nil {
			return "", time.Time{}, internalStoreError(err)
		}
		return body.ActivationCode, expiresAt, nil
	}

	code := "GP-" + randomHex(3) + "-" + randomHex(3)
	expiresAt := now.Add(time.Duration(req.ExpiresInSeconds) * time.Second)

	if err := ensureTenant(ctx, tx, tenantID); err != nil {
		return "", time.Time{}, internalStoreError(err)
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO device_activation_codes(id, tenant_id, name, code_hash, status, expires_at, created_at)
VALUES ($1, $2, $3, $4, 'active', $5, $6)`,
		randomUUID(), tenantID, req.Name, sha256Hex(code), expiresAt, now)
	if err != nil {
		return "", time.Time{}, internalStoreError(err)
	}

	responseJSON, err := json.Marshal(map[string]string{
		"activation_code": code,
		"expires_at":      expiresAt.Format(time.RFC3339),
	})
	if err != nil {
		return "", time.Time{}, internalStoreError(err)
	}
	if err := writeIdempotencyReplay(ctx, tx, replayScope, idempotencyKey, signature, responseJSON); err != nil {
		return "", time.Time{}, internalStoreError(err)
	}
	if err := tx.Commit(); err != nil {
		return "", time.Time{}, internalStoreError(err)
	}
	return code, expiresAt, nil
}

func (s *postgresStore) ListDevices(tenantID string) []device {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rows, err := s.db.QueryContext(ctx, `
SELECT id::text, tenant_id::text, name, platform, arch, status, last_seen_at, created_at
FROM devices
WHERE tenant_id = $1
ORDER BY created_at DESC`, tenantID)
	if err != nil {
		return []device{}
	}
	defer rows.Close()

	items := []device{}
	for rows.Next() {
		var item device
		var lastSeen, createdAt time.Time
		if err := rows.Scan(&item.DeviceID, &item.TenantID, &item.Name, &item.Platform, &item.Arch, &item.Status, &lastSeen, &createdAt); err != nil {
			return []device{}
		}
		item.LastSeen = lastSeen.Format(time.RFC3339)
		item.CreatedAt = createdAt.Format(time.RFC3339)
		items = append(items, item)
	}
	return items
}

func (s *postgresStore) RegisterAgent(req registerAgentRequest, now time.Time) (device, string, *appError) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return device{}, "", internalStoreError(err)
	}
	defer tx.Rollback()

	var tenantID string
	var name string
	var expiresAt time.Time
	var status string
	var consumedAt sql.NullTime
	err = tx.QueryRowContext(ctx, `
SELECT tenant_id::text, name, status, consumed_at, expires_at
FROM device_activation_codes
WHERE code_hash = $1
FOR UPDATE`, sha256Hex(req.ActivationCode)).Scan(&tenantID, &name, &status, &consumedAt, &expiresAt)
	if err == sql.ErrNoRows {
		return device{}, "", &appError{HTTPStatus: http.StatusUnprocessableEntity, Code: "activation_code_invalid", Message: "activation code is invalid"}
	}
	if err != nil {
		return device{}, "", internalStoreError(err)
	}
	if status != "active" || consumedAt.Valid || now.After(expiresAt) {
		return device{}, "", &appError{HTTPStatus: http.StatusUnprocessableEntity, Code: "activation_code_invalid", Message: "activation code is invalid"}
	}

	item := device{
		DeviceID:  randomUUID(),
		TenantID:  tenantID,
		Name:      firstNonEmpty(req.DeviceName, name),
		Platform:  req.Platform,
		Arch:      req.Arch,
		Status:    "active",
		LastSeen:  now.Format(time.RFC3339),
		CreatedAt: now.Format(time.RFC3339),
	}
	deviceToken := "dt_" + randomHex(24)

	_, err = tx.ExecContext(ctx, `
INSERT INTO devices(id, tenant_id, name, platform, arch, status, last_seen_at, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $7, $7)`,
		item.DeviceID, item.TenantID, item.Name, item.Platform, item.Arch, item.Status, now)
	if err != nil {
		return device{}, "", internalStoreError(err)
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO device_tokens(id, device_id, token_hash, status, created_at)
VALUES ($1, $2, $3, 'active', $4)`, randomUUID(), item.DeviceID, sha256Hex(deviceToken), now)
	if err != nil {
		return device{}, "", internalStoreError(err)
	}
	_, err = tx.ExecContext(ctx, `
UPDATE device_activation_codes
SET status = 'disabled', consumed_at = $1
WHERE code_hash = $2`, now, sha256Hex(req.ActivationCode))
	if err != nil {
		return device{}, "", internalStoreError(err)
	}
	if err := tx.Commit(); err != nil {
		return device{}, "", internalStoreError(err)
	}
	return item, deviceToken, nil
}

func (s *postgresStore) CreateSession(req createAgentSessionRequest, now time.Time) (session, *appError) {
	if req.CLIType == "" {
		req.CLIType = "custom"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var tenantID string
	err := s.db.QueryRowContext(ctx, "SELECT tenant_id::text FROM devices WHERE id = $1", req.DeviceID).Scan(&tenantID)
	if err == sql.ErrNoRows {
		return session{}, &appError{HTTPStatus: http.StatusNotFound, Code: "device_offline", Message: "device not found"}
	}
	if err != nil {
		return session{}, internalStoreError(err)
	}

	item := session{
		SessionID:         randomUUID(),
		TenantID:          tenantID,
		DeviceID:          req.DeviceID,
		CLIType:           req.CLIType,
		Status:            "running",
		StartedAt:         now.Format(time.RFC3339),
		LastOutputSummary: firstNonEmpty(req.LastOutputSummary, "fake CLI session started"),
		PendingApprovals:  0,
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO sessions(id, tenant_id, device_id, cli_type, status, command_line_redacted, working_dir_hash, last_output_summary, pending_approval_count, started_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 0, $9)`,
		item.SessionID, item.TenantID, item.DeviceID, item.CLIType, item.Status, req.CommandLineRedacted, req.WorkingDirHash, item.LastOutputSummary, now)
	if err != nil {
		return session{}, internalStoreError(err)
	}
	return item, nil
}

func (s *postgresStore) CreateApproval(req createAgentApprovalRequest, now time.Time) (approval, *appError) {
	if req.EventType == "" {
		req.EventType = "permission_request"
	}
	if req.RiskLevel == "" {
		req.RiskLevel = "high"
	}
	if req.ExpiresIn <= 0 {
		req.ExpiresIn = 300
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return approval{}, internalStoreError(err)
	}
	defer tx.Rollback()

	var tenantID, cliType string
	err = tx.QueryRowContext(ctx, `
SELECT tenant_id::text, cli_type
FROM sessions
WHERE id = $1 AND device_id = $2
FOR UPDATE`, req.SessionID, req.DeviceID).Scan(&tenantID, &cliType)
	if err == sql.ErrNoRows {
		return approval{}, &appError{HTTPStatus: http.StatusNotFound, Code: "agent_session_not_found", Message: "session not found"}
	}
	if err != nil {
		return approval{}, internalStoreError(err)
	}

	if req.IdempotencyKey != "" {
		if item, ok, appErr := s.findApprovalByIdempotency(ctx, tx, tenantID, req.IdempotencyKey); appErr != nil {
			return approval{}, appErr
		} else if ok {
			return item, nil
		}
	}

	item := approval{
		ApprovalID:     randomUUID(),
		TenantID:       tenantID,
		DeviceID:       req.DeviceID,
		SessionID:      req.SessionID,
		CLIType:        firstNonEmpty(req.CLIType, cliType),
		EventType:      req.EventType,
		RiskLevel:      req.RiskLevel,
		PromptText:     firstNonEmpty(req.PromptText, "allow command execution?"),
		ContextBefore:  req.ContextBefore,
		Status:         "waiting_decision",
		DeliveryStatus: "pending",
		CreatedAt:      now.Format(time.RFC3339),
		ExpiresAt:      now.Add(time.Duration(req.ExpiresIn) * time.Second).Format(time.RFC3339),
	}
	expiresAt, _ := time.Parse(time.RFC3339, item.ExpiresAt)

	_, err = tx.ExecContext(ctx, `
INSERT INTO approval_requests(id, tenant_id, device_id, session_id, idempotency_key, cli_type, event_type, risk_level, prompt_text, context_before, status, expires_at, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $13)`,
		item.ApprovalID, item.TenantID, item.DeviceID, item.SessionID, req.IdempotencyKey, item.CLIType, item.EventType, item.RiskLevel, item.PromptText, item.ContextBefore, item.Status, expiresAt, now)
	if err != nil {
		return approval{}, internalStoreError(err)
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO approval_notifications(id, tenant_id, approval_id, client_instance_id, user_id, client_type, channel, status, created_at)
SELECT gen_random_uuid(), $1, $2, id, user_id, client_type, 'websocket', 'pending', $3
FROM client_instances
WHERE tenant_id = $1 AND status = 'active'`,
		item.TenantID, item.ApprovalID, now)
	if err != nil {
		return approval{}, internalStoreError(err)
	}
	_, err = tx.ExecContext(ctx, `
UPDATE sessions
SET status = 'waiting_approval', pending_approval_count = pending_approval_count + 1
WHERE id = $1`, req.SessionID)
	if err != nil {
		return approval{}, internalStoreError(err)
	}
	if err := tx.Commit(); err != nil {
		return approval{}, internalStoreError(err)
	}
	return item, nil
}

func (s *postgresStore) GetApproval(approvalID string) (approval, *appError) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	row := s.db.QueryRowContext(ctx, approvalByIDQuery()+`
WHERE ar.id = $1`, approvalID)
	item, err := scanApproval(row)
	if err == sql.ErrNoRows {
		return approval{}, &appError{HTTPStatus: http.StatusNotFound, Code: "approval_not_found", Message: "approval not found"}
	}
	if err != nil {
		return approval{}, internalStoreError(err)
	}
	return item, nil
}

func (s *postgresStore) ListApprovals(tenantID string, status string) []approval {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	query := `
SELECT ar.id::text, ar.tenant_id::text, ar.device_id::text, ar.session_id::text, ar.cli_type, ar.event_type, ar.risk_level,
       ar.prompt_text, ar.context_before, ar.status, ar.decision_type, ar.decision_payload,
       COALESCE((SELECT id::text FROM approval_deliveries WHERE approval_id = ar.id ORDER BY created_at DESC LIMIT 1), '') AS delivery_id,
       COALESCE((SELECT status FROM approval_deliveries WHERE approval_id = ar.id ORDER BY created_at DESC LIMIT 1), 'pending') AS delivery_status,
       ar.decided_by::text, ar.decided_at, ar.created_at, ar.expires_at
FROM approval_requests ar
WHERE ar.tenant_id = $1`
	args := []any{tenantID}
	if status != "" {
		query += " AND ar.status = $2"
		args = append(args, status)
	}
	query += " ORDER BY ar.created_at DESC"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return []approval{}
	}
	defer rows.Close()

	items := []approval{}
	for rows.Next() {
		item, err := scanApproval(rows)
		if err != nil {
			return []approval{}
		}
		items = append(items, item)
	}
	return items
}

func (s *postgresStore) SubmitApprovalDecision(approvalID string, req submitApprovalDecisionRequest, idempotencyKey string, decidedBy map[string]string, now time.Time) (approval, *appError) {
	if req.DecisionType == "" {
		req.DecisionType = "approve"
	}
	if idempotencyKey == "" {
		return approval{}, &appError{HTTPStatus: http.StatusBadRequest, Code: "idempotency_key_required", Message: "Idempotency-Key header is required"}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return approval{}, internalStoreError(err)
	}
	defer tx.Rollback()

	var tenantID string
	err = tx.QueryRowContext(ctx, "SELECT tenant_id::text FROM approval_requests WHERE id = $1", approvalID).Scan(&tenantID)
	if err == sql.ErrNoRows {
		return approval{}, &appError{HTTPStatus: http.StatusNotFound, Code: "approval_not_found", Message: "approval not found"}
	}
	if err != nil {
		return approval{}, internalStoreError(err)
	}

	signature := req.DecisionType + ":" + req.Payload
	replayScope := "approval-decision:" + tenantID + ":" + approvalID
	if replay, ok, appErr := readIdempotencyReplay(ctx, tx, replayScope, idempotencyKey, signature); appErr != nil {
		return approval{}, appErr
	} else if ok {
		var item approval
		if err := json.Unmarshal(replay, &item); err != nil {
			return approval{}, internalStoreError(err)
		}
		return item, nil
	}

	var currentStatus string
	err = tx.QueryRowContext(ctx, "SELECT status FROM approval_requests WHERE id = $1 FOR UPDATE", approvalID).Scan(&currentStatus)
	if err != nil {
		return approval{}, internalStoreError(err)
	}
	if currentStatus != "waiting_decision" {
		return approval{}, &appError{HTTPStatus: http.StatusConflict, Code: "approval_already_decided", Message: "approval already decided"}
	}

	deliveryID := randomUUID()
	decidedByJSON, err := json.Marshal(decidedBy)
	if err != nil {
		return approval{}, internalStoreError(err)
	}
	_, err = tx.ExecContext(ctx, `
UPDATE approval_requests
SET status = 'delivering', decision_type = $1, decision_payload = $2, decided_by = $3::jsonb, decided_at = $4, updated_at = $4
WHERE id = $5`,
		req.DecisionType, req.Payload, string(decidedByJSON), now, approvalID)
	if err != nil {
		return approval{}, internalStoreError(err)
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO approval_actions(tenant_id, approval_id, action_type, idempotency_key, actor_type, actor_id, client_instance_id, client_type, payload_redacted, result, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'accepted', $10)`,
		tenantID, approvalID, req.DecisionType, idempotencyKey, decidedBy["actor_type"], nullableUUID(decidedBy["actor_id"]), nullableUUID(decidedBy["client_instance_id"]), decidedBy["client_type"], req.Payload, now)
	if err != nil {
		return approval{}, internalStoreError(err)
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO approval_deliveries(id, tenant_id, approval_id, device_id, session_id, decision_type, decision_payload, status, attempt_count, sent_at, created_at, updated_at)
SELECT $1, tenant_id, id, device_id, session_id, $2, $3, 'sent', 1, $4, $4, $4
FROM approval_requests
WHERE id = $5`,
		deliveryID, req.DecisionType, req.Payload, now, approvalID)
	if err != nil {
		return approval{}, internalStoreError(err)
	}
	_, err = tx.ExecContext(ctx, `
UPDATE sessions
SET status = 'waiting_approval', last_output_summary = $1
WHERE id = (SELECT session_id FROM approval_requests WHERE id = $2)`,
		"approval "+req.DecisionType+" delivering", approvalID)
	if err != nil {
		return approval{}, internalStoreError(err)
	}

	item, appErr := s.findApprovalByID(ctx, tx, approvalID)
	if appErr != nil {
		return approval{}, appErr
	}
	responseJSON, err := json.Marshal(item)
	if err != nil {
		return approval{}, internalStoreError(err)
	}
	if err := writeIdempotencyReplay(ctx, tx, replayScope, idempotencyKey, signature, responseJSON); err != nil {
		return approval{}, internalStoreError(err)
	}
	if err := tx.Commit(); err != nil {
		return approval{}, internalStoreError(err)
	}
	return item, nil
}

func (s *postgresStore) AckApprovalDecision(req ackApprovalDecisionRequest) (map[string]any, *appError) {
	if req.AckResult == "" {
		req.AckResult = "written"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, internalStoreError(err)
	}
	defer tx.Rollback()

	var item approval
	var deliveryStatus string
	err = tx.QueryRowContext(ctx, `
SELECT ar.id::text, ar.status, ar.session_id::text, ad.id::text, ad.status
FROM approval_requests ar
LEFT JOIN approval_deliveries ad ON ad.approval_id = ar.id
WHERE ar.id = $1
ORDER BY ad.created_at DESC
LIMIT 1
FOR UPDATE OF ar`, req.ApprovalID).Scan(&item.ApprovalID, &item.Status, &item.SessionID, &item.DeliveryID, &deliveryStatus)
	if err == sql.ErrNoRows {
		return nil, &appError{HTTPStatus: http.StatusNotFound, Code: "approval_not_found", Message: "approval not found"}
	}
	if err != nil {
		return nil, internalStoreError(err)
	}
	item.DeliveryStatus = deliveryStatus
	if item.Status == "delivered" && item.DeliveryStatus == "acked" {
		return approvalAckData(item), nil
	}
	if item.Status != "delivering" || item.SessionID != req.SessionID || item.DeliveryID != req.DeliveryID {
		return nil, &appError{HTTPStatus: http.StatusConflict, Code: "delivery_failed", Message: "delivery ack does not match an active delivery"}
	}

	nextStatus := "delivery_failed"
	nextDeliveryStatus := "failed"
	if req.AckResult == "written" || req.AckResult == "accepted" {
		nextStatus = "delivered"
		nextDeliveryStatus = "acked"
	}
	detailJSON, err := json.Marshal(req.Detail)
	if err != nil {
		return nil, internalStoreError(err)
	}
	_, err = tx.ExecContext(ctx, `
UPDATE approval_requests SET status = $1, updated_at = now() WHERE id = $2`, nextStatus, req.ApprovalID)
	if err != nil {
		return nil, internalStoreError(err)
	}
	_, err = tx.ExecContext(ctx, `
UPDATE approval_deliveries
SET status = $1, ack_result = $2, ack_detail = $3::jsonb, acked_at = now(), updated_at = now()
WHERE id = $4`, nextDeliveryStatus, req.AckResult, string(detailJSON), req.DeliveryID)
	if err != nil {
		return nil, internalStoreError(err)
	}
	_, err = tx.ExecContext(ctx, `
UPDATE sessions
SET status = 'running',
    pending_approval_count = GREATEST(pending_approval_count - 1, 0),
    last_output_summary = $1
WHERE id = $2`, "approval "+nextStatus, req.SessionID)
	if err != nil {
		return nil, internalStoreError(err)
	}
	if err := tx.Commit(); err != nil {
		return nil, internalStoreError(err)
	}
	item.Status = nextStatus
	item.DeliveryStatus = nextDeliveryStatus
	return approvalAckData(item), nil
}

func (s *postgresStore) CreateDeviceGrant(deviceID string, req createDeviceGrantRequest, grantedBy string, now time.Time) (deviceGrant, *appError) {
	if req.UserID == "" {
		return deviceGrant{}, &appError{HTTPStatus: http.StatusBadRequest, Code: "message_schema_invalid", Message: "user_id is required"}
	}
	if req.Permission == "" {
		req.Permission = "view"
	}
	switch req.Permission {
	case "view", "approve", "admin":
	default:
		return deviceGrant{}, &appError{HTTPStatus: http.StatusBadRequest, Code: "message_schema_invalid", Message: "permission must be view, approve, or admin"}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return deviceGrant{}, internalStoreError(err)
	}
	defer tx.Rollback()

	var tenantID string
	err = tx.QueryRowContext(ctx, "SELECT tenant_id::text FROM devices WHERE id = $1", deviceID).Scan(&tenantID)
	if err == sql.ErrNoRows {
		return deviceGrant{}, &appError{HTTPStatus: http.StatusNotFound, Code: "device_offline", Message: "device not found"}
	}
	if err != nil {
		return deviceGrant{}, internalStoreError(err)
	}

	item := deviceGrant{
		GrantID:    randomUUID(),
		TenantID:   tenantID,
		DeviceID:   deviceID,
		UserID:     req.UserID,
		Permission: req.Permission,
		GrantedBy:  grantedBy,
		CreatedAt:  now.Format(time.RFC3339),
	}
	row := tx.QueryRowContext(ctx, `
INSERT INTO device_grants(id, tenant_id, device_id, user_id, permission, granted_by_user_id, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (device_id, user_id, permission)
WHERE revoked_at IS NULL
DO UPDATE SET granted_by_user_id = EXCLUDED.granted_by_user_id
RETURNING id::text, created_at`,
		item.GrantID, item.TenantID, item.DeviceID, item.UserID, item.Permission, item.GrantedBy, now)
	var createdAt time.Time
	if err := row.Scan(&item.GrantID, &createdAt); err != nil {
		return deviceGrant{}, internalStoreError(err)
	}
	item.CreatedAt = createdAt.Format(time.RFC3339)
	if err := tx.Commit(); err != nil {
		return deviceGrant{}, internalStoreError(err)
	}
	return item, nil
}

func (s *postgresStore) CanApproveDevice(tenantID string, deviceID string, userID string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var exists bool
	err := s.db.QueryRowContext(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM device_grants
    WHERE tenant_id = $1
      AND device_id = $2
      AND user_id = $3
      AND permission IN ('approve', 'admin')
      AND revoked_at IS NULL
)`, tenantID, deviceID, userID).Scan(&exists)
	return err == nil && exists
}

func (s *postgresStore) ExpireApprovals(now time.Time) []approval {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return []approval{}
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `
SELECT id::text
FROM approval_requests
WHERE status = 'waiting_decision' AND expires_at <= $1
ORDER BY expires_at ASC
FOR UPDATE`, now)
	if err != nil {
		return []approval{}
	}
	defer rows.Close()

	approvalIDs := []string{}
	for rows.Next() {
		var approvalID string
		if err := rows.Scan(&approvalID); err != nil {
			return []approval{}
		}
		approvalIDs = append(approvalIDs, approvalID)
	}
	if err := rows.Err(); err != nil {
		return []approval{}
	}

	expired := []approval{}
	decidedBy := map[string]string{
		"actor_type":   "system",
		"actor_id":     "timeout-worker",
		"display_name": "Timeout Worker",
		"client_type":  "worker",
	}
	decidedByJSON, err := json.Marshal(decidedBy)
	if err != nil {
		return []approval{}
	}
	for _, approvalID := range approvalIDs {
		deliveryID := randomUUID()
		if _, err := tx.ExecContext(ctx, `
UPDATE approval_requests
SET status = 'delivering',
    decision_type = 'reject',
    decision_payload = 'timeout',
    decided_by = $1::jsonb,
    decided_at = $2,
    updated_at = $2
WHERE id = $3`, string(decidedByJSON), now, approvalID); err != nil {
			return []approval{}
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO approval_actions(tenant_id, approval_id, action_type, actor_type, actor_id, client_type, payload_redacted, result, created_at)
SELECT tenant_id, id, 'reject', 'system', NULL, 'worker', 'timeout', 'accepted', $1
FROM approval_requests
WHERE id = $2`, now, approvalID); err != nil {
			return []approval{}
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO approval_deliveries(id, tenant_id, approval_id, device_id, session_id, decision_type, decision_payload, status, attempt_count, sent_at, created_at, updated_at)
SELECT $1, tenant_id, id, device_id, session_id, 'reject', 'timeout', 'sent', 1, $2, $2, $2
FROM approval_requests
WHERE id = $3`, deliveryID, now, approvalID); err != nil {
			return []approval{}
		}
		if _, err := tx.ExecContext(ctx, `
UPDATE sessions
SET status = 'waiting_approval', last_output_summary = 'approval timeout reject delivering'
WHERE id = (SELECT session_id FROM approval_requests WHERE id = $1)`, approvalID); err != nil {
			return []approval{}
		}
		item, appErr := s.findApprovalByID(ctx, tx, approvalID)
		if appErr != nil {
			return []approval{}
		}
		detailJSON, _ := json.Marshal(map[string]any{"delivery_id": item.DeliveryID})
		if _, err := tx.ExecContext(ctx, `
INSERT INTO audit_logs(tenant_id, actor_type, actor_id, action, resource_type, resource_id, result, trace_id, detail, created_at)
VALUES ($1, 'system', NULL, 'approval.timeout_reject', 'approval', $2, 'success', 'tr_worker', $3::jsonb, $4)`,
			item.TenantID, item.ApprovalID, string(detailJSON), now); err != nil {
			return []approval{}
		}
		expired = append(expired, item)
	}
	if err := tx.Commit(); err != nil {
		return []approval{}
	}
	return expired
}

func (s *postgresStore) AppendAuditLog(item auditLog, now time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if item.Detail == nil {
		item.Detail = map[string]any{}
	}
	detailJSON, err := json.Marshal(item.Detail)
	if err != nil {
		return
	}
	_, _ = s.db.ExecContext(ctx, `
INSERT INTO audit_logs(tenant_id, actor_type, actor_id, action, resource_type, resource_id, result, trace_id, detail, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9::jsonb, $10)`,
		item.TenantID, item.ActorType, nullableUUID(item.ActorID), item.Action, item.ResourceType, nullableUUID(item.ResourceID), item.Result, item.TraceID, string(detailJSON), now)
}

func (s *postgresStore) ListAuditLogs(tenantID string) []auditLog {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rows, err := s.db.QueryContext(ctx, `
SELECT id, tenant_id::text, actor_type, COALESCE(actor_id::text, ''), action, resource_type, COALESCE(resource_id::text, ''), result, trace_id, detail::text, created_at
FROM audit_logs
WHERE tenant_id = $1
ORDER BY created_at DESC, id DESC
LIMIT 100`, tenantID)
	if err != nil {
		return []auditLog{}
	}
	defer rows.Close()
	items := []auditLog{}
	for rows.Next() {
		var item auditLog
		var detailJSON string
		var createdAt time.Time
		if err := rows.Scan(&item.AuditID, &item.TenantID, &item.ActorType, &item.ActorID, &item.Action, &item.ResourceType, &item.ResourceID, &item.Result, &item.TraceID, &detailJSON, &createdAt); err != nil {
			return []auditLog{}
		}
		_ = json.Unmarshal([]byte(detailJSON), &item.Detail)
		if item.Detail == nil {
			item.Detail = map[string]any{}
		}
		item.CreatedAt = createdAt.Format(time.RFC3339)
		items = append(items, item)
	}
	return items
}

func (s *postgresStore) GetSession(sessionID string) (session, *appError) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	row := s.db.QueryRowContext(ctx, `
SELECT id::text, tenant_id::text, device_id::text, cli_type, status, started_at, last_output_summary, pending_approval_count
FROM sessions
WHERE id = $1`, sessionID)
	var item session
	var startedAt time.Time
	err := row.Scan(&item.SessionID, &item.TenantID, &item.DeviceID, &item.CLIType, &item.Status, &startedAt, &item.LastOutputSummary, &item.PendingApprovals)
	if err == sql.ErrNoRows {
		return session{}, &appError{HTTPStatus: http.StatusNotFound, Code: "agent_session_not_found", Message: "session not found"}
	}
	if err != nil {
		return session{}, internalStoreError(err)
	}
	item.StartedAt = startedAt.Format(time.RFC3339)
	return item, nil
}

func (s *postgresStore) ListDeviceSessions(deviceID string) []session {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rows, err := s.db.QueryContext(ctx, `
SELECT id::text, tenant_id::text, device_id::text, cli_type, status, started_at, last_output_summary, pending_approval_count
FROM sessions
WHERE device_id = $1
ORDER BY started_at DESC`, deviceID)
	if err != nil {
		return []session{}
	}
	defer rows.Close()

	items := []session{}
	for rows.Next() {
		var item session
		var startedAt time.Time
		if err := rows.Scan(&item.SessionID, &item.TenantID, &item.DeviceID, &item.CLIType, &item.Status, &startedAt, &item.LastOutputSummary, &item.PendingApprovals); err != nil {
			return []session{}
		}
		item.StartedAt = startedAt.Format(time.RFC3339)
		items = append(items, item)
	}
	return items
}

func (s *postgresStore) MarkDeviceSeen(deviceID string, now time.Time) *appError {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := s.db.ExecContext(ctx, `
UPDATE devices
SET status = 'active', last_seen_at = $1, updated_at = $1
WHERE id = $2`, now, deviceID)
	if err != nil {
		return internalStoreError(err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return internalStoreError(err)
	}
	if rows == 0 {
		return &appError{HTTPStatus: http.StatusNotFound, Code: "device_offline", Message: "device not found"}
	}
	return nil
}

func (s *postgresStore) MarkClientInstanceSeen(clientInstanceID string, now time.Time) *appError {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := s.db.ExecContext(ctx, `
UPDATE client_instances
SET status = 'active', last_seen_at = $1, updated_at = $1
WHERE id = $2`, now, clientInstanceID)
	if err != nil {
		return internalStoreError(err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return internalStoreError(err)
	}
	if rows == 0 {
		return &appError{HTTPStatus: http.StatusNotFound, Code: "client_instance_not_found", Message: "client instance not found"}
	}
	return nil
}

func (s *postgresStore) ValidateDeviceToken(deviceID string, token string) *appError {
	if token == "" {
		return &appError{HTTPStatus: http.StatusUnauthorized, Code: "device_token_invalid", Message: "device token is invalid"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var exists bool
	err := s.db.QueryRowContext(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM device_tokens
    WHERE device_id = $1 AND token_hash = $2 AND status = 'active'
)`, deviceID, sha256Hex(token)).Scan(&exists)
	if err != nil {
		return internalStoreError(err)
	}
	if !exists {
		return &appError{HTTPStatus: http.StatusUnauthorized, Code: "device_token_invalid", Message: "device token is invalid"}
	}
	return nil
}

func (s *postgresStore) ValidateApprovalDeviceToken(approvalID string, token string) *appError {
	if token == "" {
		return &appError{HTTPStatus: http.StatusUnauthorized, Code: "device_token_invalid", Message: "device token is invalid"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var exists bool
	err := s.db.QueryRowContext(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM approval_requests ar
    JOIN device_tokens dt ON dt.device_id = ar.device_id
    WHERE ar.id = $1 AND dt.token_hash = $2 AND dt.status = 'active'
)`, approvalID, sha256Hex(token)).Scan(&exists)
	if err != nil {
		return internalStoreError(err)
	}
	if !exists {
		var approvalExists bool
		if err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM approval_requests WHERE id = $1)`, approvalID).Scan(&approvalExists); err != nil {
			return internalStoreError(err)
		}
		if !approvalExists {
			return &appError{HTTPStatus: http.StatusNotFound, Code: "approval_not_found", Message: "approval not found"}
		}
		return &appError{HTTPStatus: http.StatusUnauthorized, Code: "device_token_invalid", Message: "device token is invalid"}
	}
	return nil
}

func (s *postgresStore) ListPendingDeliveries(deviceID string) []approval {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rows, err := s.db.QueryContext(ctx, approvalByIDQuery()+`
JOIN approval_deliveries ad ON ad.approval_id = ar.id
WHERE ar.device_id = $1 AND ar.status = 'delivering' AND ad.status = 'sent'
ORDER BY ad.created_at ASC`, deviceID)
	if err != nil {
		return []approval{}
	}
	defer rows.Close()

	items := []approval{}
	for rows.Next() {
		item, err := scanApproval(rows)
		if err != nil {
			return []approval{}
		}
		items = append(items, item)
	}
	return items
}

func ensureTenant(ctx context.Context, tx *sql.Tx, tenantID string) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO tenants(id, name, status)
VALUES ($1, 'Local Tenant', 'active')
ON CONFLICT (id) DO NOTHING`, tenantID)
	return err
}

func readIdempotencyReplay(ctx context.Context, tx *sql.Tx, scope string, key string, signature string) ([]byte, bool, *appError) {
	var storedSignature string
	var responseJSON []byte
	err := tx.QueryRowContext(ctx, `
SELECT request_signature, response_json
FROM http_idempotency_keys
WHERE scope = $1 AND idempotency_key = $2`, scope, key).Scan(&storedSignature, &responseJSON)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, internalStoreError(err)
	}
	if storedSignature != signature {
		return nil, false, &appError{HTTPStatus: http.StatusConflict, Code: "approval_decision_conflict", Message: "Idempotency-Key reused with different parameters"}
	}
	return responseJSON, true, nil
}

func writeIdempotencyReplay(ctx context.Context, tx *sql.Tx, scope string, key string, signature string, responseJSON []byte) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO http_idempotency_keys(scope, idempotency_key, request_signature, response_json)
VALUES ($1, $2, $3, $4::jsonb)`, scope, key, signature, string(responseJSON))
	return err
}

func (s *postgresStore) findApprovalByIdempotency(ctx context.Context, tx *sql.Tx, tenantID string, idempotencyKey string) (approval, bool, *appError) {
	row := tx.QueryRowContext(ctx, approvalByIDQuery()+`
WHERE ar.tenant_id = $1 AND ar.idempotency_key = $2`, tenantID, idempotencyKey)
	item, err := scanApproval(row)
	if err == sql.ErrNoRows {
		return approval{}, false, nil
	}
	if err != nil {
		return approval{}, false, internalStoreError(err)
	}
	return item, true, nil
}

func (s *postgresStore) findApprovalByID(ctx context.Context, tx *sql.Tx, approvalID string) (approval, *appError) {
	row := tx.QueryRowContext(ctx, approvalByIDQuery()+`
WHERE ar.id = $1`, approvalID)
	item, err := scanApproval(row)
	if err == sql.ErrNoRows {
		return approval{}, &appError{HTTPStatus: http.StatusNotFound, Code: "approval_not_found", Message: "approval not found"}
	}
	if err != nil {
		return approval{}, internalStoreError(err)
	}
	return item, nil
}

func approvalByIDQuery() string {
	return `
SELECT ar.id::text, ar.tenant_id::text, ar.device_id::text, ar.session_id::text, ar.cli_type, ar.event_type, ar.risk_level,
       ar.prompt_text, ar.context_before, ar.status, ar.decision_type, ar.decision_payload,
       COALESCE((SELECT id::text FROM approval_deliveries WHERE approval_id = ar.id ORDER BY created_at DESC LIMIT 1), '') AS delivery_id,
       COALESCE((SELECT status FROM approval_deliveries WHERE approval_id = ar.id ORDER BY created_at DESC LIMIT 1), 'pending') AS delivery_status,
       ar.decided_by::text, ar.decided_at, ar.created_at, ar.expires_at
FROM approval_requests ar
`
}

type approvalScanner interface {
	Scan(dest ...any) error
}

func scanApproval(scanner approvalScanner) (approval, error) {
	var item approval
	var decidedByJSON string
	var decidedAt sql.NullTime
	var createdAt, expiresAt time.Time
	err := scanner.Scan(&item.ApprovalID, &item.TenantID, &item.DeviceID, &item.SessionID, &item.CLIType, &item.EventType, &item.RiskLevel,
		&item.PromptText, &item.ContextBefore, &item.Status, &item.DecisionType, &item.DecisionPayload, &item.DeliveryID, &item.DeliveryStatus,
		&decidedByJSON, &decidedAt, &createdAt, &expiresAt)
	if err != nil {
		return approval{}, err
	}
	if decidedByJSON != "" {
		json.Unmarshal([]byte(decidedByJSON), &item.DecidedBy)
	}
	if item.DecidedBy == nil {
		item.DecidedBy = map[string]string{}
	}
	if decidedAt.Valid {
		item.DecidedAt = decidedAt.Time.Format(time.RFC3339)
	}
	item.CreatedAt = createdAt.Format(time.RFC3339)
	item.ExpiresAt = expiresAt.Format(time.RFC3339)
	return item, nil
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func nullableUUID(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func internalStoreError(err error) *appError {
	return &appError{HTTPStatus: http.StatusInternalServerError, Code: "internal_error", Message: err.Error()}
}
