-- +migrate Dialect postgres
ALTER TABLE plugin_human_artifacts
    ADD COLUMN IF NOT EXISTS draft_version BIGINT NOT NULL DEFAULT 1;

-- +migrate Dialect sqlite
ALTER TABLE plugin_human_artifacts
    ADD COLUMN draft_version INTEGER NOT NULL DEFAULT 1;
