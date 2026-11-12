-- +migrate Dialect postgres,sqlite
CREATE TABLE IF NOT EXISTS embedding_index_states (
    scope VARCHAR(255) PRIMARY KEY,
    active_model_id VARCHAR(255) NOT NULL DEFAULT '',
    active_source VARCHAR(16) NOT NULL DEFAULT '',
    active_model_name VARCHAR(255) NOT NULL DEFAULT '',
    active_provider_name VARCHAR(255) NOT NULL DEFAULT '',
    signature VARCHAR(255) NOT NULL DEFAULT '',
    phase VARCHAR(32) NOT NULL DEFAULT 'ready',
    generation BIGINT NOT NULL DEFAULT 1,
    target_model_id VARCHAR(255) NOT NULL DEFAULT '',
    target_source VARCHAR(16) NOT NULL DEFAULT '',
    target_signature VARCHAR(255) NOT NULL DEFAULT '',
    updated_at TIMESTAMP NOT NULL
);
