-- 20260604120000_recreate_eval_sets_dataset_ids
-- +migrate Down
-- +migrate Dialect postgres
--
-- Irreversible breaking development-data reset. Old eval_sets.dataset_id data is not restored.

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
