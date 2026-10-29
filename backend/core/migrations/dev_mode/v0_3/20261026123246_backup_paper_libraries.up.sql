-- +migrate Dialect postgres,sqlite
-- Copy the legacy graph before the immutable entity migration drops its table.
-- CREATE IF NOT EXISTS also lets databases which already applied that migration
-- pick up this late-added, lower-numbered migration without failing.
CREATE TABLE IF NOT EXISTS paper_libraries (
 dataset_id VARCHAR(255) NOT NULL PRIMARY KEY,
 data_json JSON NOT NULL,
 revision BIGINT NOT NULL DEFAULT 0 CHECK(revision >= 0),
 created_at TIMESTAMP NOT NULL,
 updated_at TIMESTAMP NOT NULL
);
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
