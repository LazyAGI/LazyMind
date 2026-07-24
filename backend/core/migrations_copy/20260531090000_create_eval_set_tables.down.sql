-- 20260531090000_create_eval_set_tables
-- +migrate Down

DROP TABLE IF EXISTS public.eval_set_items CASCADE;
DROP TABLE IF EXISTS public.eval_sets;
DROP TABLE IF EXISTS public.eval_set_shards;
