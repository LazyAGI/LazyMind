-- 20260728114817_add_accepted_user_agreement_version
-- +migrate Up
-- +migrate Dialect postgres
ALTER TABLE user_ui_preferences
    ADD COLUMN IF NOT EXISTS accepted_user_agreement_version VARCHAR(64) NOT NULL DEFAULT '';

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
