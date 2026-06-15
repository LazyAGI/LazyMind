-- 20260612120000_create_resource_versions
-- +migrate Up

CREATE TABLE IF NOT EXISTS public.resource_versions (
    id character varying(36) NOT NULL,
    resource_type character varying(32) NOT NULL,
    resource_id character varying(128) NOT NULL,
    user_id character varying(255) NOT NULL,
    change_source character varying(32) NOT NULL,
    from_version bigint DEFAULT 0 NOT NULL,
    to_version bigint DEFAULT 0 NOT NULL,
    source_ref_type character varying(64) DEFAULT ''::character varying NOT NULL,
    source_ref_id character varying(128) DEFAULT ''::character varying NOT NULL,
    before_content text DEFAULT ''::text NOT NULL,
    after_content text DEFAULT ''::text NOT NULL,
    diff text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT resource_versions_pkey PRIMARY KEY (id),
    CONSTRAINT chk_resource_versions_resource_type CHECK (resource_type IN ('skill', 'memory', 'user_preference')),
    CONSTRAINT chk_resource_versions_change_source CHECK (change_source IN ('direct_save', 'draft_confirm', 'review_accept', 'auto_apply', 'internal_direct'))
);

CREATE INDEX IF NOT EXISTS idx_resource_versions_resource
ON public.resource_versions (resource_type, resource_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_resource_versions_user_resource
ON public.resource_versions (user_id, resource_type, resource_id, created_at DESC);
