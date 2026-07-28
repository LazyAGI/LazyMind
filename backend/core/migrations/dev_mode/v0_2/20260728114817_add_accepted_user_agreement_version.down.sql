-- 20260728114817_add_accepted_user_agreement_version
-- +migrate Down
-- +migrate Dialect postgres
ALTER TABLE user_ui_preferences
    DROP COLUMN IF EXISTS accepted_user_agreement_version;

-- +migrate Dialect sqlite
ALTER TABLE user_ui_preferences RENAME TO __user_ui_preferences_before_agreement;

CREATE TABLE user_ui_preferences (
    user_id varchar(255) NOT NULL,
    chat_preference_notice_dismissed numeric NOT NULL DEFAULT false,
    developer_mode_active numeric NOT NULL DEFAULT false,
    created_at datetime NOT NULL,
    updated_at datetime NOT NULL,
    PRIMARY KEY (user_id)
);

INSERT INTO user_ui_preferences (
    user_id,
    chat_preference_notice_dismissed,
    developer_mode_active,
    created_at,
    updated_at
)
SELECT
    user_id,
    chat_preference_notice_dismissed,
    developer_mode_active,
    created_at,
    updated_at
FROM __user_ui_preferences_before_agreement;

DROP TABLE __user_ui_preferences_before_agreement;
