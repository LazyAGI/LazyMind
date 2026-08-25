-- +migrate Dialect postgres
-- P1 online query: public web page used when the official knowledge base is
-- not installed. Kept empty to hide the "online query" entry on the card.
ALTER TABLE public.knowledge_market_items
    ADD COLUMN IF NOT EXISTS online_access_url VARCHAR(1024) NOT NULL DEFAULT '';

-- +migrate Dialect sqlite
ALTER TABLE `knowledge_market_items` ADD COLUMN `online_access_url` varchar(1024) NOT NULL DEFAULT "";
