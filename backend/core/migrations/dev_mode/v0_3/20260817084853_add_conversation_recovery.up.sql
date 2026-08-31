-- +migrate Dialect postgres
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS archived_at TIMESTAMP NULL;
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS archive_folder_id VARCHAR(36) NULL;
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS trash_expires_at TIMESTAMP NULL;

CREATE TABLE IF NOT EXISTS conversation_archive_folders (
    id VARCHAR(36) PRIMARY KEY,
    user_id VARCHAR(255) NOT NULL,
    name VARCHAR(255) NOT NULL,
    normalized_name VARCHAR(255) NOT NULL,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    CONSTRAINT uk_conversation_archive_folders_user_name
        UNIQUE (user_id, normalized_name)
);

CREATE INDEX IF NOT EXISTS idx_conversations_user_lifecycle
    ON conversations(create_user_id, deleted_at, archived_at, is_task_conv, updated_at);
CREATE INDEX IF NOT EXISTS idx_conversations_user_archive_folder
    ON conversations(create_user_id, archive_folder_id, archived_at);

ALTER TABLE plugin_drafts ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMP NULL;
ALTER TABLE plugin_drafts ADD COLUMN IF NOT EXISTS trash_expires_at TIMESTAMP NULL;
ALTER TABLE plugin_drafts ADD COLUMN IF NOT EXISTS published_status_before_trash VARCHAR(16) NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_plugin_drafts_user_trash
    ON plugin_drafts(created_by, deleted_at, trash_expires_at);
ALTER TABLE skills ADD COLUMN IF NOT EXISTS trash_expires_at TIMESTAMP NULL;
ALTER TABLE task_center_tasks ADD COLUMN IF NOT EXISTS archived_reason VARCHAR(32) NOT NULL DEFAULT '';
UPDATE conversations SET trash_expires_at = CURRENT_TIMESTAMP + INTERVAL '30 days'
    WHERE deleted_at IS NOT NULL AND trash_expires_at IS NULL;
UPDATE skills SET trash_expires_at = CURRENT_TIMESTAMP + INTERVAL '30 days'
    WHERE deleted_at IS NOT NULL AND trash_expires_at IS NULL;
DROP INDEX IF EXISTS idx_plugin_drafts_user_plugin_id;
CREATE UNIQUE INDEX IF NOT EXISTS idx_plugin_drafts_user_plugin_id
    ON plugin_drafts(created_by, plugin_id)
    WHERE plugin_id != '' AND deleted_at IS NULL;

-- +migrate Dialect sqlite
ALTER TABLE conversations ADD COLUMN archived_at DATETIME NULL;
ALTER TABLE conversations ADD COLUMN archive_folder_id VARCHAR(36) NULL;
ALTER TABLE conversations ADD COLUMN trash_expires_at DATETIME NULL;

CREATE TABLE IF NOT EXISTS conversation_archive_folders (
    id VARCHAR(36) PRIMARY KEY,
    user_id VARCHAR(255) NOT NULL,
    name VARCHAR(255) NOT NULL,
    normalized_name VARCHAR(255) NOT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    CONSTRAINT uk_conversation_archive_folders_user_name
        UNIQUE (user_id, normalized_name)
);

CREATE INDEX IF NOT EXISTS idx_conversations_user_lifecycle
    ON conversations(create_user_id, deleted_at, archived_at, is_task_conv, updated_at);
CREATE INDEX IF NOT EXISTS idx_conversations_user_archive_folder
    ON conversations(create_user_id, archive_folder_id, archived_at);

ALTER TABLE plugin_drafts ADD COLUMN deleted_at DATETIME NULL;
ALTER TABLE plugin_drafts ADD COLUMN trash_expires_at DATETIME NULL;
ALTER TABLE plugin_drafts ADD COLUMN published_status_before_trash VARCHAR(16) NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_plugin_drafts_user_trash
    ON plugin_drafts(created_by, deleted_at, trash_expires_at);
ALTER TABLE skills ADD COLUMN trash_expires_at DATETIME NULL;
ALTER TABLE task_center_tasks ADD COLUMN archived_reason VARCHAR(32) NOT NULL DEFAULT '';
UPDATE conversations SET trash_expires_at = datetime('now', '+30 days')
    WHERE deleted_at IS NOT NULL AND trash_expires_at IS NULL;
UPDATE skills SET trash_expires_at = datetime('now', '+30 days')
    WHERE deleted_at IS NOT NULL AND trash_expires_at IS NULL;
DROP INDEX IF EXISTS idx_plugin_drafts_user_plugin_id;
CREATE UNIQUE INDEX IF NOT EXISTS idx_plugin_drafts_user_plugin_id
    ON plugin_drafts(created_by, plugin_id)
    WHERE plugin_id != '' AND deleted_at IS NULL;
