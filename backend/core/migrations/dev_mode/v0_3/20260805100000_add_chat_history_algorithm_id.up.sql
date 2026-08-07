-- Already applied on this environment; keep the catalog aligned with schema_migrations.
ALTER TABLE chat_histories
    ADD COLUMN algorithm_id VARCHAR(64);

CREATE INDEX IF NOT EXISTS idx_chat_histories_algorithm_create_time
    ON chat_histories (algorithm_id, create_time);
