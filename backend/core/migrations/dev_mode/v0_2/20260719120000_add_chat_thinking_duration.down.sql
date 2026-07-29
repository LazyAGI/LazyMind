-- +migrate Dialect postgres
ALTER TABLE multi_answers_chat_histories
    DROP COLUMN IF EXISTS thinking_duration_s;

ALTER TABLE chat_histories
    DROP COLUMN IF EXISTS thinking_duration_s;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
