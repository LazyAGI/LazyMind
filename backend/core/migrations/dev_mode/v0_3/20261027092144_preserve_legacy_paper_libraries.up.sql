-- +migrate Dialect postgres,sqlite
-- Restore the pre-normalization graph after the immutable entity migration.
CREATE TABLE IF NOT EXISTS paper_libraries (
 dataset_id VARCHAR(255) NOT NULL PRIMARY KEY,
 data_json JSON NOT NULL,
 revision BIGINT NOT NULL DEFAULT 0 CHECK(revision >= 0),
 created_at TIMESTAMP NOT NULL,
 updated_at TIMESTAMP NOT NULL
);
INSERT INTO paper_libraries (dataset_id,data_json,revision,created_at,updated_at)
 SELECT dataset_id,data_json,revision,created_at,updated_at
 FROM paper_library_migration_backups WHERE TRUE
 ON CONFLICT(dataset_id) DO NOTHING;
DROP TABLE paper_library_migration_backups;

CREATE TABLE IF NOT EXISTS paper_library_legacy_states (
 dataset_id VARCHAR(255) NOT NULL PRIMARY KEY,
 extras_json JSON NOT NULL,
 migrated_at TIMESTAMP,
 updated_at TIMESTAMP NOT NULL
);

-- +migrate Dialect postgres
UPDATE datasets SET scene='academic_papers'
 WHERE scene='general' AND ext->>'library_kind'='paper';

-- +migrate Dialect sqlite
UPDATE datasets SET scene='academic_papers'
 WHERE scene='general' AND CASE WHEN json_valid(ext) THEN json_extract(ext,'$.library_kind') ELSE NULL END='paper';
