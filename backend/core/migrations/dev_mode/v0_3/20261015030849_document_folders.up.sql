CREATE TABLE IF NOT EXISTS document_folder_inventories (
 id VARCHAR(36) PRIMARY KEY, user_id VARCHAR(255) NOT NULL, conversation_id VARCHAR(128) NOT NULL,
 source_json JSON NOT NULL, state_json JSON NOT NULL, status VARCHAR(32) NOT NULL, version BIGINT NOT NULL,
 directory_id VARCHAR(36) NOT NULL DEFAULT '', batch_id VARCHAR(36) NOT NULL DEFAULT '',
 created_at TIMESTAMP NOT NULL, updated_at TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_document_folder_inventories_user_id ON document_folder_inventories(user_id);
CREATE INDEX IF NOT EXISTS idx_document_folder_inventories_directory_id ON document_folder_inventories(directory_id);
CREATE TABLE IF NOT EXISTS document_folder_nodes (
 id VARCHAR(64) PRIMARY KEY, inventory_id VARCHAR(36) NOT NULL, node_key VARCHAR(64) NOT NULL,
 parent_key VARCHAR(64) NOT NULL, path TEXT NOT NULL, name TEXT NOT NULL, kind VARCHAR(32) NOT NULL,
 listed BOOLEAN NOT NULL, resource_key VARCHAR(64) NOT NULL DEFAULT '', candidate_json JSON
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_folder_node ON document_folder_nodes(inventory_id,node_key);
CREATE INDEX IF NOT EXISTS idx_folder_parent ON document_folder_nodes(inventory_id,parent_key);
