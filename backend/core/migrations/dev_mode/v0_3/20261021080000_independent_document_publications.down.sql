-- +migrate Dialect postgres
CREATE TEMP TABLE publication_rollback_guard (pending_count INTEGER CHECK(pending_count = 0));
INSERT INTO publication_rollback_guard SELECT COUNT(*) FROM document_publication_bindings WHERE binding_scope <> '' AND pending_operation_id <> '';
DROP TABLE publication_rollback_guard;
-- Refuse rollback while new publications are unresolved. Keep operations so
-- confirmed outcomes remain auditable; only remove the new binding namespace.
DELETE FROM document_publication_bindings WHERE binding_scope <> '';
DROP INDEX idx_document_publication_item;
CREATE UNIQUE INDEX idx_document_publication_item ON document_publication_bindings(session_id,slot_id,item_index);
ALTER TABLE document_publication_bindings DROP COLUMN last_success_at;
ALTER TABLE document_publication_bindings DROP COLUMN last_operation_id;
ALTER TABLE document_publication_bindings DROP COLUMN last_success_hash;
ALTER TABLE document_publication_bindings DROP COLUMN connection_id;
ALTER TABLE document_publication_bindings DROP COLUMN binding_scope;
DROP INDEX idx_document_publication_operations_publication_id;
ALTER TABLE document_publication_operations DROP COLUMN connection_id;
ALTER TABLE document_publication_operations DROP COLUMN publication_id;

-- +migrate Dialect sqlite
CREATE TEMP TABLE publication_rollback_guard (pending_count INTEGER CHECK(pending_count = 0));
INSERT INTO publication_rollback_guard SELECT COUNT(*) FROM document_publication_bindings WHERE binding_scope <> '' AND pending_operation_id <> '';
DROP TABLE publication_rollback_guard;
-- Refuse rollback while new publications are unresolved. Keep operations so
-- confirmed outcomes remain auditable; only remove the new binding namespace.
DELETE FROM document_publication_bindings WHERE binding_scope <> '';
DROP INDEX idx_document_publication_item;
CREATE UNIQUE INDEX idx_document_publication_item ON document_publication_bindings(session_id,slot_id,item_index);
ALTER TABLE document_publication_bindings DROP COLUMN last_success_at;
ALTER TABLE document_publication_bindings DROP COLUMN last_operation_id;
ALTER TABLE document_publication_bindings DROP COLUMN last_success_hash;
ALTER TABLE document_publication_bindings DROP COLUMN connection_id;
ALTER TABLE document_publication_bindings DROP COLUMN binding_scope;
DROP INDEX idx_document_publication_operations_publication_id;
ALTER TABLE document_publication_operations DROP COLUMN connection_id;
ALTER TABLE document_publication_operations DROP COLUMN publication_id;
