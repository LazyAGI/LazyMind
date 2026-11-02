-- 20261021113853_add_workflow_trust_notice_preference
-- +migrate Up
-- +migrate Dialect postgres
ALTER TABLE user_ui_preferences
    ADD COLUMN IF NOT EXISTS workflow_trust_notice_dismissed BOOLEAN NOT NULL DEFAULT FALSE;

-- +migrate Dialect sqlite
ALTER TABLE user_ui_preferences ADD COLUMN workflow_trust_notice_dismissed BOOLEAN NOT NULL DEFAULT FALSE;
