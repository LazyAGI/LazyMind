-- 20260805100000_add_chat_history_algorithm_id
-- +migrate Up
-- +migrate Dialect postgres
ALTER TABLE public.chat_histories
    ADD COLUMN IF NOT EXISTS algorithm_id VARCHAR(64);

CREATE INDEX IF NOT EXISTS idx_chat_histories_algorithm_create_time
    ON public.chat_histories (algorithm_id, create_time);

-- +migrate Dialect sqlite
ALTER TABLE chat_histories ADD COLUMN algorithm_id varchar(64);
CREATE INDEX IF NOT EXISTS idx_chat_histories_algorithm_create_time
    ON chat_histories (algorithm_id, create_time);
