CREATE TABLE IF NOT EXISTS document_directories (
    id VARCHAR(36) PRIMARY KEY,
    user_id VARCHAR(255) NOT NULL,
    name VARCHAR(255) NOT NULL,
    intent TEXT NOT NULL,
    organization TEXT NOT NULL,
    auto_add BOOLEAN NOT NULL DEFAULT true,
    pinned BOOLEAN NOT NULL DEFAULT false,
    ready BOOLEAN NOT NULL DEFAULT false,
    version BIGINT NOT NULL DEFAULT 1,
    last_used_at TIMESTAMP NOT NULL,
    use_until TIMESTAMP,
    organized_at TIMESTAMP,
    deleted_at TIMESTAMP,
    coverage_json JSON,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_document_directories_user_id ON document_directories(user_id);

CREATE TABLE IF NOT EXISTS document_directory_groups (
    id VARCHAR(36) PRIMARY KEY,
    directory_id VARCHAR(36) NOT NULL,
    parent_id VARCHAR(36) NOT NULL DEFAULT '',
    name VARCHAR(255) NOT NULL,
    position INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_document_directory_groups_directory_id ON document_directory_groups(directory_id);

CREATE TABLE IF NOT EXISTS document_directory_entries (
    id VARCHAR(36) PRIMARY KEY,
    directory_id VARCHAR(36) NOT NULL,
    resource_key VARCHAR(64) NOT NULL,
    group_id VARCHAR(36) NOT NULL DEFAULT '',
    source VARCHAR(32) NOT NULL,
    namespace TEXT NOT NULL,
    resource_id TEXT NOT NULL,
    locator_json JSON,
    title TEXT NOT NULL,
    pinned BOOLEAN NOT NULL DEFAULT false,
    excluded BOOLEAN NOT NULL DEFAULT false,
    read_state VARCHAR(32) NOT NULL DEFAULT 'unread',
    confirmed BOOLEAN NOT NULL DEFAULT false,
    access_state VARCHAR(32) NOT NULL DEFAULT 'unknown',
    content_version TEXT NOT NULL DEFAULT '',
    content_state VARCHAR(32) NOT NULL DEFAULT 'unknown',
    read_at TIMESTAMP,
    evidence_json JSON,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_directory_resource ON document_directory_entries(directory_id, resource_key);

CREATE TABLE IF NOT EXISTS document_observations (
    id VARCHAR(64) PRIMARY KEY,
    user_id VARCHAR(255) NOT NULL,
    conversation_id VARCHAR(128) NOT NULL,
    history_id VARCHAR(128) NOT NULL,
    run_id VARCHAR(128) NOT NULL,
    call_id VARCHAR(255) NOT NULL,
    query TEXT NOT NULL,
    payload_json JSON NOT NULL,
    batch_id VARCHAR(36) NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_document_observations_user_id ON document_observations(user_id);

CREATE INDEX IF NOT EXISTS idx_document_observation_pending ON document_observations(conversation_id, batch_id);

CREATE TABLE IF NOT EXISTS document_directory_batches (
    id VARCHAR(36) PRIMARY KEY,
    user_id VARCHAR(255) NOT NULL,
    parent_id VARCHAR(36) NOT NULL DEFAULT '',
    directory_id VARCHAR(36) NOT NULL DEFAULT '',
    job_id VARCHAR(64) NOT NULL DEFAULT '',
    trigger_id VARCHAR(255) NOT NULL,
    owner_key VARCHAR(255) NOT NULL,
    input_json JSON NOT NULL,
    status VARCHAR(32) NOT NULL,
    result_json JSON,
    acknowledged BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_document_directory_batches_user_id ON document_directory_batches(user_id);

CREATE INDEX IF NOT EXISTS idx_document_directory_batches_parent_id ON document_directory_batches(parent_id);

CREATE INDEX IF NOT EXISTS idx_document_directory_batches_directory_id ON document_directory_batches(directory_id);

CREATE UNIQUE INDEX IF NOT EXISTS uq_directory_batch_trigger ON document_directory_batches(trigger_id, owner_key);
