CREATE TABLE IF NOT EXISTS policy_rules (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    owner_user_id uuid,
    name varchar(128) NOT NULL,
    scope varchar(32) NOT NULL DEFAULT 'tenant',
    priority integer NOT NULL DEFAULT 100,
    enabled boolean NOT NULL DEFAULT true,
    cli_type varchar(64) NOT NULL DEFAULT '',
    event_type varchar(64) NOT NULL DEFAULT '',
    risk_level varchar(32) NOT NULL DEFAULT '',
    device_selector jsonb NOT NULL DEFAULT '{}'::jsonb,
    command_pattern varchar(512) NOT NULL DEFAULT '',
    decision varchar(32) NOT NULL,
    expires_at timestamptz,
    reason text NOT NULL DEFAULT '',
    created_by_user_id uuid,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_policy_rules_tenant_enabled_priority ON policy_rules(tenant_id, enabled, priority);
CREATE INDEX IF NOT EXISTS idx_policy_rules_owner_enabled_priority ON policy_rules(owner_user_id, enabled, priority);
