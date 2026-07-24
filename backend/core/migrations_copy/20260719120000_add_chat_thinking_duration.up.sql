ALTER TABLE chat_histories
    ADD COLUMN IF NOT EXISTS thinking_duration_s BIGINT NOT NULL DEFAULT 0;

ALTER TABLE multi_answers_chat_histories
    ADD COLUMN IF NOT EXISTS thinking_duration_s BIGINT NOT NULL DEFAULT 0;
