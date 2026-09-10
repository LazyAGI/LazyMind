-- +migrate Dialect postgres
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS history_order BIGINT NULL;

-- +migrate Dialect sqlite
ALTER TABLE conversations ADD COLUMN history_order INTEGER NULL;
