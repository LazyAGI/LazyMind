-- 20260617100000_set_mcp_servers_enabled_default_false
-- +migrate Up
-- +migrate Dialect postgres

ALTER TABLE IF EXISTS public.mcp_servers
    ALTER COLUMN enabled SET DEFAULT false;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
