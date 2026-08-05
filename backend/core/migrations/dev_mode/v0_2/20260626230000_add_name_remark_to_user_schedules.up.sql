-- +migrate Dialect postgres
ALTER TABLE user_schedules
    ADD COLUMN IF NOT EXISTS name      VARCHAR(128) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS remark    TEXT         NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS run_count INT          NOT NULL DEFAULT 0;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
