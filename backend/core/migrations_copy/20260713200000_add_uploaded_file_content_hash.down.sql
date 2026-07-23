DROP INDEX IF EXISTS public.idx_uploaded_files_reusable_hash;

ALTER TABLE public.uploaded_files
    DROP COLUMN IF EXISTS content_hash;
