CREATE TABLE IF NOT EXISTS conversation_artifacts (
    id varchar(36) PRIMARY KEY,
    conversation_id varchar(36) NOT NULL,
    history_id varchar(36) NOT NULL,
    filename varchar(255) NOT NULL,
    slot varchar(255) NOT NULL,
    content_type varchar(32) NOT NULL,
    value jsonb NOT NULL,
    caption text,
    create_user_id varchar(255) NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_conversation_artifacts_conversation_id
    ON conversation_artifacts (conversation_id);
CREATE INDEX IF NOT EXISTS idx_conversation_artifacts_history_id
    ON conversation_artifacts (history_id);
CREATE INDEX IF NOT EXISTS idx_conversation_artifacts_create_user_id
    ON conversation_artifacts (create_user_id);
