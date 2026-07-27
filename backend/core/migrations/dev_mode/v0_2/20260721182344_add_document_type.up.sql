-- +migrate Dialect postgres
ALTER TABLE public.documents
    ADD COLUMN IF NOT EXISTS document_type character varying(64);

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
