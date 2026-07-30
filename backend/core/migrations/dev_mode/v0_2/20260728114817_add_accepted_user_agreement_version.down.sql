-- 20260728114817_add_accepted_user_agreement_version
-- +migrate Down
-- +migrate Dialect postgres
ALTER TABLE user_ui_preferences
    DROP COLUMN IF EXISTS accepted_user_agreement_version;

-- +migrate Dialect sqlite
-- Idempotent: column may already be absent after a previous rollback.
ALTER TABLE user_ui_preferences DROP COLUMN accepted_user_agreement_version;
