-- +migrate Dialect postgres
ALTER TABLE conversations
    ADD COLUMN IF NOT EXISTS chat_executor VARCHAR(32) NOT NULL DEFAULT 'lazymind';

-- +migrate Dialect sqlite
ALTER TABLE conversations
    ADD COLUMN chat_executor VARCHAR(32) NOT NULL DEFAULT 'lazymind';
