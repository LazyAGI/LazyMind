-- 20260807160000_rename_user_chat_settings_enable_workflow
-- +migrate Down
-- +migrate Dialect postgres

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_schema = 'public'
          AND table_name = 'user_chat_settings'
          AND column_name = 'enable_workflow'
    ) AND NOT EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_schema = 'public'
          AND table_name = 'user_chat_settings'
          AND column_name = 'enable_plugin'
    ) THEN
        ALTER TABLE public.user_chat_settings RENAME COLUMN enable_workflow TO enable_plugin;
    ELSIF NOT EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_schema = 'public'
          AND table_name = 'user_chat_settings'
          AND column_name = 'enable_plugin'
    ) THEN
        ALTER TABLE public.user_chat_settings ADD COLUMN enable_plugin BOOLEAN NOT NULL DEFAULT TRUE;
    END IF;
END $$;

-- +migrate Dialect sqlite
CREATE TABLE IF NOT EXISTS user_chat_settings_next (
    user_id varchar(255),
    enable_plugin numeric NOT NULL DEFAULT true,
    plugin_mode varchar(16) NOT NULL DEFAULT "dynamic",
    enable_subagent numeric NOT NULL DEFAULT true,
    updated_at datetime NOT NULL,
    PRIMARY KEY (user_id)
);
DELETE FROM user_chat_settings_next;
INSERT INTO user_chat_settings_next SELECT * FROM user_chat_settings;
DROP TABLE user_chat_settings;
ALTER TABLE user_chat_settings_next RENAME TO user_chat_settings;
