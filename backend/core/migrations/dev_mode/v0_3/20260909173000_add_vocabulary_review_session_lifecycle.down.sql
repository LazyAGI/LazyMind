DROP INDEX IF EXISTS idx_vocabulary_review_session_word;
DROP INDEX IF EXISTS idx_vocabulary_review_sessions_active;
-- +migrate Dialect postgres
ALTER TABLE vocabulary_review_sessions DROP COLUMN IF EXISTS expires_at;
ALTER TABLE vocabulary_review_sessions DROP COLUMN IF EXISTS status;
-- +migrate Dialect sqlite
ALTER TABLE vocabulary_review_sessions DROP COLUMN expires_at;
ALTER TABLE vocabulary_review_sessions DROP COLUMN status;
