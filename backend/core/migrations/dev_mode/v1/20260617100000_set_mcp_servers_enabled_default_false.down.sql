-- 20260617100000_set_mcp_servers_enabled_default_false
-- +migrate Down

ALTER TABLE IF EXISTS public.mcp_servers
    ALTER COLUMN enabled SET DEFAULT true;
