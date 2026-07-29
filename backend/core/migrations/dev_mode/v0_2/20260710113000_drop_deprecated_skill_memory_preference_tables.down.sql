-- +migrate Dialect postgres
-- Deprecated resource entity/version/suggestion tables are intentionally not recreated.
-- New installations use skills/skill_* and personal_resource_* tables for editable resources.

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
