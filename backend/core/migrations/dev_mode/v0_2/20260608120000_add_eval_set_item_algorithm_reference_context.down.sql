-- 20260608120000_add_eval_set_item_algorithm_reference_context
-- +migrate Down
-- +migrate Dialect postgres

ALTER TABLE public.eval_set_items
    DROP COLUMN IF EXISTS algorithm_reference_context;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
