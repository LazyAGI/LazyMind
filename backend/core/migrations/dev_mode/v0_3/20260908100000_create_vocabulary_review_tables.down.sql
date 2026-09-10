-- +migrate Dialect postgres
DROP TABLE IF EXISTS vocabulary_review_logs;
DROP TABLE IF EXISTS vocabulary_review_cards;
ALTER TABLE vocabulary_words DROP COLUMN IF EXISTS mastered_at;
-- +migrate Dialect sqlite
DROP TABLE IF EXISTS vocabulary_review_logs;
DROP TABLE IF EXISTS vocabulary_review_cards;
ALTER TABLE vocabulary_words DROP COLUMN mastered_at;
