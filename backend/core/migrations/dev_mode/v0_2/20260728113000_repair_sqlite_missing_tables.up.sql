-- Repair SQLite tables omitted from the v0.2 aggregate snapshot.
-- +migrate Dialect postgres
SELECT 1;

-- +migrate Dialect sqlite
CREATE TABLE IF NOT EXISTS `skill_market_installs` (`market_item_id` varchar(36) NOT NULL,`user_id` varchar(255) NOT NULL,`skill_id` varchar(36) NOT NULL,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`market_item_id`,`user_id`));

CREATE INDEX IF NOT EXISTS `idx_skill_market_installs_user` ON `skill_market_installs`(`user_id`,`market_item_id`);

CREATE INDEX IF NOT EXISTS `idx_skill_market_installs_skill` ON `skill_market_installs`(`skill_id`);

INSERT INTO skill_market_installs (
    market_item_id,
    user_id,
    skill_id,
    created_at,
    updated_at
)
SELECT
    market_item_id,
    user_id,
    skill_id,
    created_at,
    updated_at
FROM (
    SELECT
        market_items.id AS market_item_id,
        user_skills.owner_user_id AS user_id,
        user_skills.id AS skill_id,
        revisions.created_at AS created_at,
        COALESCE(user_skills.updated_at, revisions.created_at) AS updated_at,
        ROW_NUMBER() OVER (
            PARTITION BY market_items.id, user_skills.owner_user_id
            ORDER BY revisions.created_at DESC
        ) AS install_rank
    FROM skill_market_items AS market_items
    JOIN skill_revisions AS revisions
        ON revisions.source_ref_type = 'skill'
       AND revisions.source_ref_id = market_items.source_skill_id
       AND revisions.change_source = 'market_install'
    JOIN skills AS user_skills
        ON user_skills.id = revisions.skill_id
       AND user_skills.deleted_at IS NULL
    WHERE user_skills.owner_user_id <> ''
) AS ranked_installs
WHERE install_rank = 1
ON CONFLICT (market_item_id, user_id) DO UPDATE SET
    skill_id = excluded.skill_id,
    updated_at = excluded.updated_at;

CREATE TABLE IF NOT EXISTS `plugin_generation_analyses` (`id` varchar(36),`draft_id` varchar(36) NOT NULL,`user_id` varchar(255) NOT NULL,`source_type` varchar(16) NOT NULL,`source_skill_id` varchar(36) NOT NULL DEFAULT "",`source_skill_revision_id` varchar(36) NOT NULL DEFAULT "",`source_skill_revision_no` integer NOT NULL DEFAULT 0,`source_skill_tree_hash` varchar(64) NOT NULL DEFAULT "",`status` varchar(32) NOT NULL,`verdict_code` varchar(64) NOT NULL DEFAULT "",`verdict_message` text NOT NULL DEFAULT "",`candidates_json` text NOT NULL DEFAULT "[]",`selected_candidate_id` varchar(128) NOT NULL DEFAULT "",`coverage_report_json` text NOT NULL DEFAULT "{}",`tool_mapping_report_json` text NOT NULL DEFAULT "{}",`script_report_json` text NOT NULL DEFAULT "{}",`source_package_json` text NOT NULL DEFAULT "{}",`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE INDEX IF NOT EXISTS `idx_plugin_generation_analyses_draft` ON `plugin_generation_analyses`(`draft_id`,`created_at`);

CREATE TABLE IF NOT EXISTS `plugin_repair_runs` (`id` varchar(36),`draft_id` varchar(36) NOT NULL,`user_id` varchar(255) NOT NULL,`base_plugin_revision_id` varchar(36) NOT NULL DEFAULT "",`draft_version_before` integer NOT NULL,`target` varchar(32) NOT NULL,`mode` varchar(32) NOT NULL,`source_analysis_id` varchar(36) NOT NULL DEFAULT "",`source_skill_revision_id` varchar(36) NOT NULL DEFAULT "",`repair_hint` text NOT NULL DEFAULT "",`diagnostics_before_json` text NOT NULL DEFAULT "{}",`changes_json` text NOT NULL DEFAULT "{}",`diagnostics_after_json` text NOT NULL DEFAULT "{}",`status` varchar(32) NOT NULL,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE INDEX IF NOT EXISTS `idx_plugin_repair_runs_draft` ON `plugin_repair_runs`(`draft_id`,`created_at`);
