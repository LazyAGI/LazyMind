-- +migrate Dialect postgres
-- M2: install pipeline for official knowledge bases.
-- Drop package metadata that is no longer maintained in the catalog; the
-- download source is a third-party URL and hashes are computed at install time.
ALTER TABLE public.knowledge_market_items
    DROP COLUMN IF EXISTS package_sha256,
    DROP COLUMN IF EXISTS package_size,
    DROP COLUMN IF EXISTS doc_count,
    DROP COLUMN IF EXISTS files;

ALTER TABLE public.knowledge_market_items
    ADD COLUMN IF NOT EXISTS package_revision VARCHAR(64) NOT NULL DEFAULT '';

-- Track the installation lifecycle and the resulting personal dataset.
ALTER TABLE public.knowledge_market_installs
    ADD COLUMN IF NOT EXISTS dataset_id VARCHAR(64) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS install_state VARCHAR(32) NOT NULL DEFAULT 'pending',
    ADD COLUMN IF NOT EXISTS installed_at TIMESTAMP WITHOUT TIME ZONE NULL,
    ADD COLUMN IF NOT EXISTS config JSONB NOT NULL DEFAULT '{}'::jsonb;

-- +migrate Dialect sqlite
ALTER TABLE `knowledge_market_items` DROP COLUMN `package_sha256`;
ALTER TABLE `knowledge_market_items` DROP COLUMN `package_size`;
ALTER TABLE `knowledge_market_items` DROP COLUMN `doc_count`;
ALTER TABLE `knowledge_market_items` DROP COLUMN `files`;
ALTER TABLE `knowledge_market_items` ADD COLUMN `package_revision` varchar(64) NOT NULL DEFAULT "";

ALTER TABLE `knowledge_market_installs` ADD COLUMN `dataset_id` varchar(64) NOT NULL DEFAULT "";
ALTER TABLE `knowledge_market_installs` ADD COLUMN `install_state` varchar(32) NOT NULL DEFAULT "pending";
ALTER TABLE `knowledge_market_installs` ADD COLUMN `installed_at` datetime NULL;
ALTER TABLE `knowledge_market_installs` ADD COLUMN `config` json NOT NULL DEFAULT '{}';
