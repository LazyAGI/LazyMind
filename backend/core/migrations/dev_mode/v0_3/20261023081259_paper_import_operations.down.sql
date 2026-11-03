-- +migrate Dialect postgres,sqlite
ALTER TABLE paper_import_items DROP COLUMN result_kind;
ALTER TABLE paper_import_items DROP COLUMN library_paper_id;
ALTER TABLE paper_import_items DROP COLUMN title_snapshot;
ALTER TABLE paper_import_batches DROP COLUMN source_context_json;
