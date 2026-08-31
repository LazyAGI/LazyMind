-- +migrate Dialect postgres
DROP INDEX IF EXISTS public.idx_knowledge_market_installs_user;
DROP TABLE IF EXISTS public.knowledge_market_installs;
DROP INDEX IF EXISTS public.idx_knowledge_market_items_category_status;
DROP TABLE IF EXISTS public.knowledge_market_items;

-- +migrate Dialect sqlite
DROP INDEX IF EXISTS `idx_knowledge_market_installs_user`;
DROP TABLE IF EXISTS `knowledge_market_installs`;
DROP INDEX IF EXISTS `idx_knowledge_market_items_category_status`;
DROP TABLE IF EXISTS `knowledge_market_items`;
