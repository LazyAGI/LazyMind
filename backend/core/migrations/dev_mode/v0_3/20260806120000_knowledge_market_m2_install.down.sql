-- +migrate Dialect postgres
ALTER TABLE public.knowledge_market_items
    ADD COLUMN IF NOT EXISTS package_sha256 VARCHAR(64) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS package_size BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS doc_count BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS files JSONB NOT NULL DEFAULT '[]'::jsonb;
ALTER TABLE public.knowledge_market_items DROP COLUMN IF EXISTS package_revision;

ALTER TABLE public.knowledge_market_installs
    DROP COLUMN IF EXISTS dataset_id,
    DROP COLUMN IF EXISTS install_state,
    DROP COLUMN IF EXISTS installed_at,
    DROP COLUMN IF EXISTS config;

-- +migrate Dialect sqlite
ALTER TABLE `knowledge_market_items` ADD COLUMN `package_sha256` varchar(64) NOT NULL DEFAULT "";
ALTER TABLE `knowledge_market_items` ADD COLUMN `package_size` integer NOT NULL DEFAULT 0;
ALTER TABLE `knowledge_market_items` ADD COLUMN `doc_count` integer NOT NULL DEFAULT 0;
ALTER TABLE `knowledge_market_items` ADD COLUMN `files` json NOT NULL DEFAULT '[]';
ALTER TABLE `knowledge_market_items` DROP COLUMN `package_revision`;

ALTER TABLE `knowledge_market_installs` DROP COLUMN `dataset_id`;
ALTER TABLE `knowledge_market_installs` DROP COLUMN `install_state`;
ALTER TABLE `knowledge_market_installs` DROP COLUMN `installed_at`;
ALTER TABLE `knowledge_market_installs` DROP COLUMN `config`;
