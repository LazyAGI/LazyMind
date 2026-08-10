-- +migrate Dialect postgres
ALTER TABLE external_agent_runs DROP COLUMN IF EXISTS control_error;
ALTER TABLE external_agent_runs DROP COLUMN IF EXISTS control_release;

-- +migrate Dialect sqlite
ALTER TABLE external_agent_runs DROP COLUMN control_error;
ALTER TABLE external_agent_runs DROP COLUMN control_release;
