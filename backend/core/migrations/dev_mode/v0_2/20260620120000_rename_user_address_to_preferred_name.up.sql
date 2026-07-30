-- +migrate Dialect postgres
ALTER TABLE public.system_user_preferences
    RENAME COLUMN user_address TO preferred_name;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
