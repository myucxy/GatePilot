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

CREATE INDEX IF NOT EXISTS idx_output_chunks_session_sequence ON output_chunks(session_id, sequence_no DESC);
