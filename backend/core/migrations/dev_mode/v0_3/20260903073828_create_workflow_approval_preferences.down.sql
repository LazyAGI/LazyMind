-- +migrate Dialect postgres
DROP TABLE IF EXISTS public.workflow_approval_preferences;

-- +migrate Dialect sqlite
DROP TABLE IF EXISTS workflow_approval_preferences;
