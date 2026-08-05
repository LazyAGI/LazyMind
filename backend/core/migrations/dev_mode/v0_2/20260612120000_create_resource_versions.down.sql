-- 20260612120000_create_resource_versions
-- +migrate Down
-- +migrate Dialect postgres

DROP TABLE IF EXISTS public.resource_versions;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
