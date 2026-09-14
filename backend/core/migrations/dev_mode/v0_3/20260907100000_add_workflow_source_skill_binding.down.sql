-- +migrate Dialect postgres
DROP INDEX IF EXISTS idx_plugins_source_skill;
ALTER TABLE plugins
    DROP COLUMN IF EXISTS source_draft_id,
    DROP COLUMN IF EXISTS source_skill_tree_hash,
    DROP COLUMN IF EXISTS source_skill_revision_no,
    DROP COLUMN IF EXISTS source_skill_revision_id,
    DROP COLUMN IF EXISTS source_skill_name,
    DROP COLUMN IF EXISTS source_skill_id;

-- +migrate Dialect sqlite
DROP INDEX IF EXISTS idx_plugins_source_skill;
ALTER TABLE plugins DROP COLUMN source_draft_id;
ALTER TABLE plugins DROP COLUMN source_skill_tree_hash;
ALTER TABLE plugins DROP COLUMN source_skill_revision_no;
ALTER TABLE plugins DROP COLUMN source_skill_revision_id;
ALTER TABLE plugins DROP COLUMN source_skill_name;
ALTER TABLE plugins DROP COLUMN source_skill_id;
