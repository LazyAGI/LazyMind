ALTER TABLE conversation_artifacts
    DROP CONSTRAINT IF EXISTS chk_conversation_artifacts_content_type,
    DROP CONSTRAINT IF EXISTS chk_conversation_artifacts_filename;

DROP INDEX IF EXISTS idx_conversation_artifacts_owner_conversation_created;

CREATE INDEX IF NOT EXISTS idx_conversation_artifacts_conversation_id
    ON conversation_artifacts (conversation_id);
CREATE INDEX IF NOT EXISTS idx_conversation_artifacts_create_user_id
    ON conversation_artifacts (create_user_id);
