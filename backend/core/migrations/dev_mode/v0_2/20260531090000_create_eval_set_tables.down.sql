-- 20260531090000_create_eval_set_tables
-- +migrate Down
-- +migrate Dialect postgres

DROP TABLE IF EXISTS public.eval_set_items CASCADE;
DROP TABLE IF EXISTS public.eval_sets;
DROP TABLE IF EXISTS public.eval_set_shards;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
