-- +migrate Dialect postgres
ALTER TABLE plugin_human_artifacts
    DROP COLUMN IF EXISTS draft_version;

-- +migrate Dialect sqlite
ALTER TABLE plugin_human_artifacts
    DROP COLUMN draft_version;
