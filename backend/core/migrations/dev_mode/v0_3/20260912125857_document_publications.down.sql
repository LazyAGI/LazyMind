-- +migrate Dialect postgres
DROP TABLE IF EXISTS document_publication_bindings;
DROP TABLE IF EXISTS document_publication_operations;

-- +migrate Dialect sqlite
DROP TABLE IF EXISTS document_publication_bindings;
DROP TABLE IF EXISTS document_publication_operations;
