-- +migrate Dialect postgres
CREATE TABLE IF NOT EXISTS cloud_resource_bindings (
    id VARCHAR(36) PRIMARY KEY,
    cloud_issuer VARCHAR(512) NOT NULL,
    cloud_account_id VARCHAR(255) NOT NULL,
    resource_type VARCHAR(16) NOT NULL,
    cloud_resource_id VARCHAR(128) NOT NULL,
    client_resource_key VARCHAR(128) NOT NULL,
    local_resource_id VARCHAR(128) NOT NULL,
    local_resource_ref VARCHAR(512) NOT NULL DEFAULT '',
    cloud_content_hash VARCHAR(64) NOT NULL,
    installed_local_revision_id VARCHAR(64) NOT NULL,
    installed_local_content_hash VARCHAR(64) NOT NULL,
    cloud_resource_name VARCHAR(255) NOT NULL,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    CONSTRAINT uk_cloud_binding_resource UNIQUE (cloud_issuer, cloud_account_id, resource_type, cloud_resource_id),
    CONSTRAINT uk_cloud_binding_local UNIQUE (cloud_issuer, cloud_account_id, resource_type, local_resource_id),
    CONSTRAINT chk_cloud_binding_resource_type CHECK (resource_type IN ('skill', 'workflow'))
);

-- +migrate Dialect sqlite
CREATE TABLE IF NOT EXISTS cloud_resource_bindings (
    id VARCHAR(36) PRIMARY KEY,
    cloud_issuer VARCHAR(512) NOT NULL,
    cloud_account_id VARCHAR(255) NOT NULL,
    resource_type VARCHAR(16) NOT NULL CHECK (resource_type IN ('skill', 'workflow')),
    cloud_resource_id VARCHAR(128) NOT NULL,
    client_resource_key VARCHAR(128) NOT NULL,
    local_resource_id VARCHAR(128) NOT NULL,
    local_resource_ref VARCHAR(512) NOT NULL DEFAULT '',
    cloud_content_hash VARCHAR(64) NOT NULL,
    installed_local_revision_id VARCHAR(64) NOT NULL,
    installed_local_content_hash VARCHAR(64) NOT NULL,
    cloud_resource_name VARCHAR(255) NOT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE (cloud_issuer, cloud_account_id, resource_type, cloud_resource_id),
    UNIQUE (cloud_issuer, cloud_account_id, resource_type, local_resource_id)
);
