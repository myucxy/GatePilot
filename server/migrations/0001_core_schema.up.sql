-- GatePilot M1-M3 核心持久化结构。
-- 本迁移先覆盖设备绑定、会话、审批决策和投递 ACK 主链路，后续迁移再补 OIDC、Push、策略和分区。
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS tenants (
    id uuid PRIMARY KEY,
    name varchar(128) NOT NULL,
    status varchar(32) NOT NULL DEFAULT 'active',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS device_activation_codes (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name varchar(128) NOT NULL,
    code_hash varchar(128) NOT NULL UNIQUE,
    status varchar(32) NOT NULL DEFAULT 'active',
    consumed_at timestamptz,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS devices (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name varchar(128) NOT NULL,
    platform varchar(32) NOT NULL,
    arch varchar(32) NOT NULL,
    status varchar(32) NOT NULL,
    last_seen_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS client_instances (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id uuid NOT NULL,
    client_type varchar(32) NOT NULL,
    device_id uuid REFERENCES devices(id) ON DELETE CASCADE,
    display_name varchar(128) NOT NULL,
    app_version varchar(64) NOT NULL,
    platform varchar(64) NOT NULL,
    push_token_ciphertext text NOT NULL DEFAULT '',
    push_provider varchar(32) NOT NULL DEFAULT '',
    status varchar(32) NOT NULL,
    last_seen_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS device_tokens (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id uuid NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    token_hash varchar(128) NOT NULL UNIQUE,
    status varchar(32) NOT NULL DEFAULT 'active',
    created_at timestamptz NOT NULL DEFAULT now(),
    rotated_at timestamptz,
    revoked_at timestamptz
);

CREATE TABLE IF NOT EXISTS device_grants (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    device_id uuid NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    user_id uuid NOT NULL,
    permission varchar(32) NOT NULL,
    granted_by_user_id uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz,
    revoked_at timestamptz
);

CREATE TABLE IF NOT EXISTS sessions (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    device_id uuid NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    cli_type varchar(32) NOT NULL,
    status varchar(32) NOT NULL,
    command_line_redacted text NOT NULL DEFAULT '',
    working_dir_hash varchar(128) NOT NULL DEFAULT '',
    last_output_summary text NOT NULL DEFAULT '',
    pending_approval_count integer NOT NULL DEFAULT 0,
    started_at timestamptz NOT NULL DEFAULT now(),
    ended_at timestamptz
);

CREATE TABLE IF NOT EXISTS approval_requests (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    device_id uuid NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    session_id uuid NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    idempotency_key varchar(256) NOT NULL DEFAULT '',
    cli_type varchar(32) NOT NULL,
    event_type varchar(64) NOT NULL,
    risk_level varchar(32) NOT NULL,
    prompt_text text NOT NULL,
    context_before text NOT NULL DEFAULT '',
    status varchar(32) NOT NULL,
    decision_type varchar(32) NOT NULL DEFAULT '',
    decision_payload text NOT NULL DEFAULT '',
    decided_by jsonb NOT NULL DEFAULT '{}'::jsonb,
    decided_at timestamptz,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS approval_deliveries (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    approval_id uuid NOT NULL REFERENCES approval_requests(id) ON DELETE CASCADE,
    device_id uuid NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    session_id uuid NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    decision_type varchar(32) NOT NULL,
    decision_payload text NOT NULL DEFAULT '',
    status varchar(32) NOT NULL,
    attempt_count integer NOT NULL DEFAULT 0,
    ack_result varchar(32),
    ack_detail jsonb NOT NULL DEFAULT '{}'::jsonb,
    next_attempt_at timestamptz,
    sent_at timestamptz,
    acked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS approval_notifications (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    approval_id uuid NOT NULL REFERENCES approval_requests(id) ON DELETE CASCADE,
    client_instance_id uuid NOT NULL REFERENCES client_instances(id) ON DELETE CASCADE,
    user_id uuid NOT NULL,
    client_type varchar(32) NOT NULL,
    channel varchar(32) NOT NULL,
    status varchar(32) NOT NULL,
    sent_at timestamptz,
    read_at timestamptz,
    failed_at timestamptz,
    error text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS approval_actions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    approval_id uuid NOT NULL REFERENCES approval_requests(id) ON DELETE CASCADE,
    action_type varchar(32) NOT NULL,
    idempotency_key varchar(256) NOT NULL DEFAULT '',
    actor_type varchar(32) NOT NULL,
    actor_id uuid,
    client_instance_id uuid,
    client_type varchar(32),
    payload_redacted text NOT NULL DEFAULT '',
    result varchar(32) NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS output_chunks (
    id bigserial PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    session_id uuid NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    sequence_no bigint NOT NULL,
    stream_type varchar(16) NOT NULL,
    content_redacted text NOT NULL DEFAULT '',
    content_hash varchar(128) NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(session_id, sequence_no)
);

CREATE TABLE IF NOT EXISTS audit_logs (
    id bigserial PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    actor_type varchar(32) NOT NULL,
    actor_id uuid,
    action varchar(128) NOT NULL,
    resource_type varchar(64) NOT NULL,
    resource_id uuid,
    result varchar(32) NOT NULL,
    trace_id varchar(128) NOT NULL DEFAULT '',
    detail jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS http_idempotency_keys (
    scope varchar(256) NOT NULL,
    idempotency_key varchar(256) NOT NULL,
    request_signature varchar(512) NOT NULL,
    response_json jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (scope, idempotency_key)
);

CREATE INDEX IF NOT EXISTS idx_devices_tenant_status ON devices(tenant_id, status);
CREATE INDEX IF NOT EXISTS idx_client_instances_tenant_user_status ON client_instances(tenant_id, user_id, status);
CREATE INDEX IF NOT EXISTS idx_client_instances_device_status ON client_instances(device_id, status);
CREATE INDEX IF NOT EXISTS idx_device_grants_tenant_user ON device_grants(tenant_id, user_id);
CREATE INDEX IF NOT EXISTS idx_device_grants_device_revoked ON device_grants(device_id, revoked_at);
CREATE UNIQUE INDEX IF NOT EXISTS idx_device_grants_active_unique ON device_grants(device_id, user_id, permission) WHERE revoked_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_sessions_device_started ON sessions(device_id, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_approvals_tenant_status_created ON approval_requests(tenant_id, status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_approvals_session_created ON approval_requests(session_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_approvals_waiting_expires ON approval_requests(expires_at) WHERE status = 'waiting_decision';
CREATE UNIQUE INDEX IF NOT EXISTS idx_approvals_tenant_idempotency ON approval_requests(tenant_id, idempotency_key) WHERE idempotency_key <> '';
CREATE UNIQUE INDEX IF NOT EXISTS idx_approval_actions_idempotency ON approval_actions(tenant_id, approval_id, idempotency_key) WHERE idempotency_key <> '';
CREATE INDEX IF NOT EXISTS idx_output_chunks_session_sequence ON output_chunks(session_id, sequence_no DESC);
CREATE INDEX IF NOT EXISTS idx_deliveries_device_status_retry ON approval_deliveries(device_id, status, next_attempt_at);
CREATE INDEX IF NOT EXISTS idx_deliveries_approval_created ON approval_deliveries(approval_id, created_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_notifications_approval_client ON approval_notifications(approval_id, client_instance_id);
CREATE INDEX IF NOT EXISTS idx_notifications_tenant_user_created ON approval_notifications(tenant_id, user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_logs_tenant_created ON audit_logs(tenant_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_logs_trace ON audit_logs(trace_id);
CREATE INDEX IF NOT EXISTS idx_audit_logs_resource ON audit_logs(resource_type, resource_id);
