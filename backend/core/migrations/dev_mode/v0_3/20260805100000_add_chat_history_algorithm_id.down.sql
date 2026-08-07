DROP INDEX IF EXISTS idx_chat_histories_algorithm_create_time;
ALTER TABLE chat_histories DROP COLUMN IF EXISTS algorithm_id;
