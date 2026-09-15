-- +migrate Dialect postgres
ALTER TABLE external_agent_bindings
    DROP COLUMN IF EXISTS managed_by_lazymind;

-- +migrate Dialect sqlite
ALTER TABLE external_agent_bindings
    DROP COLUMN managed_by_lazymind;
