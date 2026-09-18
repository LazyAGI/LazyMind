-- +migrate Dialect postgres
ALTER TABLE public.conversation_workspace_bindings
    ADD COLUMN permission_mode VARCHAR(32) NOT NULL DEFAULT 'ask_as_needed';
ALTER TABLE public.conversation_workspace_bindings
    ADD COLUMN permission_version BIGINT NOT NULL DEFAULT 1;
ALTER TABLE public.conversation_workspace_bindings
    ADD COLUMN updated_at TIMESTAMP NULL;
UPDATE public.conversation_workspace_bindings
SET updated_at = created_at
WHERE updated_at IS NULL;
ALTER TABLE public.conversation_workspace_bindings
    ALTER COLUMN updated_at SET DEFAULT CURRENT_TIMESTAMP;
ALTER TABLE public.conversation_workspace_bindings
    ALTER COLUMN updated_at SET NOT NULL;
ALTER TABLE public.conversation_workspace_bindings
    ADD CONSTRAINT chk_conversation_workspace_permission_mode
    CHECK (permission_mode IN ('always_ask', 'ask_as_needed', 'allow_all'));

-- +migrate Dialect sqlite
ALTER TABLE conversation_workspace_bindings
    ADD COLUMN permission_mode TEXT NOT NULL DEFAULT 'ask_as_needed'
    CHECK (permission_mode IN ('always_ask', 'ask_as_needed', 'allow_all'));
ALTER TABLE conversation_workspace_bindings
    ADD COLUMN permission_version INTEGER NOT NULL DEFAULT 1;
ALTER TABLE conversation_workspace_bindings
    ADD COLUMN updated_at DATETIME NOT NULL DEFAULT '1970-01-01 00:00:00';
UPDATE conversation_workspace_bindings SET updated_at = created_at;
