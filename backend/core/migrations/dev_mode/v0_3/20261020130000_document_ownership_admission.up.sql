-- +migrate Dialect postgres,sqlite
CREATE TABLE document_ownership_backfills (
 dataset_id VARCHAR(255) PRIMARY KEY,
 kb_id VARCHAR(255) NOT NULL,
 tenant_id VARCHAR(128) NOT NULL,
 max_association_id BIGINT NOT NULL,
 completed_at TIMESTAMP,
 created_at TIMESTAMP NOT NULL
);
CREATE TABLE rejected_document_publications (
 dataset_id VARCHAR(255) NOT NULL,
 external_doc_id VARCHAR(128) NOT NULL,
 PRIMARY KEY (dataset_id,external_doc_id)
);
