-- +migrate Dialect postgres
ALTER TABLE plugin_session_steps ADD COLUMN IF NOT EXISTS lease_owner VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE plugin_session_steps ADD COLUMN IF NOT EXISTS lease_token VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE plugin_session_steps ADD COLUMN IF NOT EXISTS fencing_generation BIGINT NOT NULL DEFAULT 0;
ALTER TABLE plugin_session_steps ADD COLUMN IF NOT EXISTS lease_expires_at TIMESTAMP NULL;
ALTER TABLE plugin_session_steps ADD COLUMN IF NOT EXISTS heartbeat_at TIMESTAMP NULL;
ALTER TABLE plugin_session_steps ADD COLUMN IF NOT EXISTS progress_json JSONB NOT NULL DEFAULT '{}';
ALTER TABLE plugin_session_steps ADD COLUMN IF NOT EXISTS terminal_code VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE plugin_session_steps ADD COLUMN IF NOT EXISTS result_json JSONB NOT NULL DEFAULT '{}';
CREATE INDEX IF NOT EXISTS idx_plugin_session_steps_claim ON plugin_session_steps(status, lease_expires_at, id);
CREATE TABLE IF NOT EXISTS workflow_outbox (
    id VARCHAR(36) PRIMARY KEY,
    attempt_id VARCHAR(36) NOT NULL UNIQUE,
    session_id VARCHAR(36) NOT NULL,
    payload_json JSONB NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'pending',
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_workflow_outbox_status ON workflow_outbox(status, created_at);
CREATE INDEX IF NOT EXISTS idx_workflow_outbox_session ON workflow_outbox(session_id);

-- +migrate Dialect sqlite
ALTER TABLE plugin_session_steps ADD COLUMN lease_owner TEXT NOT NULL DEFAULT '';
ALTER TABLE plugin_session_steps ADD COLUMN lease_token TEXT NOT NULL DEFAULT '';
ALTER TABLE plugin_session_steps ADD COLUMN fencing_generation INTEGER NOT NULL DEFAULT 0;
ALTER TABLE plugin_session_steps ADD COLUMN lease_expires_at DATETIME NULL;
ALTER TABLE plugin_session_steps ADD COLUMN heartbeat_at DATETIME NULL;
ALTER TABLE plugin_session_steps ADD COLUMN progress_json TEXT NOT NULL DEFAULT '{}';
ALTER TABLE plugin_session_steps ADD COLUMN terminal_code TEXT NOT NULL DEFAULT '';
ALTER TABLE plugin_session_steps ADD COLUMN result_json TEXT NOT NULL DEFAULT '{}';
CREATE INDEX IF NOT EXISTS idx_plugin_session_steps_claim ON plugin_session_steps(status, lease_expires_at, id);
CREATE TABLE IF NOT EXISTS workflow_outbox (
    id TEXT PRIMARY KEY, attempt_id TEXT NOT NULL UNIQUE, session_id TEXT NOT NULL,
    payload_json TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'pending',
    created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_workflow_outbox_status ON workflow_outbox(status, created_at);
CREATE INDEX IF NOT EXISTS idx_workflow_outbox_session ON workflow_outbox(session_id);
