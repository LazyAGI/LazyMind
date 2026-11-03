-- +migrate Dialect postgres,sqlite
ALTER TABLE academic_expansions ADD COLUMN discovery_mode VARCHAR(32) NOT NULL DEFAULT 'legacy_mock';
ALTER TABLE academic_expansion_rounds ADD COLUMN discovery_mode VARCHAR(32) NOT NULL DEFAULT 'legacy_mock';
ALTER TABLE academic_expansions ADD COLUMN seed_snapshot_json JSON NOT NULL DEFAULT '[]';
ALTER TABLE academic_expansions ADD COLUMN discovery_json JSON NOT NULL DEFAULT '[]';
ALTER TABLE academic_paper_contents ADD COLUMN source_provider VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE academic_paper_contents ADD COLUMN source_kind VARCHAR(16) NOT NULL DEFAULT 'legacy';
ALTER TABLE academic_paper_contents ADD COLUMN source_version VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE academic_paper_contents ADD COLUMN source_key VARCHAR(128) NOT NULL DEFAULT 'legacy';
DROP INDEX uk_academic_content;
CREATE UNIQUE INDEX uk_academic_content ON academic_paper_contents(user_id, paper_id, parser_version, source_key);
