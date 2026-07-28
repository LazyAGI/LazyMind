-- +migrate Dialect postgres
CREATE TABLE IF NOT EXISTS plugin_run_outbox (
    task_id VARCHAR(36) PRIMARY KEY,
    payload JSONB NOT NULL,
    status VARCHAR(16) NOT NULL,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_plugin_run_outbox_status ON plugin_run_outbox(status, created_at);

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
