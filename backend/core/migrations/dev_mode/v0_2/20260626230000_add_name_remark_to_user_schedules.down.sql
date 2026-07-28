-- +migrate Dialect postgres
ALTER TABLE user_schedules
    DROP COLUMN IF EXISTS name,
    DROP COLUMN IF EXISTS remark,
    DROP COLUMN IF EXISTS run_count;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
