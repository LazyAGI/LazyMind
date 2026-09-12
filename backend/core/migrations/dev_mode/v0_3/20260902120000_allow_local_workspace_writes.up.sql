-- +migrate Dialect postgres
ALTER TABLE public.local_workspaces
    DROP CONSTRAINT IF EXISTS chk_local_workspaces_write_policy;
ALTER TABLE public.local_workspaces
    ALTER COLUMN write_policy SET DEFAULT 'allow';
UPDATE public.local_workspaces SET write_policy = 'allow';
ALTER TABLE public.local_workspaces
    ADD CONSTRAINT chk_local_workspaces_write_policy CHECK (write_policy = 'allow');

-- +migrate Dialect sqlite
PRAGMA foreign_keys = OFF;
CREATE TABLE local_workspaces_write_policy_new (
    id TEXT PRIMARY KEY,
    create_user_id TEXT NOT NULL,
    display_name TEXT NOT NULL,
    canonical_path TEXT NOT NULL,
    directory_identity TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('active', 'revoked', 'path_unavailable')),
    version INTEGER NOT NULL DEFAULT 1,
    source TEXT NOT NULL CHECK (source IN ('local', 'desktop')),
    read_policy TEXT NOT NULL DEFAULT 'allow' CHECK (read_policy = 'allow'),
    write_policy TEXT NOT NULL DEFAULT 'allow' CHECK (write_policy = 'allow'),
    authorized_at DATETIME NOT NULL,
    last_used_at DATETIME NOT NULL,
    revoked_at DATETIME NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
INSERT INTO local_workspaces_write_policy_new (
    id, create_user_id, display_name, canonical_path, directory_identity,
    status, version, source, read_policy, write_policy, authorized_at,
    last_used_at, revoked_at, created_at, updated_at
)
SELECT id, create_user_id, display_name, canonical_path, directory_identity,
       status, version, source, read_policy, 'allow', authorized_at,
       last_used_at, revoked_at, created_at, updated_at
FROM local_workspaces;
DROP TABLE local_workspaces;
ALTER TABLE local_workspaces_write_policy_new RENAME TO local_workspaces;
CREATE INDEX idx_local_workspaces_user_recent
    ON local_workspaces(create_user_id, status, last_used_at DESC);
PRAGMA foreign_keys = ON;
