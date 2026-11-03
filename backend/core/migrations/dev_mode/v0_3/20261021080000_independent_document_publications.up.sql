-- +migrate Dialect postgres
ALTER TABLE document_publication_operations ADD COLUMN publication_id VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE document_publication_operations ADD COLUMN connection_id VARCHAR(255) NOT NULL DEFAULT '';
CREATE INDEX idx_document_publication_operations_publication_id ON document_publication_operations(publication_id);
ALTER TABLE document_publication_bindings ADD COLUMN binding_scope VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE document_publication_bindings ADD COLUMN connection_id VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE document_publication_bindings ADD COLUMN last_success_hash VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE document_publication_bindings ADD COLUMN last_operation_id VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE document_publication_bindings ADD COLUMN last_success_at TIMESTAMP;
DROP INDEX idx_document_publication_item;
CREATE UNIQUE INDEX idx_document_publication_item ON document_publication_bindings(session_id,slot_id,item_index,binding_scope);

-- +migrate Dialect sqlite
ALTER TABLE document_publication_operations ADD COLUMN publication_id VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE document_publication_operations ADD COLUMN connection_id VARCHAR(255) NOT NULL DEFAULT '';
CREATE INDEX idx_document_publication_operations_publication_id ON document_publication_operations(publication_id);
ALTER TABLE document_publication_bindings ADD COLUMN binding_scope VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE document_publication_bindings ADD COLUMN connection_id VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE document_publication_bindings ADD COLUMN last_success_hash VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE document_publication_bindings ADD COLUMN last_operation_id VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE document_publication_bindings ADD COLUMN last_success_at TIMESTAMP;
DROP INDEX idx_document_publication_item;
CREATE UNIQUE INDEX idx_document_publication_item ON document_publication_bindings(session_id,slot_id,item_index,binding_scope);
