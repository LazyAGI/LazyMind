DROP INDEX IF EXISTS idx_plugin_drafts_user_plugin_id;
ALTER TABLE plugin_drafts DROP COLUMN IF EXISTS plugin_id;
