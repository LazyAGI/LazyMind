-- +migrate Dialect postgres
ALTER TABLE conversations DROP COLUMN IF EXISTS history_order;

-- +migrate Dialect sqlite
ALTER TABLE conversations DROP COLUMN history_order;
