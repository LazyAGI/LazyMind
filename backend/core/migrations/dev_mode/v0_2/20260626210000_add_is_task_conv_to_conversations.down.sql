-- +migrate Dialect postgres
ALTER TABLE conversations DROP COLUMN IF EXISTS is_task_conv;
DROP INDEX IF EXISTS idx_conversations_is_task_conv;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
