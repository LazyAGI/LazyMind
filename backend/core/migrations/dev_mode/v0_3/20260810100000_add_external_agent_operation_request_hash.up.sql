ALTER TABLE external_agent_operations
    ADD COLUMN request_hash VARCHAR(64) NOT NULL DEFAULT '';
