-- +migrate Dialect postgres
ALTER TABLE public.system_memories
    ADD COLUMN IF NOT EXISTS agent_persona text DEFAULT ''::text NOT NULL,
    ADD COLUMN IF NOT EXISTS user_address text DEFAULT ''::text NOT NULL,
    ADD COLUMN IF NOT EXISTS response_style text DEFAULT ''::text NOT NULL;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
