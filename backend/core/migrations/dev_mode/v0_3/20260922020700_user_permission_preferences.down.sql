UPDATE conversation_workspace_bindings SET
    permission_mode = COALESCE((SELECT NULLIF(permission_mode, '') FROM conversations WHERE id = conversation_id), permission_mode),
    permission_version = COALESCE((SELECT NULLIF(permission_version, 0) FROM conversations WHERE id = conversation_id), permission_version);
ALTER TABLE conversations DROP COLUMN permission_version;
ALTER TABLE conversations DROP COLUMN permission_mode;
ALTER TABLE user_chat_settings DROP COLUMN permission_version;
ALTER TABLE user_chat_settings DROP COLUMN default_permission_mode;
