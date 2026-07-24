-- 20260531090000_create_eval_set_tables
-- +migrate Up

CREATE TABLE public.eval_set_shards (
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

CREATE INDEX idx_eval_set_shards_status ON public.eval_set_shards(status);

INSERT INTO public.eval_set_shards (
    id, status, row_limit, row_open_threshold, size_limit_bytes,
    size_open_threshold_bytes, actual_rows, estimated_bytes,
    created_at, updated_at
) VALUES (
    'eval_shard_0001', 'open', 200000, 120000, 8589934592,
    5368709120, 0, 0, now(), now()
) ON CONFLICT (id) DO NOTHING;

CREATE TABLE public.eval_sets (
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

CREATE INDEX idx_eval_sets_owner ON public.eval_sets(owner_id);
CREATE INDEX idx_eval_sets_group ON public.eval_sets(group_id);
CREATE INDEX idx_eval_sets_dataset_ids ON public.eval_sets USING gin (dataset_ids);
CREATE INDEX idx_eval_sets_shard ON public.eval_sets(shard_id);
CREATE INDEX idx_eval_sets_status ON public.eval_sets(status);

CREATE TABLE public.eval_set_items (
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
)
PARTITION BY LIST (shard_id);

COMMENT ON COLUMN public.eval_set_items.is_deleted
    IS 'Template/business field imported from eval-set files; not a logical-delete marker. System deletion is physical DELETE.';

CREATE TABLE public.eval_set_items_p_eval_shard_0001
PARTITION OF public.eval_set_items
FOR VALUES IN ('eval_shard_0001');

CREATE INDEX idx_eval_set_items_set_created
    ON public.eval_set_items(shard_id, eval_set_id, created_at DESC);
CREATE INDEX idx_eval_set_items_set_source
    ON public.eval_set_items(shard_id, eval_set_id, source);
CREATE INDEX idx_eval_set_items_set_type
    ON public.eval_set_items(shard_id, eval_set_id, question_type);
CREATE INDEX idx_eval_set_items_set_updated
    ON public.eval_set_items(shard_id, eval_set_id, updated_at DESC);
