-- 20260612110000_create_mcp_tables
-- +migrate Up

CREATE TABLE IF NOT EXISTS public.mcp_servers (
    id character varying(64) NOT NULL,
    create_user_id character varying(255) NOT NULL,
    create_user_name character varying(255) DEFAULT ''::character varying NOT NULL,
    name character varying(255) NOT NULL,
    transport character varying(32) NOT NULL,
    url text DEFAULT ''::text NOT NULL,
    headers_json json DEFAULT '{}'::json NOT NULL,
    allowed_tools_json json DEFAULT '[]'::json NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    is_verified boolean DEFAULT false NOT NULL,
    share boolean DEFAULT false NOT NULL,
    timeout integer DEFAULT 5 NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    deleted_at timestamp with time zone,
    CONSTRAINT mcp_servers_pkey PRIMARY KEY (id)
);

CREATE INDEX IF NOT EXISTS idx_mcp_servers_user
    ON public.mcp_servers USING btree (create_user_id, deleted_at);

CREATE INDEX IF NOT EXISTS idx_mcp_servers_share
    ON public.mcp_servers USING btree (share, enabled, deleted_at);

CREATE TABLE IF NOT EXISTS public.mcp_server_tools (
    id character varying(64) NOT NULL,
    mcp_server_id character varying(64) NOT NULL,
    tool_name character varying(255) NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    input_schema_json json DEFAULT '{}'::json NOT NULL,
    last_discovered_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    deleted_at timestamp with time zone,
    CONSTRAINT mcp_server_tools_pkey PRIMARY KEY (id)
);

CREATE INDEX IF NOT EXISTS idx_mcp_tools_server
    ON public.mcp_server_tools USING btree (mcp_server_id, deleted_at);
