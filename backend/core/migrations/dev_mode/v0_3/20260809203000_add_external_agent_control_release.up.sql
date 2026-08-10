ALTER TABLE external_agent_runs
    ADD COLUMN IF NOT EXISTS control_release VARCHAR(32) NOT NULL DEFAULT '';

ALTER TABLE external_agent_runs
    ADD COLUMN IF NOT EXISTS control_error TEXT;
