-- 20260610123000_create_user_disabled_tools
-- +migrate Down
-- +migrate Dialect postgres

DROP TABLE IF EXISTS public.user_disabled_tools;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
