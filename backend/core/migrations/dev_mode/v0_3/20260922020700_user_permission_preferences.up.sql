ALTER TABLE user_chat_settings ADD COLUMN default_permission_mode VARCHAR(32) NOT NULL DEFAULT 'always_ask';
ALTER TABLE user_chat_settings ADD COLUMN permission_version BIGINT NOT NULL DEFAULT 1;
ALTER TABLE conversations ADD COLUMN permission_mode VARCHAR(32) NOT NULL DEFAULT '';
ALTER TABLE conversations ADD COLUMN permission_version BIGINT NOT NULL DEFAULT 0;
UPDATE conversations SET
    permission_mode = COALESCE((SELECT permission_mode FROM conversation_workspace_bindings WHERE conversation_id = conversations.id), 'always_ask'),
    permission_version = COALESCE((SELECT permission_version FROM conversation_workspace_bindings WHERE conversation_id = conversations.id), 1);
