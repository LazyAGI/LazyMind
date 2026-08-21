-- +migrate Dialect postgres
-- Source-specific knowledge base ingestion: the catalog selects an adapter and
-- stores its free-form options for install/update time.
ALTER TABLE public.knowledge_market_items
    ADD COLUMN IF NOT EXISTS source_adapter VARCHAR(64) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS source_options JSONB NOT NULL DEFAULT '{}'::jsonb;

-- +migrate Dialect sqlite
ALTER TABLE `knowledge_market_items` ADD COLUMN `source_adapter` varchar(64) NOT NULL DEFAULT "";
ALTER TABLE `knowledge_market_items` ADD COLUMN `source_options` json NOT NULL DEFAULT '{}';
