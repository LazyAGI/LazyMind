-- 20260728114817_add_accepted_user_agreement_version
-- +migrate Up
-- +migrate Dialect postgres
ALTER TABLE user_ui_preferences
    ADD COLUMN IF NOT EXISTS accepted_user_agreement_version VARCHAR(64) NOT NULL DEFAULT '';

-- +migrate Dialect sqlite
-- Idempotent: column may already exist from seed/aggregate CREATE TABLE.
ALTER TABLE user_ui_preferences ADD COLUMN accepted_user_agreement_version varchar(64) NOT NULL DEFAULT '';
