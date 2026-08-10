-- +migrate Dialect postgres
-- Intentionally empty. Workflow facade expansion is rolling-deployment safe;
-- application rollback must not destructively remove data required by newer binaries.
SELECT 1;

-- +migrate Dialect sqlite
-- Expand-only: rollback is an application flag change, not destructive DDL.
SELECT 1;
