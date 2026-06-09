-- 20260604120000_recreate_eval_sets_dataset_ids
-- +migrate Up
--
-- Breaking eval-set development-data reset:
-- old eval_sets.dataset_id data is intentionally not migrated.

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_schema = 'public'
          AND table_name = 'eval_sets'
          AND column_name = 'dataset_id'
    ) OR NOT EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_schema = 'public'
          AND table_name = 'eval_sets'
          AND column_name = 'dataset_ids'
    ) THEN
        IF to_regclass('public.async_jobs') IS NOT NULL THEN
            DELETE FROM public.async_jobs
            WHERE resource_type = 'eval_set' OR job_type = 'eval_set_import';
        END IF;

        DROP TABLE IF EXISTS public.eval_set_import_previews CASCADE;
        DROP TABLE IF EXISTS public.eval_set_items CASCADE;
        DROP TABLE IF EXISTS public.eval_sets CASCADE;
        DROP TABLE IF EXISTS public.eval_set_shards CASCADE;
    END IF;
END $$;

CREATE TABLE IF NOT EXISTS public.eval_set_shards (
    id character varying(64) NOT NULL,
    status character varying(32) DEFAULT 'open'::character varying NOT NULL,
    row_limit bigint DEFAULT 200000 NOT NULL,
    row_open_threshold bigint DEFAULT 120000 NOT NULL,
    size_limit_bytes bigint DEFAULT 8589934592 NOT NULL,
    size_open_threshold_bytes bigint DEFAULT 5368709120 NOT NULL,
    actual_rows bigint DEFAULT 0 NOT NULL,
    estimated_bytes bigint DEFAULT 0 NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    sealed_at timestamp with time zone,
    CONSTRAINT eval_set_shards_pkey PRIMARY KEY (id),
    CONSTRAINT chk_eval_set_shards_status CHECK ((status)::text IN ('open', 'sealed'))
);

CREATE INDEX IF NOT EXISTS idx_eval_set_shards_status ON public.eval_set_shards(status);

INSERT INTO public.eval_set_shards (
    id, status, row_limit, row_open_threshold, size_limit_bytes,
    size_open_threshold_bytes, actual_rows, estimated_bytes,
    created_at, updated_at
) VALUES (
    'eval_shard_0001', 'open', 200000, 120000, 8589934592,
    5368709120, 0, 0, now(), now()
) ON CONFLICT (id) DO NOTHING;

CREATE TABLE IF NOT EXISTS public.eval_sets (
    id character varying(64) NOT NULL,
    name character varying(255) NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    dataset_ids jsonb DEFAULT '[]'::jsonb NOT NULL,
    owner_id character varying(255) NOT NULL,
    group_id character varying(255) DEFAULT ''::character varying NOT NULL,
    shard_id character varying(64) NOT NULL,
    status character varying(32) DEFAULT 'active'::character varying NOT NULL,
    item_count bigint DEFAULT 0 NOT NULL,
    create_user_id character varying(255) NOT NULL,
    create_user_name character varying(255) DEFAULT ''::character varying NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    CONSTRAINT eval_sets_pkey PRIMARY KEY (id),
    CONSTRAINT chk_eval_sets_status CHECK ((status)::text IN ('active', 'importing', 'failed')),
    CONSTRAINT fk_eval_sets_shard FOREIGN KEY (shard_id) REFERENCES public.eval_set_shards(id)
);

CREATE INDEX IF NOT EXISTS idx_eval_sets_owner ON public.eval_sets(owner_id);
CREATE INDEX IF NOT EXISTS idx_eval_sets_group ON public.eval_sets(group_id);
CREATE INDEX IF NOT EXISTS idx_eval_sets_dataset_ids ON public.eval_sets USING gin (dataset_ids);
CREATE INDEX IF NOT EXISTS idx_eval_sets_shard ON public.eval_sets(shard_id);
CREATE INDEX IF NOT EXISTS idx_eval_sets_status ON public.eval_sets(status);

