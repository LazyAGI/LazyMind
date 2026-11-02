-- +migrate Dialect postgres,sqlite
-- The original entity migration's down creates the legacy table before this.
INSERT INTO paper_libraries (dataset_id,data_json,revision,created_at,updated_at)
 SELECT dataset_id,data_json,revision,created_at,updated_at
 FROM paper_library_migration_backups WHERE TRUE
 ON CONFLICT(dataset_id) DO NOTHING;
DROP TABLE paper_library_migration_backups;
