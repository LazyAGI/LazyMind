-- +migrate Dialect postgres
ALTER TABLE plugin_drafts
  DROP COLUMN IF EXISTS plugin_yaml_content,
  DROP COLUMN IF EXISTS state_yaml_content,
  DROP COLUMN IF EXISTS scenario_content,
  DROP COLUMN IF EXISTS scripts_content,
  DROP COLUMN IF EXISTS generate_status;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
