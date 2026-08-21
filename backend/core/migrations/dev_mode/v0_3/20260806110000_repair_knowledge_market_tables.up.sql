-- Repair databases that applied the v0_3 aggregate before knowledge-market
-- tables were added to it. Fresh databases already have these tables, so all
-- statements are intentionally idempotent.
-- +migrate Dialect postgres
CREATE TABLE IF NOT EXISTS public.knowledge_market_items (
    id VARCHAR(64) PRIMARY KEY,
    category VARCHAR(32) NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    icon TEXT NOT NULL DEFAULT '',
    domain VARCHAR(64) NOT NULL DEFAULT '',
    tags JSONB NOT NULL DEFAULT '[]'::jsonb,
    version VARCHAR(32) NOT NULL DEFAULT '',
    version_date VARCHAR(10) NOT NULL DEFAULT '',
    version_note TEXT NOT NULL DEFAULT '',
    package_url TEXT NOT NULL DEFAULT '',
    package_sha256 VARCHAR(64) NOT NULL DEFAULT '',
    package_size BIGINT NOT NULL DEFAULT 0,
    doc_count BIGINT NOT NULL DEFAULT 0,
    data_source TEXT NOT NULL DEFAULT '',
    files JSONB NOT NULL DEFAULT '[]'::jsonb,
    sample_questions JSONB NOT NULL DEFAULT '[]'::jsonb,
    status VARCHAR(32) NOT NULL DEFAULT 'published',
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL,
    updated_at TIMESTAMP WITHOUT TIME ZONE NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_knowledge_market_items_category_status
    ON public.knowledge_market_items(status, category, sort_order);

CREATE TABLE IF NOT EXISTS public.knowledge_market_installs (
    market_item_id VARCHAR(64) NOT NULL,
    user_id VARCHAR(255) NOT NULL,
    installed_version VARCHAR(32) NOT NULL DEFAULT '',
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL,
    updated_at TIMESTAMP WITHOUT TIME ZONE NOT NULL,
    PRIMARY KEY (market_item_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_knowledge_market_installs_user
    ON public.knowledge_market_installs(user_id, market_item_id);

-- +migrate Dialect sqlite
CREATE TABLE IF NOT EXISTS `knowledge_market_items` (`id` varchar(64),`category` varchar(32) NOT NULL,`name` varchar(255) NOT NULL,`description` text NOT NULL DEFAULT "",`icon` text NOT NULL DEFAULT "",`domain` varchar(64) NOT NULL DEFAULT "",`tags` json NOT NULL DEFAULT '[]',`version` varchar(32) NOT NULL DEFAULT "",`version_date` varchar(10) NOT NULL DEFAULT "",`version_note` text NOT NULL DEFAULT "",`package_url` text NOT NULL DEFAULT "",`package_sha256` varchar(64) NOT NULL DEFAULT "",`package_size` integer NOT NULL DEFAULT 0,`doc_count` integer NOT NULL DEFAULT 0,`data_source` text NOT NULL DEFAULT "",`files` json NOT NULL DEFAULT '[]',`sample_questions` json NOT NULL DEFAULT '[]',`status` varchar(32) NOT NULL DEFAULT "published",`sort_order` integer NOT NULL DEFAULT 0,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE INDEX IF NOT EXISTS `idx_knowledge_market_items_category_status` ON `knowledge_market_items`(`status`,`category`,`sort_order`);

CREATE TABLE IF NOT EXISTS `knowledge_market_installs` (`market_item_id` varchar(64) NOT NULL,`user_id` varchar(255) NOT NULL,`installed_version` varchar(32) NOT NULL DEFAULT "",`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`market_item_id`,`user_id`));

CREATE INDEX IF NOT EXISTS `idx_knowledge_market_installs_user` ON `knowledge_market_installs`(`user_id`,`market_item_id`);
