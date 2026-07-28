-- +migrate Dialect postgres
DROP TABLE IF EXISTS public.user_selected_providers;
DROP SEQUENCE IF EXISTS public.user_selected_providers_id_seq;

ALTER TABLE user_model_providers
  DROP COLUMN capabilities,
  DROP COLUMN category;

ALTER TABLE default_model_providers
  DROP COLUMN capabilities,
  DROP COLUMN category;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
