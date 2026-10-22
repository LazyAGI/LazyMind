-- Creating the old unique index first deliberately rejects a lossy downgrade.
-- +migrate Dialect postgres,sqlite
CREATE UNIQUE INDEX uk_academic_content_downgrade ON academic_paper_contents(user_id, paper_id, parser_version);
DROP INDEX uk_academic_content;
DROP INDEX uk_academic_content_downgrade;
CREATE UNIQUE INDEX uk_academic_content ON academic_paper_contents(user_id, paper_id, parser_version);
ALTER TABLE academic_paper_contents DROP COLUMN source_key;
ALTER TABLE academic_paper_contents DROP COLUMN source_version;
ALTER TABLE academic_paper_contents DROP COLUMN source_kind;
ALTER TABLE academic_paper_contents DROP COLUMN source_provider;
ALTER TABLE academic_expansions DROP COLUMN discovery_json;
ALTER TABLE academic_expansions DROP COLUMN seed_snapshot_json;

ALTER TABLE academic_expansions DROP COLUMN discovery_mode;
ALTER TABLE academic_expansion_rounds DROP COLUMN discovery_mode;
