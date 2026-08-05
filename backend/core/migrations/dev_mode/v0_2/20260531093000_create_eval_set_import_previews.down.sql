-- 20260531093000_create_eval_set_import_previews
-- +migrate Down
-- +migrate Dialect postgres

DROP TABLE IF EXISTS public.eval_set_import_previews;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
