-- +migrate Dialect postgres
DROP TRIGGER knowledge_dataset_membership_version ON documents;
DROP TRIGGER knowledge_document_version ON documents;
DROP FUNCTION knowledge_dataset_membership_version();
DROP FUNCTION knowledge_document_version();
-- +migrate Dialect sqlite
DROP TRIGGER knowledge_dataset_membership_update;
DROP TRIGGER knowledge_dataset_membership_delete;
DROP TRIGGER knowledge_dataset_membership_insert;
DROP TRIGGER knowledge_document_version;

-- +migrate Dialect postgres,sqlite
DROP TABLE resource_operation_receipts;
ALTER TABLE documents DROP COLUMN resource_version;
ALTER TABLE datasets DROP COLUMN resource_version;
ALTER TABLE datasets DROP COLUMN scene;
