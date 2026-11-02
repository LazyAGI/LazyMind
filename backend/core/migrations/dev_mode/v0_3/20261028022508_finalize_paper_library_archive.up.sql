-- +migrate Dialect postgres,sqlite
-- On development databases that already applied the former preservation SQL,
-- the newly inserted pre-drop migration ran later and left a second copy here.
-- Reconcile it without overwriting the existing immutable graph.
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
INSERT INTO paper_libraries (dataset_id,data_json,revision,created_at,updated_at)
 SELECT dataset_id,data_json,revision,created_at,updated_at
 FROM paper_library_migration_backups WHERE TRUE
 ON CONFLICT(dataset_id) DO NOTHING;
DROP TABLE paper_library_migration_backups;
