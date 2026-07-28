-- +migrate Dialect postgres
DROP INDEX IF EXISTS public.idx_uploaded_files_reusable_hash;

ALTER TABLE public.uploaded_files
    DROP COLUMN IF EXISTS content_hash;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
