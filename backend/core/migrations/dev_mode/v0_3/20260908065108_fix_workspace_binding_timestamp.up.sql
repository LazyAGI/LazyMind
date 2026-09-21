-- +migrate Dialect postgres
-- PostgreSQL already has the current-time default in the shared migration.
SELECT 1;

-- +migrate Dialect sqlite
PRAGMA foreign_keys = OFF;
CREATE TABLE conversation_workspace_bindings_timestamp_new (
    conversation_id TEXT PRIMARY KEY REFERENCES conversations(id) ON DELETE CASCADE,
    workspace_id TEXT NOT NULL REFERENCES local_workspaces(id) ON DELETE RESTRICT,
    permission_mode TEXT NOT NULL DEFAULT 'ask_as_needed'
        CHECK (permission_mode IN ('always_ask', 'ask_as_needed', 'allow_all')),
    permission_version INTEGER NOT NULL DEFAULT 1,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO conversation_workspace_bindings_timestamp_new
    (conversation_id, workspace_id, permission_mode, permission_version, created_at, updated_at)
SELECT conversation_id, workspace_id, permission_mode, permission_version, created_at, updated_at
FROM conversation_workspace_bindings;
DROP TABLE conversation_workspace_bindings;
ALTER TABLE conversation_workspace_bindings_timestamp_new RENAME TO conversation_workspace_bindings;
CREATE INDEX idx_conversation_workspace_bindings_workspace ON conversation_workspace_bindings(workspace_id);
PRAGMA foreign_keys = ON;
