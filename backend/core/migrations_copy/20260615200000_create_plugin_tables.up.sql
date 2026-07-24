-- Create Plugin tables: plugin_sessions, plugin_session_steps, plugin_slot_revisions
-- Plugin sessions track one plugin workflow per conversation.
-- SubAgent tables (sub_agent_tasks / sub_agent_steps / sub_agent_artifacts) are reused unchanged.

CREATE TABLE IF NOT EXISTS plugin_sessions (
    id                  VARCHAR(36)  PRIMARY KEY,
    conversation_id     VARCHAR(36)  NOT NULL,
    plugin_id           VARCHAR(64)  NOT NULL,
    trigger_history_id  VARCHAR(36),
    status              VARCHAR(16)  NOT NULL DEFAULT 'active',
    current_step_id     VARCHAR(64),
    create_user_id      VARCHAR(255) NOT NULL DEFAULT '',
    created_at          TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at          TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_ps_conv        ON plugin_sessions(conversation_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_ps_conv_active ON plugin_sessions(conversation_id, status);

-- Each step execution instance maps to one sub_agent_tasks record.
CREATE TABLE IF NOT EXISTS plugin_session_steps (
    id          VARCHAR(36) PRIMARY KEY,
    session_id  VARCHAR(36) NOT NULL REFERENCES plugin_sessions(id),
    step_id     VARCHAR(64) NOT NULL,
    attempt     INT         NOT NULL DEFAULT 1,
    task_id     VARCHAR(36) NOT NULL,
    status      VARCHAR(16) NOT NULL DEFAULT 'pending',
    created_at  TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_pss_session ON plugin_session_steps(session_id, step_id, attempt);
CREATE INDEX IF NOT EXISTS idx_pss_task    ON plugin_session_steps(task_id);

-- Records each slot write produced by a step artifact.
-- list_index is NULL for single-cardinality slots; 0-based index for list slots.
CREATE TABLE IF NOT EXISTS plugin_slot_revisions (
    id           VARCHAR(36)  PRIMARY KEY,
    session_id   VARCHAR(36)  NOT NULL REFERENCES plugin_sessions(id),
    slot_id      VARCHAR(64)  NOT NULL,
    revision     INT          NOT NULL,
    list_index   INT,
    selected     BOOLEAN      NOT NULL DEFAULT TRUE,
    artifact_key VARCHAR(255) NOT NULL,
    step_id      VARCHAR(64)  NOT NULL,
    attempt      INT          NOT NULL,
    created_at   TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_psr_slot_rev ON plugin_slot_revisions(session_id, slot_id, revision);
CREATE INDEX        IF NOT EXISTS idx_psr_artifact  ON plugin_slot_revisions(artifact_key);
CREATE INDEX        IF NOT EXISTS idx_psr_session   ON plugin_slot_revisions(session_id, slot_id);
