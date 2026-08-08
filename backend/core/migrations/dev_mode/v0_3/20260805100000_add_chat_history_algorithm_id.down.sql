-- 20260805100000_add_chat_history_algorithm_id
-- +migrate Down
-- +migrate Dialect postgres
DROP INDEX IF EXISTS public.idx_chat_histories_algorithm_create_time;
ALTER TABLE public.chat_histories DROP COLUMN IF EXISTS algorithm_id;

-- +migrate Dialect sqlite
DROP INDEX IF EXISTS idx_chat_histories_algorithm_create_time;
ALTER TABLE chat_histories DROP COLUMN algorithm_id;
