-- 20261021113853_add_workflow_trust_notice_preference
-- +migrate Down
-- +migrate Dialect postgres
ALTER TABLE user_ui_preferences
    DROP COLUMN IF EXISTS workflow_trust_notice_dismissed;

-- +migrate Dialect sqlite
ALTER TABLE user_ui_preferences DROP COLUMN workflow_trust_notice_dismissed;
