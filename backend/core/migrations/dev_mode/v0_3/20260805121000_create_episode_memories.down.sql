-- 20260805121000_create_episode_memories
-- +migrate Down
-- +migrate Dialect postgres
DROP TABLE IF EXISTS public.episode_memories;

-- +migrate Dialect sqlite
DROP TABLE IF EXISTS episode_memories;
