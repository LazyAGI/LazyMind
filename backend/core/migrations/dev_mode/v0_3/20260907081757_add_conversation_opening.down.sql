-- +migrate Dialect postgres
DELETE FROM async_jobs WHERE job_type IN ('conversation.opening', 'conversation.opening.backfill');
DROP TABLE IF EXISTS conversation_opening_metadata;
DROP TABLE IF EXISTS conversation_opening_backfills;
ALTER TABLE conversations DROP COLUMN title_revision;
ALTER TABLE conversations DROP COLUMN title_source;
DELETE FROM user_selected_models WHERE model_type = 'conversation_metadata';

-- +migrate Dialect sqlite
DELETE FROM async_jobs WHERE job_type IN ('conversation.opening', 'conversation.opening.backfill');
DROP TABLE IF EXISTS conversation_opening_metadata;
DROP TABLE IF EXISTS conversation_opening_backfills;
ALTER TABLE conversations DROP COLUMN title_revision;
ALTER TABLE conversations DROP COLUMN title_source;
DELETE FROM user_selected_models WHERE model_type = 'conversation_metadata';
