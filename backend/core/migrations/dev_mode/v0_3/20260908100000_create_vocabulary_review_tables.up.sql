-- +migrate Dialect postgres
ALTER TABLE vocabulary_words ADD COLUMN mastered_at TIMESTAMPTZ NULL;
CREATE TABLE IF NOT EXISTS vocabulary_review_cards (id VARCHAR(64) PRIMARY KEY, owner_id VARCHAR(64) NOT NULL, word_id VARCHAR(64) NOT NULL, fsrs_card_json TEXT NOT NULL, row_version BIGINT NOT NULL DEFAULT 1, suspended_at TIMESTAMPTZ NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP, UNIQUE(owner_id, word_id));
CREATE INDEX IF NOT EXISTS idx_vocabulary_review_due ON vocabulary_review_cards(owner_id, suspended_at);
CREATE TABLE IF NOT EXISTS vocabulary_review_logs (id VARCHAR(64) PRIMARY KEY, owner_id VARCHAR(64) NOT NULL, card_id VARCHAR(64) NOT NULL, rating INTEGER NOT NULL, fsrs_log_json TEXT NOT NULL, reviewed_at TIMESTAMPTZ NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP);
-- +migrate Dialect sqlite
ALTER TABLE vocabulary_words ADD COLUMN mastered_at DATETIME NULL;
CREATE TABLE IF NOT EXISTS vocabulary_review_cards (id TEXT PRIMARY KEY, owner_id TEXT NOT NULL, word_id TEXT NOT NULL, fsrs_card_json TEXT NOT NULL, row_version INTEGER NOT NULL DEFAULT 1, suspended_at DATETIME NULL, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, UNIQUE(owner_id, word_id));
CREATE INDEX IF NOT EXISTS idx_vocabulary_review_due ON vocabulary_review_cards(owner_id, suspended_at);
CREATE TABLE IF NOT EXISTS vocabulary_review_logs (id TEXT PRIMARY KEY, owner_id TEXT NOT NULL, card_id TEXT NOT NULL, rating INTEGER NOT NULL, fsrs_log_json TEXT NOT NULL, reviewed_at DATETIME NOT NULL, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
