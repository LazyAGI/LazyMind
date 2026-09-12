-- +migrate Dialect postgres
CREATE TABLE IF NOT EXISTS public.local_workspaces (
    id VARCHAR(64) PRIMARY KEY,
    create_user_id VARCHAR(255) NOT NULL,
    display_name VARCHAR(255) NOT NULL,
    canonical_path TEXT NOT NULL,
    directory_identity VARCHAR(512) NOT NULL,
    status VARCHAR(32) NOT NULL,
    version BIGINT NOT NULL DEFAULT 1,
    source VARCHAR(32) NOT NULL,
    read_policy VARCHAR(32) NOT NULL DEFAULT 'allow',
    write_policy VARCHAR(32) NOT NULL DEFAULT 'ask_before_write',
    authorized_at TIMESTAMP NOT NULL,
    last_used_at TIMESTAMP NOT NULL,
    revoked_at TIMESTAMP NULL,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    CONSTRAINT chk_local_workspaces_status CHECK (status IN ('active', 'revoked', 'path_unavailable')),
    CONSTRAINT chk_local_workspaces_source CHECK (source IN ('local', 'desktop')),
    CONSTRAINT chk_local_workspaces_read_policy CHECK (read_policy = 'allow'),
    CONSTRAINT chk_local_workspaces_write_policy CHECK (write_policy = 'ask_before_write')
);
CREATE INDEX IF NOT EXISTS idx_local_workspaces_user_recent
    ON public.local_workspaces(create_user_id, status, last_used_at DESC);

CREATE TABLE IF NOT EXISTS public.conversation_workspace_bindings (
    conversation_id VARCHAR(36) PRIMARY KEY,
    workspace_id VARCHAR(64) NOT NULL,
    created_at TIMESTAMP NOT NULL,
    CONSTRAINT fk_conversation_workspace_bindings_conversation
        FOREIGN KEY (conversation_id) REFERENCES public.conversations(id) ON DELETE CASCADE,
    CONSTRAINT fk_conversation_workspace_bindings_workspace
        FOREIGN KEY (workspace_id) REFERENCES public.local_workspaces(id) ON DELETE RESTRICT
);
CREATE INDEX IF NOT EXISTS idx_conversation_workspace_bindings_workspace
    ON public.conversation_workspace_bindings(workspace_id);

-- +migrate Dialect sqlite
CREATE TABLE IF NOT EXISTS local_workspaces (
    id TEXT PRIMARY KEY,
    create_user_id TEXT NOT NULL,
    display_name TEXT NOT NULL,
    canonical_path TEXT NOT NULL,
    directory_identity TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('active', 'revoked', 'path_unavailable')),
    version INTEGER NOT NULL DEFAULT 1,
    source TEXT NOT NULL CHECK (source IN ('local', 'desktop')),
    read_policy TEXT NOT NULL DEFAULT 'allow' CHECK (read_policy = 'allow'),
    write_policy TEXT NOT NULL DEFAULT 'ask_before_write' CHECK (write_policy = 'ask_before_write'),
    authorized_at DATETIME NOT NULL,
    last_used_at DATETIME NOT NULL,
    revoked_at DATETIME NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_local_workspaces_user_recent
    ON local_workspaces(create_user_id, status, last_used_at DESC);

CREATE TABLE IF NOT EXISTS conversation_workspace_bindings (
    conversation_id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    created_at DATETIME NOT NULL,
    FOREIGN KEY (conversation_id) REFERENCES conversations(id) ON DELETE CASCADE,
    FOREIGN KEY (workspace_id) REFERENCES local_workspaces(id) ON DELETE RESTRICT
);
CREATE INDEX IF NOT EXISTS idx_conversation_workspace_bindings_workspace
    ON conversation_workspace_bindings(workspace_id);
