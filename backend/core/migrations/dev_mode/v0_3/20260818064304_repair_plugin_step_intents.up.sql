-- +migrate Dialect postgres
CREATE TABLE IF NOT EXISTS plugin_step_intents (
    id             VARCHAR(36) PRIMARY KEY,
    session_id     VARCHAR(36) NOT NULL,
    step_id        VARCHAR(64) NOT NULL,
    intent_context TEXT        NOT NULL DEFAULT '{}',
    updated_at     TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS uk_plugin_step_intent
    ON plugin_step_intents (session_id, step_id);

-- +migrate Dialect sqlite
CREATE TABLE IF NOT EXISTS plugin_step_intents (
    id             VARCHAR(36) PRIMARY KEY,
    session_id     VARCHAR(36) NOT NULL,
    step_id        VARCHAR(64) NOT NULL,
    intent_context TEXT        NOT NULL DEFAULT '{}',
    updated_at     DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS uk_plugin_step_intent
    ON plugin_step_intents (session_id, step_id);
