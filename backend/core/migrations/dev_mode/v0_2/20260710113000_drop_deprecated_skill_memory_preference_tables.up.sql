-- +migrate Dialect postgres
DROP TABLE IF EXISTS public.resource_versions CASCADE;
DROP TABLE IF EXISTS public.resource_suggestions CASCADE;
DROP TABLE IF EXISTS public.skill_resources CASCADE;
DROP TABLE IF EXISTS public.system_user_preferences CASCADE;
DROP TABLE IF EXISTS public.system_memories CASCADE;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
