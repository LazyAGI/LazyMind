-- +migrate Dialect postgres
ALTER TABLE conversation_artifacts
    DROP CONSTRAINT IF EXISTS chk_conversation_artifacts_content_type;

ALTER TABLE conversation_artifacts
    ADD CONSTRAINT chk_conversation_artifacts_content_type
    CHECK (content_type IN ('text', 'json')) NOT VALID;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
