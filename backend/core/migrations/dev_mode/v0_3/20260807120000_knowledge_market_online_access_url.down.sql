-- +migrate Dialect postgres
ALTER TABLE public.knowledge_market_items
    DROP COLUMN IF EXISTS online_access_url;

-- +migrate Dialect sqlite
ALTER TABLE `knowledge_market_items` DROP COLUMN `online_access_url`;
