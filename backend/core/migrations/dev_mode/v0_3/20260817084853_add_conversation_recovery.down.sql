-- +migrate Dialect postgres
DROP INDEX IF EXISTS idx_plugin_drafts_user_trash;
DROP INDEX IF EXISTS idx_plugin_drafts_user_plugin_id;
CREATE UNIQUE INDEX IF NOT EXISTS idx_plugin_drafts_user_plugin_id
    ON plugin_drafts(created_by, plugin_id) WHERE plugin_id != '';
ALTER TABLE task_center_tasks DROP COLUMN IF EXISTS archived_reason;
ALTER TABLE skills DROP COLUMN IF EXISTS trash_expires_at;
ALTER TABLE plugin_drafts DROP COLUMN IF EXISTS published_status_before_trash;
ALTER TABLE plugin_drafts DROP COLUMN IF EXISTS trash_expires_at;
ALTER TABLE plugin_drafts DROP COLUMN IF EXISTS deleted_at;
DROP INDEX IF EXISTS idx_conversations_user_archive_folder;
DROP INDEX IF EXISTS idx_conversations_user_lifecycle;
DROP TABLE IF EXISTS conversation_archive_folders;
ALTER TABLE conversations DROP COLUMN IF EXISTS archive_folder_id;
ALTER TABLE conversations DROP COLUMN IF EXISTS trash_expires_at;
ALTER TABLE conversations DROP COLUMN IF EXISTS archived_at;

-- +migrate Dialect sqlite
DROP INDEX IF EXISTS idx_plugin_drafts_user_trash;
DROP INDEX IF EXISTS idx_plugin_drafts_user_plugin_id;
CREATE UNIQUE INDEX IF NOT EXISTS idx_plugin_drafts_user_plugin_id
    ON plugin_drafts(created_by, plugin_id) WHERE plugin_id != '';
ALTER TABLE task_center_tasks DROP COLUMN archived_reason;
ALTER TABLE skills DROP COLUMN trash_expires_at;
ALTER TABLE plugin_drafts DROP COLUMN published_status_before_trash;
ALTER TABLE plugin_drafts DROP COLUMN trash_expires_at;
ALTER TABLE plugin_drafts DROP COLUMN deleted_at;
DROP INDEX IF EXISTS idx_conversations_user_archive_folder;
DROP INDEX IF EXISTS idx_conversations_user_lifecycle;
DROP TABLE IF EXISTS conversation_archive_folders;
ALTER TABLE conversations DROP COLUMN archive_folder_id;
ALTER TABLE conversations DROP COLUMN trash_expires_at;
ALTER TABLE conversations DROP COLUMN archived_at;
