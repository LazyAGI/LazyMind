DROP INDEX IF EXISTS idx_conversation_artifacts_conversation_id;
DROP INDEX IF EXISTS idx_conversation_artifacts_create_user_id;

CREATE INDEX IF NOT EXISTS idx_conversation_artifacts_owner_conversation_created
    ON conversation_artifacts (create_user_id, conversation_id, created_at);

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'chk_conversation_artifacts_content_type'
          AND conrelid = 'conversation_artifacts'::regclass
    ) THEN
        ALTER TABLE conversation_artifacts
            ADD CONSTRAINT chk_conversation_artifacts_content_type
            CHECK (content_type IN ('text', 'json'));
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'chk_conversation_artifacts_filename'
          AND conrelid = 'conversation_artifacts'::regclass
    ) THEN
        ALTER TABLE conversation_artifacts
            ADD CONSTRAINT chk_conversation_artifacts_filename
            CHECK (length(btrim(filename)) > 0);
    END IF;
END $$;
