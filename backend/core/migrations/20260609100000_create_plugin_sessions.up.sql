-- Plugin sessions: one row per plugin session tied to a conversation.
CREATE TABLE plugin_sessions (
    id               VARCHAR(36)  PRIMARY KEY,
    conversation_id  VARCHAR(36)  NOT NULL,
    history_id       VARCHAR(36),
    plugin_id        VARCHAR(64)  NOT NULL,
    current_step_id  VARCHAR(64),
    meta             JSONB        NOT NULL DEFAULT '{}',
    create_user_id   VARCHAR(255),
    created_at       TIMESTAMP    NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMP    NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_plugin_sessions_conversation ON plugin_sessions(conversation_id);
CREATE INDEX idx_plugin_sessions_user ON plugin_sessions(create_user_id);

-- Plugin session steps: one row per step execution attempt (retries create new rows).
CREATE TABLE plugin_session_steps (
    id             VARCHAR(36)  PRIMARY KEY,
    session_id     VARCHAR(36)  NOT NULL REFERENCES plugin_sessions(id),
    step           VARCHAR(64)  NOT NULL,
    step_mode      VARCHAR(16)  NOT NULL,
    step_status    VARCHAR(16)  NOT NULL DEFAULT 'running',
    last_heartbeat TIMESTAMP    NOT NULL DEFAULT NOW(),
    workspace_path VARCHAR(512) NOT NULL,
    created_at     TIMESTAMP    NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMP    NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_plugin_session_steps_session ON plugin_session_steps(session_id);
CREATE INDEX idx_plugin_session_steps_step    ON plugin_session_steps(session_id, step);

-- Plugin session step checkpoints: incremental progress for long-running steps.
CREATE TABLE plugin_session_step_checkpoints (
    id              VARCHAR(36)  PRIMARY KEY,
    step_exec_id    VARCHAR(36)  NOT NULL REFERENCES plugin_session_steps(id),
    sequence        INT          NOT NULL,
    completed_count INT          NOT NULL DEFAULT 0,
    total_count     INT          NOT NULL DEFAULT 0,
    partial_results JSONB        NOT NULL DEFAULT '[]',
    phase_note      TEXT,
    created_at      TIMESTAMP    NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_checkpoints_step_exec ON plugin_session_step_checkpoints(step_exec_id);

-- Plugin session artifacts: outputs from step executions.
CREATE TABLE plugin_session_artifacts (
    id           VARCHAR(36)  PRIMARY KEY,
    session_id   VARCHAR(36)  NOT NULL REFERENCES plugin_sessions(id),
    step_exec_id VARCHAR(36)  NOT NULL REFERENCES plugin_session_steps(id),
    artifact_id  VARCHAR(64)  NOT NULL,
    value        JSONB        NOT NULL,
    created_at   TIMESTAMP    NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_artifacts_session    ON plugin_session_artifacts(session_id, artifact_id);
CREATE INDEX idx_artifacts_step_exec  ON plugin_session_artifacts(step_exec_id);
