-- Fail rather than truncate any identities added after the upgrade.
-- +migrate Dialect postgres
ALTER TABLE vocabulary_source_refs ALTER COLUMN dataset_id TYPE VARCHAR(64);
ALTER TABLE vocabulary_source_refs ALTER COLUMN document_id TYPE VARCHAR(64);
ALTER TABLE vocabulary_source_refs ALTER COLUMN segment_id TYPE VARCHAR(64);
-- +migrate Dialect sqlite
SELECT 1;
