-- Revert: remove generate_error column from plugin_drafts.
ALTER TABLE plugin_drafts
    DROP COLUMN IF EXISTS generate_error;
