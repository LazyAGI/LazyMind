-- +migrate Dialect postgres
ALTER TABLE public.knowledge_market_items
    DROP COLUMN IF EXISTS source_adapter,
    DROP COLUMN IF EXISTS source_options;

-- +migrate Dialect sqlite
ALTER TABLE `knowledge_market_items` DROP COLUMN `source_adapter`;
ALTER TABLE `knowledge_market_items` DROP COLUMN `source_options`;
