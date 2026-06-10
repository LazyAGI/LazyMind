-- 20260608120000_add_eval_set_item_algorithm_reference_context
-- +migrate Up

ALTER TABLE public.eval_set_items
    ADD COLUMN IF NOT EXISTS algorithm_reference_context text DEFAULT ''::text NOT NULL;

UPDATE public.eval_set_items
SET algorithm_reference_context = trim(both from replace(reference_context, E'\r\n', E'\n'))
WHERE algorithm_reference_context = '';
