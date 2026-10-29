-- Match canonical dataset (255), document (128), and segment (255) identities.
-- +migrate Dialect postgres
ALTER TABLE vocabulary_source_refs ALTER COLUMN dataset_id TYPE VARCHAR(255);
ALTER TABLE vocabulary_source_refs ALTER COLUMN document_id TYPE VARCHAR(128);
ALTER TABLE vocabulary_source_refs ALTER COLUMN segment_id TYPE VARCHAR(255);
-- +migrate Dialect sqlite
-- SQLite already stores these identifiers as unrestricted TEXT.
SELECT 1;
