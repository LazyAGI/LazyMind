-- +migrate Dialect postgres,sqlite
ALTER TABLE paper_import_batches ADD COLUMN source_context_json JSON NOT NULL DEFAULT '{}';
ALTER TABLE paper_import_items ADD COLUMN title_snapshot TEXT NOT NULL DEFAULT '';
ALTER TABLE paper_import_items ADD COLUMN library_paper_id VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE paper_import_items ADD COLUMN result_kind VARCHAR(32) NOT NULL DEFAULT '';
