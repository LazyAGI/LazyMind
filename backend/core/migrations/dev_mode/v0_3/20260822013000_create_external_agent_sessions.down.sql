-- +migrate Dialect postgres
DROP TABLE IF EXISTS external_agent_sessions;

-- +migrate Dialect sqlite
DROP TABLE IF EXISTS external_agent_sessions;
