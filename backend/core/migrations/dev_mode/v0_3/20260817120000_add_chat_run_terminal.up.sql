-- +migrate Dialect postgres
ALTER TABLE chat_histories ADD COLUMN IF NOT EXISTS run_id VARCHAR(64);
ALTER TABLE chat_histories ADD COLUMN IF NOT EXISTS run_status VARCHAR(32);
ALTER TABLE chat_histories ADD COLUMN IF NOT EXISTS run_terminal JSONB;
CREATE INDEX IF NOT EXISTS idx_chat_histories_run_id ON chat_histories(run_id);
ALTER TABLE multi_answers_chat_histories ADD COLUMN IF NOT EXISTS run_id VARCHAR(64);
ALTER TABLE multi_answers_chat_histories ADD COLUMN IF NOT EXISTS run_status VARCHAR(32);
ALTER TABLE multi_answers_chat_histories ADD COLUMN IF NOT EXISTS run_terminal JSONB;
CREATE INDEX IF NOT EXISTS idx_multi_answers_chat_histories_run_id ON multi_answers_chat_histories(run_id);

-- +migrate Dialect sqlite
ALTER TABLE chat_histories ADD COLUMN run_id TEXT;
ALTER TABLE chat_histories ADD COLUMN run_status TEXT;
ALTER TABLE chat_histories ADD COLUMN run_terminal TEXT;
CREATE INDEX IF NOT EXISTS idx_chat_histories_run_id ON chat_histories(run_id);
ALTER TABLE multi_answers_chat_histories ADD COLUMN run_id TEXT;
ALTER TABLE multi_answers_chat_histories ADD COLUMN run_status TEXT;
ALTER TABLE multi_answers_chat_histories ADD COLUMN run_terminal TEXT;
CREATE INDEX IF NOT EXISTS idx_multi_answers_chat_histories_run_id ON multi_answers_chat_histories(run_id);
