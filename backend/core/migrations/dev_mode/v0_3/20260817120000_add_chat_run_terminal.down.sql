-- +migrate Dialect postgres
DROP INDEX IF EXISTS idx_multi_answers_chat_histories_run_id;
ALTER TABLE multi_answers_chat_histories DROP COLUMN IF EXISTS run_terminal;
ALTER TABLE multi_answers_chat_histories DROP COLUMN IF EXISTS run_status;
ALTER TABLE multi_answers_chat_histories DROP COLUMN IF EXISTS run_id;
DROP INDEX IF EXISTS idx_chat_histories_run_id;
ALTER TABLE chat_histories DROP COLUMN IF EXISTS run_terminal;
ALTER TABLE chat_histories DROP COLUMN IF EXISTS run_status;
ALTER TABLE chat_histories DROP COLUMN IF EXISTS run_id;

-- +migrate Dialect sqlite
DROP INDEX IF EXISTS idx_multi_answers_chat_histories_run_id;
ALTER TABLE multi_answers_chat_histories DROP COLUMN run_terminal;
ALTER TABLE multi_answers_chat_histories DROP COLUMN run_status;
ALTER TABLE multi_answers_chat_histories DROP COLUMN run_id;
DROP INDEX IF EXISTS idx_chat_histories_run_id;
ALTER TABLE chat_histories DROP COLUMN run_terminal;
ALTER TABLE chat_histories DROP COLUMN run_status;
ALTER TABLE chat_histories DROP COLUMN run_id;
