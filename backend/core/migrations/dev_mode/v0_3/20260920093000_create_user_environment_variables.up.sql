-- +migrate Dialect postgres
CREATE TABLE IF NOT EXISTS user_environment_variables (
    id VARCHAR(64) PRIMARY KEY,
    user_id VARCHAR(255) NOT NULL,
    name VARCHAR(128) NOT NULL,
    value_ciphertext TEXT NOT NULL,
    credential_version INTEGER NOT NULL DEFAULT 2,
    credential_revision BIGINT NOT NULL DEFAULT 1,
    enabled BOOLEAN NOT NULL,
    description VARCHAR(512) NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    deleted_at TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_user_env_user_name_active
    ON user_environment_variables (user_id, name)
    WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_user_env_user_enabled
    ON user_environment_variables (user_id, enabled);
CREATE INDEX IF NOT EXISTS idx_user_environment_variables_deleted_at
    ON user_environment_variables (deleted_at);

-- +migrate Dialect sqlite
CREATE TABLE IF NOT EXISTS user_environment_variables (
    id VARCHAR(64) PRIMARY KEY,
    user_id VARCHAR(255) NOT NULL,
    name VARCHAR(128) NOT NULL,
    value_ciphertext TEXT NOT NULL,
    credential_version INTEGER NOT NULL DEFAULT 2,
    credential_revision INTEGER NOT NULL DEFAULT 1,
    enabled BOOLEAN NOT NULL,
    description VARCHAR(512) NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    deleted_at DATETIME
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_user_env_user_name_active
    ON user_environment_variables (user_id, name)
    WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_user_env_user_enabled
    ON user_environment_variables (user_id, enabled);
CREATE INDEX IF NOT EXISTS idx_user_environment_variables_deleted_at
    ON user_environment_variables (deleted_at);
