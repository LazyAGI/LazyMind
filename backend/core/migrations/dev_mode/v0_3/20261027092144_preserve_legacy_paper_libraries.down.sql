-- +migrate Dialect postgres,sqlite
-- Keep the archive while the immutable entity down recreates its original table.
CREATE TABLE IF NOT EXISTS paper_library_migration_backups (
 dataset_id VARCHAR(255) NOT NULL PRIMARY KEY,
 data_json JSON NOT NULL,
 revision BIGINT NOT NULL DEFAULT 0 CHECK(revision >= 0),
 created_at TIMESTAMP NOT NULL,
 updated_at TIMESTAMP NOT NULL
);
INSERT INTO paper_library_migration_backups (dataset_id,data_json,revision,created_at,updated_at)
 SELECT dataset_id,data_json,revision,created_at,updated_at
 FROM paper_libraries WHERE TRUE
 ON CONFLICT(dataset_id) DO NOTHING;
DROP TABLE paper_libraries;
DROP TABLE paper_library_legacy_states;
