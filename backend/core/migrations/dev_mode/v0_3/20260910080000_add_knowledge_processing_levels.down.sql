DROP TABLE IF EXISTS document_processing_states;
DROP INDEX IF EXISTS idx_datasets_processing_level;
-- +migrate Dialect postgres
ALTER TABLE datasets DROP COLUMN IF EXISTS processing_config;
ALTER TABLE datasets DROP COLUMN IF EXISTS reader_fallback_accepted;
ALTER TABLE datasets DROP COLUMN IF EXISTS transition_status;
ALTER TABLE datasets DROP COLUMN IF EXISTS processing_revision;
ALTER TABLE datasets DROP COLUMN IF EXISTS processing_level;
-- +migrate Dialect sqlite
ALTER TABLE datasets DROP COLUMN processing_config;
ALTER TABLE datasets DROP COLUMN reader_fallback_accepted;
ALTER TABLE datasets DROP COLUMN transition_status;
ALTER TABLE datasets DROP COLUMN processing_revision;
ALTER TABLE datasets DROP COLUMN processing_level;
