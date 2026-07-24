ALTER TABLE multi_answers_chat_histories
    DROP COLUMN IF EXISTS thinking_duration_s;

ALTER TABLE chat_histories
    DROP COLUMN IF EXISTS thinking_duration_s;
