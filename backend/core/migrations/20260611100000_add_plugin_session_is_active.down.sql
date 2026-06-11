ALTER TABLE plugin_sessions DROP COLUMN IF EXISTS is_active;
DROP INDEX IF EXISTS idx_plugin_sessions_active;
