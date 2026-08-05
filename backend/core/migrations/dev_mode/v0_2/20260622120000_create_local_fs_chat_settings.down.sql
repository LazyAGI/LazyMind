-- 20260622120000_create_local_fs_chat_settings
-- +migrate Down
-- +migrate Dialect postgres

DROP TABLE IF EXISTS public.local_fs_chat_settings;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
