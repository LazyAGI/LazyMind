-- +migrate Dialect postgres
DROP INDEX IF EXISTS public.idx_conversation_workspace_bindings_workspace;
DROP TABLE IF EXISTS public.conversation_workspace_bindings;
DROP INDEX IF EXISTS public.idx_local_workspaces_user_recent;
DROP TABLE IF EXISTS public.local_workspaces;

-- +migrate Dialect sqlite
DROP INDEX IF EXISTS idx_conversation_workspace_bindings_workspace;
DROP TABLE IF EXISTS conversation_workspace_bindings;
DROP INDEX IF EXISTS idx_local_workspaces_user_recent;
DROP TABLE IF EXISTS local_workspaces;
