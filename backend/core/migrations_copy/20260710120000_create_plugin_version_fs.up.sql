CREATE TABLE IF NOT EXISTS plugins (
    id VARCHAR(36) PRIMARY KEY,
    plugin_ref VARCHAR(512) NOT NULL UNIQUE,
    plugin_id VARCHAR(255) NOT NULL,
    owner_user_id VARCHAR(255) NOT NULL,
    owner_scope VARCHAR(128) NOT NULL,
    source_type VARCHAR(16) NOT NULL DEFAULT 'user',
    relative_root VARCHAR(1024) NOT NULL UNIQUE,
    name VARCHAR(255) NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    when_to_use TEXT NOT NULL DEFAULT '',
    head_revision_id VARCHAR(36),
    version BIGINT NOT NULL DEFAULT 0,
    status VARCHAR(16) NOT NULL DEFAULT 'active',
    contains_scripts BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_plugins_owner ON plugins(owner_user_id, status);

CREATE TABLE IF NOT EXISTS plugin_blobs (
    hash VARCHAR(64) PRIMARY KEY,
    size BIGINT NOT NULL,
    mime VARCHAR(128),
    file_type VARCHAR(32) NOT NULL DEFAULT 'unknown',
    is_binary BOOLEAN NOT NULL DEFAULT FALSE,
    content BYTEA NOT NULL,
    created_at TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS plugin_revisions (
    id VARCHAR(36) PRIMARY KEY,
    plugin_resource_id VARCHAR(36) NOT NULL,
    parent_revision_id VARCHAR(36),
    revision_no BIGINT NOT NULL,
    tree_hash VARCHAR(64) NOT NULL,
    message TEXT NOT NULL DEFAULT '',
    created_by VARCHAR(255),
    created_at TIMESTAMP NOT NULL,
    UNIQUE(plugin_resource_id, revision_no)
);

CREATE INDEX IF NOT EXISTS idx_plugin_revisions_resource ON plugin_revisions(plugin_resource_id);

CREATE TABLE IF NOT EXISTS plugin_revision_entries (
    revision_id VARCHAR(36) NOT NULL,
    path VARCHAR(1024) NOT NULL,
    entry_type VARCHAR(16) NOT NULL DEFAULT 'file',
    blob_hash VARCHAR(64),
    size BIGINT NOT NULL DEFAULT 0,
    mime VARCHAR(128),
    file_type VARCHAR(32) NOT NULL DEFAULT 'unknown',
    is_binary BOOLEAN NOT NULL DEFAULT FALSE,
    mode INT NOT NULL DEFAULT 420,
    PRIMARY KEY(revision_id, path)
);

CREATE TABLE IF NOT EXISTS user_plugin_settings (
    user_id VARCHAR(255) NOT NULL,
    plugin_ref VARCHAR(512) NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at TIMESTAMP NOT NULL,
    PRIMARY KEY(user_id, plugin_ref)
);

ALTER TABLE plugin_sessions ADD COLUMN IF NOT EXISTS plugin_ref VARCHAR(512) NOT NULL DEFAULT '';
ALTER TABLE plugin_sessions ADD COLUMN IF NOT EXISTS plugin_revision_id VARCHAR(36) NOT NULL DEFAULT '';
ALTER TABLE plugin_sessions ADD COLUMN IF NOT EXISTS plugin_revision_no BIGINT NOT NULL DEFAULT 0;
ALTER TABLE plugin_sessions ADD COLUMN IF NOT EXISTS plugin_tree_hash VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE plugin_sessions ADD COLUMN IF NOT EXISTS plugin_remote_root VARCHAR(1024) NOT NULL DEFAULT '';
ALTER TABLE plugin_drafts ADD COLUMN IF NOT EXISTS base_revision_id VARCHAR(36) NOT NULL DEFAULT '';
