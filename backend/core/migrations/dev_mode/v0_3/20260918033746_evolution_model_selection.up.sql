-- +migrate Dialect postgres
CREATE TABLE evolution_model_validations (
    model_ref VARCHAR(160) PRIMARY KEY,
    validation_version VARCHAR(64) NOT NULL,
    evidence_id VARCHAR(255) NOT NULL,
    passed BOOLEAN NOT NULL DEFAULT FALSE,
    verified_at TIMESTAMP WITH TIME ZONE NOT NULL,
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL
);
ALTER TABLE agent_threads ADD COLUMN status_observed_at TIMESTAMP WITH TIME ZONE NULL;

-- +migrate Dialect sqlite
CREATE TABLE evolution_model_validations (
    model_ref VARCHAR(160) PRIMARY KEY,
    validation_version VARCHAR(64) NOT NULL,
    evidence_id VARCHAR(255) NOT NULL,
    passed BOOLEAN NOT NULL DEFAULT FALSE,
    verified_at DATETIME NOT NULL,
    expires_at DATETIME NOT NULL
);
ALTER TABLE agent_threads ADD COLUMN status_observed_at DATETIME NULL;
