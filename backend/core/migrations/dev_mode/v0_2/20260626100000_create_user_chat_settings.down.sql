-- 20260626100000_create_user_chat_settings
-- +migrate Down
-- +migrate Dialect postgres

DROP TABLE IF EXISTS public.user_chat_settings;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
