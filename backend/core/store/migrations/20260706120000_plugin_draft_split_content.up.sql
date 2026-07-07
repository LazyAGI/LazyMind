-- Add split-content columns to plugin_drafts.
-- The original `content` column is kept for backward compatibility;
-- readers should prefer the new columns and fall back to `content` when they are empty.
ALTER TABLE plugin_drafts
    ADD COLUMN IF NOT EXISTS plugin_yaml_content TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS state_yaml_content   TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS scenario_content     TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS scripts_content      TEXT NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS generate_status      VARCHAR(16) NOT NULL DEFAULT '';

-- generate_status values: '' | 'generating' | 'done' | 'failed'
-- scripts_content is a JSON object: { "scripts/tools.py": "..." }
