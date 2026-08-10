-- 20260805120000_create_memory_current_entries
-- +migrate Down
-- +migrate Dialect postgres
DROP TABLE IF EXISTS public.memory_current_entries;

-- +migrate Dialect sqlite
DROP TABLE IF EXISTS memory_current_entries;
