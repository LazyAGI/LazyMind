-- 20260531100000_create_async_jobs
-- +migrate Down
-- +migrate Dialect postgres

DROP TABLE IF EXISTS public.async_jobs;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
