-- +migrate Dialect postgres
ALTER TABLE plugins
    ADD COLUMN IF NOT EXISTS source_skill_id VARCHAR(36) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS source_skill_name VARCHAR(255) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS source_skill_revision_id VARCHAR(36) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS source_skill_revision_no BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS source_skill_tree_hash VARCHAR(64) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS source_draft_id VARCHAR(36) NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_plugins_source_skill
    ON plugins(source_skill_id);

-- +migrate Dialect sqlite
ALTER TABLE plugins ADD COLUMN source_skill_id VARCHAR(36) NOT NULL DEFAULT '';
ALTER TABLE plugins ADD COLUMN source_skill_name VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE plugins ADD COLUMN source_skill_revision_id VARCHAR(36) NOT NULL DEFAULT '';
ALTER TABLE plugins ADD COLUMN source_skill_revision_no BIGINT NOT NULL DEFAULT 0;
ALTER TABLE plugins ADD COLUMN source_skill_tree_hash VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE plugins ADD COLUMN source_draft_id VARCHAR(36) NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_plugins_source_skill
    ON plugins(source_skill_id);
