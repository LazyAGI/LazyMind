ALTER TABLE public.uploaded_files
    ADD COLUMN IF NOT EXISTS content_hash VARCHAR(64) NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_uploaded_files_reusable_hash
    ON public.uploaded_files(create_user_id, content_hash)
    WHERE deleted_at IS NULL
      AND content_hash <> ''
      AND status IN ('UPLOADED', 'BOUND');
