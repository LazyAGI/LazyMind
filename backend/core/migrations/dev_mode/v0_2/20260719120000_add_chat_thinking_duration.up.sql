-- +migrate Dialect postgres
ALTER TABLE chat_histories
    ADD COLUMN IF NOT EXISTS thinking_duration_s BIGINT NOT NULL DEFAULT 0;

ALTER TABLE multi_answers_chat_histories
    ADD COLUMN IF NOT EXISTS thinking_duration_s BIGINT NOT NULL DEFAULT 0;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
