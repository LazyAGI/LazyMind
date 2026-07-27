ALTER TABLE public.documents
    ADD COLUMN IF NOT EXISTS document_type character varying(64);
