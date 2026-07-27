-- +migrate Dialect postgres
ALTER TABLE public.documents
    DROP COLUMN IF EXISTS document_type;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
