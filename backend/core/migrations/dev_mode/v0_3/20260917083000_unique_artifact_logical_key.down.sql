-- +migrate Dialect postgres
DROP INDEX IF EXISTS uk_artifacts_owner_logical_key;

-- +migrate Dialect sqlite
DROP INDEX IF EXISTS uk_artifacts_owner_logical_key;