CREATE TABLE IF NOT EXISTS public.eval_set_items (
    id character varying(64) NOT NULL,
    shard_id character varying(64) NOT NULL,
    eval_set_id character varying(64) NOT NULL,
    case_id character varying(255) DEFAULT ''::character varying NOT NULL,
    question text NOT NULL,
    ground_truth text NOT NULL,
    question_type character varying(128) NOT NULL,
    generate_reason text DEFAULT ''::text NOT NULL,
    key_points text DEFAULT ''::text NOT NULL,
    reference_chunk_ids text DEFAULT ''::text NOT NULL,
    reference_context text DEFAULT ''::text NOT NULL,
    algorithm_reference_context text DEFAULT ''::text NOT NULL,
    reference_doc text DEFAULT ''::text NOT NULL,
    reference_doc_ids text DEFAULT ''::text NOT NULL,
    is_deleted boolean DEFAULT false NOT NULL,
    estimated_bytes bigint DEFAULT 0 NOT NULL,
    source character varying(32) NOT NULL,
    source_session_id character varying(128) DEFAULT ''::character varying NOT NULL,
    source_history_id character varying(128) DEFAULT ''::character varying NOT NULL,
    create_user_id character varying(255) NOT NULL,
    create_user_name character varying(255) DEFAULT ''::character varying NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    CONSTRAINT eval_set_items_pkey PRIMARY KEY (shard_id, id),
    CONSTRAINT chk_eval_set_items_source CHECK ((source)::text IN ('upload', 'manual', 'flowback')),
    CONSTRAINT fk_eval_set_items_set FOREIGN KEY (eval_set_id) REFERENCES public.eval_sets(id),
    CONSTRAINT fk_eval_set_items_shard FOREIGN KEY (shard_id) REFERENCES public.eval_set_shards(id)
) PARTITION BY LIST (shard_id);

COMMENT ON COLUMN public.eval_set_items.is_deleted
    IS 'Template/business field imported from eval-set files; not a logical-delete marker. System deletion is physical DELETE.';

CREATE TABLE IF NOT EXISTS public.eval_set_items_p_eval_shard_0001
PARTITION OF public.eval_set_items
FOR VALUES IN ('eval_shard_0001');

CREATE INDEX IF NOT EXISTS idx_eval_set_items_set_created
    ON public.eval_set_items(shard_id, eval_set_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_eval_set_items_set_source
    ON public.eval_set_items(shard_id, eval_set_id, source);
CREATE INDEX IF NOT EXISTS idx_eval_set_items_set_type
    ON public.eval_set_items(shard_id, eval_set_id, question_type);
CREATE INDEX IF NOT EXISTS idx_eval_set_items_set_updated
    ON public.eval_set_items(shard_id, eval_set_id, updated_at DESC);

CREATE TABLE IF NOT EXISTS public.eval_set_import_previews (
    token character varying(64) NOT NULL,
    status character varying(32) DEFAULT 'ready'::character varying NOT NULL,
    file_name character varying(512) DEFAULT ''::character varying NOT NULL,
    file_type character varying(16) NOT NULL,
    temp_path text DEFAULT ''::text NOT NULL,
    total_rows bigint DEFAULT 0 NOT NULL,
    empty_rows bigint DEFAULT 0 NOT NULL,
    valid_rows bigint DEFAULT 0 NOT NULL,
    preview_rows_json json,
    error_details_json json,
    create_user_id character varying(255) NOT NULL,
    create_user_name character varying(255) DEFAULT ''::character varying NOT NULL,
    created_at timestamp with time zone NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    consumed_at timestamp with time zone,
    CONSTRAINT eval_set_import_previews_pkey PRIMARY KEY (token),
    CONSTRAINT chk_eval_set_import_previews_status CHECK ((status)::text IN ('ready', 'consumed', 'expired'))
);

CREATE INDEX IF NOT EXISTS idx_eval_set_import_previews_status ON public.eval_set_import_previews(status);
CREATE INDEX IF NOT EXISTS idx_eval_set_import_previews_expires_at ON public.eval_set_import_previews(expires_at);
CREATE INDEX IF NOT EXISTS idx_eval_set_import_previews_user ON public.eval_set_import_previews(create_user_id);
