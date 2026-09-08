-- +migrate Dialect postgres
ALTER TABLE public.conversation_workspace_bindings
    DROP CONSTRAINT IF EXISTS chk_conversation_workspace_permission_mode;
ALTER TABLE public.conversation_workspace_bindings DROP COLUMN updated_at;
ALTER TABLE public.conversation_workspace_bindings DROP COLUMN permission_version;
ALTER TABLE public.conversation_workspace_bindings DROP COLUMN permission_mode;

-- +migrate Dialect sqlite
PRAGMA foreign_keys = OFF;
CREATE TABLE conversation_workspace_bindings_permission_old (
    conversation_id TEXT PRIMARY KEY REFERENCES conversations(id) ON DELETE CASCADE,
    workspace_id TEXT NOT NULL REFERENCES local_workspaces(id) ON DELETE RESTRICT,
    created_at DATETIME NOT NULL
);
INSERT INTO conversation_workspace_bindings_permission_old (
    conversation_id, workspace_id, created_at
)
SELECT conversation_id, workspace_id, created_at
FROM conversation_workspace_bindings;
DROP TABLE conversation_workspace_bindings;
ALTER TABLE conversation_workspace_bindings_permission_old
    RENAME TO conversation_workspace_bindings;
CREATE INDEX idx_conversation_workspace_bindings_workspace
    ON conversation_workspace_bindings(workspace_id);
PRAGMA foreign_keys = ON;
