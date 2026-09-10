-- +migrate Dialect postgres
ALTER TABLE vocabulary_review_sessions ADD COLUMN IF NOT EXISTS status VARCHAR(16) NOT NULL DEFAULT 'active';
ALTER TABLE vocabulary_review_sessions ADD COLUMN IF NOT EXISTS expires_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP;
-- +migrate Dialect sqlite
ALTER TABLE vocabulary_review_sessions ADD COLUMN status VARCHAR(16) NOT NULL DEFAULT 'active';
ALTER TABLE vocabulary_review_sessions ADD COLUMN expires_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP;
-- +migrate Dialect *
CREATE INDEX IF NOT EXISTS idx_vocabulary_review_sessions_active ON vocabulary_review_sessions(owner_id, provider, wordbook_id, completed_at, expires_at);
CREATE UNIQUE INDEX IF NOT EXISTS idx_vocabulary_review_session_word ON vocabulary_review_session_items(session_id, word_id);
