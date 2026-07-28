-- 20260701120000_create_user_ui_preferences
-- +migrate Down
-- +migrate Dialect postgres

DROP TABLE IF EXISTS public.user_ui_preferences;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
