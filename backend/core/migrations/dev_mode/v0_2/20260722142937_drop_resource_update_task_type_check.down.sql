-- +migrate Dialect postgres
-- The task_type enum-style check is intentionally not restored.
-- Restoring it would make every new application task type require a schema migration.

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
