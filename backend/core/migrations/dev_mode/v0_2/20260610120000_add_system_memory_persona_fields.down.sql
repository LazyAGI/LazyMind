-- +migrate Dialect postgres
ALTER TABLE public.system_memories
    DROP COLUMN IF EXISTS response_style,
    DROP COLUMN IF EXISTS user_address,
    DROP COLUMN IF EXISTS agent_persona;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
