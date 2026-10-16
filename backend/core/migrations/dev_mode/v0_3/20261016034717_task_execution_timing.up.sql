-- +migrate Dialect postgres
CREATE TABLE task_execution_timings (
    id VARCHAR(128) PRIMARY KEY,
    run_id VARCHAR(128) NOT NULL,
    conversation_id VARCHAR(36) NOT NULL,
    owner_user_id VARCHAR(255) NOT NULL,
    history_id VARCHAR(128) NOT NULL,
    workflow_session_id VARCHAR(36) NOT NULL DEFAULT '',
    status VARCHAR(24) NOT NULL,
    revision BIGINT NOT NULL DEFAULT 1,
    requested_at TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ
);
CREATE INDEX idx_task_execution_scope ON task_execution_timings(owner_user_id, conversation_id, run_id, requested_at);
CREATE INDEX idx_task_execution_history ON task_execution_timings(history_id);
CREATE INDEX idx_task_execution_workflow ON task_execution_timings(workflow_session_id, requested_at);
CREATE TABLE task_attempt_timings (
    id VARCHAR(255) PRIMARY KEY,
    execution_id VARCHAR(128) NOT NULL,
    started_at TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ
);
CREATE INDEX idx_task_attempt_execution ON task_attempt_timings(execution_id);

-- +migrate Dialect sqlite
CREATE TABLE task_execution_timings (
    id VARCHAR(128) PRIMARY KEY,
    run_id VARCHAR(128) NOT NULL,
    conversation_id VARCHAR(36) NOT NULL,
    owner_user_id VARCHAR(255) NOT NULL,
    history_id VARCHAR(128) NOT NULL,
    workflow_session_id VARCHAR(36) NOT NULL DEFAULT '',
    status VARCHAR(24) NOT NULL,
    revision BIGINT NOT NULL DEFAULT 1,
    requested_at DATETIME NOT NULL,
    finished_at DATETIME
);
CREATE INDEX idx_task_execution_scope ON task_execution_timings(owner_user_id, conversation_id, run_id, requested_at);
CREATE INDEX idx_task_execution_history ON task_execution_timings(history_id);
CREATE INDEX idx_task_execution_workflow ON task_execution_timings(workflow_session_id, requested_at);
CREATE TABLE task_attempt_timings (
    id VARCHAR(255) PRIMARY KEY,
    execution_id VARCHAR(128) NOT NULL,
    started_at DATETIME NOT NULL,
    finished_at DATETIME
);
CREATE INDEX idx_task_attempt_execution ON task_attempt_timings(execution_id);
