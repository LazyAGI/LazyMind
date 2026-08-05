-- +migrate Dialect postgres
ALTER TABLE default_model_providers
    DROP COLUMN IF EXISTS description_i18n;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
