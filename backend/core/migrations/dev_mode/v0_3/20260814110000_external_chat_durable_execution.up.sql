-- +migrate Dialect postgres
ALTER TABLE external_agent_runs
    ADD COLUMN IF NOT EXISTS prompt TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS query TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS sequence INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS history_ext JSONB,
    ADD COLUMN IF NOT EXISTS host_id VARCHAR(128) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS lease_token VARCHAR(64) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS lease_expires_at TIMESTAMP,
    ADD COLUMN IF NOT EXISTS claimed_at TIMESTAMP,
    ADD COLUMN IF NOT EXISTS last_heartbeat_at TIMESTAMP,
    ADD COLUMN IF NOT EXISTS stop_requested BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS claim_count INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS next_event_sequence BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS completed_at TIMESTAMP;

CREATE INDEX IF NOT EXISTS idx_external_agent_runs_history_id
    ON external_agent_runs (history_id);
CREATE INDEX IF NOT EXISTS idx_external_agent_runs_actor_user_id
    ON external_agent_runs (actor_user_id);
CREATE INDEX IF NOT EXISTS idx_external_agent_runs_claim
    ON external_agent_runs (actor_user_id, provider, status, lease_expires_at, created_at);

CREATE TABLE IF NOT EXISTS external_chat_run_events (
    id VARCHAR(64) PRIMARY KEY,
    run_id VARCHAR(36) NOT NULL,
    sequence BIGINT NOT NULL,
    type VARCHAR(32) NOT NULL,
    text TEXT,
    provider_thread_id VARCHAR(128) NOT NULL DEFAULT '',
    error_message TEXT,
    created_at TIMESTAMP NOT NULL,
    CONSTRAINT uk_external_chat_run_event_sequence UNIQUE (run_id, sequence)
);
CREATE INDEX IF NOT EXISTS idx_external_chat_run_events_run_id
    ON external_chat_run_events (run_id);

CREATE TABLE IF NOT EXISTS external_chat_hosts (
    actor_user_id VARCHAR(255) NOT NULL,
    provider VARCHAR(32) NOT NULL,
    host_id VARCHAR(128) NOT NULL,
    installed BOOLEAN NOT NULL,
    ready BOOLEAN NOT NULL,
    unavailable_reason VARCHAR(512) NOT NULL DEFAULT '',
    last_seen TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    PRIMARY KEY (actor_user_id, provider, host_id)
);
CREATE INDEX IF NOT EXISTS idx_external_chat_hosts_last_seen
    ON external_chat_hosts (last_seen);

-- +migrate Dialect sqlite
ALTER TABLE external_agent_runs ADD COLUMN prompt TEXT NOT NULL DEFAULT '';
ALTER TABLE external_agent_runs ADD COLUMN query TEXT NOT NULL DEFAULT '';
ALTER TABLE external_agent_runs ADD COLUMN sequence INTEGER NOT NULL DEFAULT 0;
ALTER TABLE external_agent_runs ADD COLUMN history_ext TEXT;
ALTER TABLE external_agent_runs ADD COLUMN host_id VARCHAR(128) NOT NULL DEFAULT '';
ALTER TABLE external_agent_runs ADD COLUMN lease_token VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE external_agent_runs ADD COLUMN lease_expires_at DATETIME;
ALTER TABLE external_agent_runs ADD COLUMN claimed_at DATETIME;
ALTER TABLE external_agent_runs ADD COLUMN last_heartbeat_at DATETIME;
ALTER TABLE external_agent_runs ADD COLUMN stop_requested BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE external_agent_runs ADD COLUMN claim_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE external_agent_runs ADD COLUMN next_event_sequence INTEGER NOT NULL DEFAULT 0;
ALTER TABLE external_agent_runs ADD COLUMN completed_at DATETIME;

CREATE INDEX IF NOT EXISTS idx_external_agent_runs_history_id
    ON external_agent_runs (history_id);
CREATE INDEX IF NOT EXISTS idx_external_agent_runs_actor_user_id
    ON external_agent_runs (actor_user_id);
CREATE INDEX IF NOT EXISTS idx_external_agent_runs_claim
    ON external_agent_runs (actor_user_id, provider, status, lease_expires_at, created_at);

CREATE TABLE IF NOT EXISTS external_chat_run_events (
    id VARCHAR(64) PRIMARY KEY,
    run_id VARCHAR(36) NOT NULL,
    sequence INTEGER NOT NULL,
    type VARCHAR(32) NOT NULL,
    text TEXT,
    provider_thread_id VARCHAR(128) NOT NULL DEFAULT '',
    error_message TEXT,
    created_at DATETIME NOT NULL,
    CONSTRAINT uk_external_chat_run_event_sequence UNIQUE (run_id, sequence)
);
CREATE INDEX IF NOT EXISTS idx_external_chat_run_events_run_id
    ON external_chat_run_events (run_id);

CREATE TABLE IF NOT EXISTS external_chat_hosts (
    actor_user_id VARCHAR(255) NOT NULL,
    provider VARCHAR(32) NOT NULL,
    host_id VARCHAR(128) NOT NULL,
    installed BOOLEAN NOT NULL,
    ready BOOLEAN NOT NULL,
    unavailable_reason VARCHAR(512) NOT NULL DEFAULT '',
    last_seen DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    PRIMARY KEY (actor_user_id, provider, host_id)
);
CREATE INDEX IF NOT EXISTS idx_external_chat_hosts_last_seen
    ON external_chat_hosts (last_seen);
