DROP TABLE IF EXISTS plugin_repair_runs;
DROP TABLE IF EXISTS plugin_generation_analyses;
ALTER TABLE plugin_drafts DROP COLUMN IF EXISTS source_analysis_id;
ALTER TABLE plugin_drafts DROP COLUMN IF EXISTS source_skill_tree_hash;
ALTER TABLE plugin_drafts DROP COLUMN IF EXISTS source_skill_revision_no;
ALTER TABLE plugin_drafts DROP COLUMN IF EXISTS source_skill_revision_id;
ALTER TABLE plugin_drafts ALTER COLUMN generate_status TYPE VARCHAR(16);
