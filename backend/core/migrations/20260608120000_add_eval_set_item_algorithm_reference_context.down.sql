-- 20260608120000_add_eval_set_item_algorithm_reference_context
-- +migrate Down

ALTER TABLE public.eval_set_items
    DROP COLUMN IF EXISTS algorithm_reference_context;
