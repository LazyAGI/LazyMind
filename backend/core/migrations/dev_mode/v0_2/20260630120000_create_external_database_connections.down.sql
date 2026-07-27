-- 20260630120000_create_external_database_connections
-- +migrate Down
-- +migrate Dialect postgres

DROP TABLE IF EXISTS public.external_database_connections;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
