-- +migrate Dialect postgres
DROP INDEX IF EXISTS uk_user_selected_models_shared_model;

ALTER TABLE user_selected_models DROP COLUMN IF EXISTS share;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
