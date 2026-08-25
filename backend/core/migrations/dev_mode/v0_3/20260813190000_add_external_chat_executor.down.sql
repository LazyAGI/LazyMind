-- +migrate Dialect postgres
ALTER TABLE conversations DROP COLUMN IF EXISTS chat_executor;

-- +migrate Dialect sqlite
ALTER TABLE conversations DROP COLUMN chat_executor;
