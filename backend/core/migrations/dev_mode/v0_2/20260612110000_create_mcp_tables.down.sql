-- 20260612110000_create_mcp_tables
-- +migrate Down
-- +migrate Dialect postgres

DROP TABLE IF EXISTS public.mcp_server_tools;
DROP TABLE IF EXISTS public.mcp_servers;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
