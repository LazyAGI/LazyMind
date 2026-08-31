-- +migrate Dialect postgres
CREATE TABLE IF NOT EXISTS public.skill_distribution_artifacts (
    archive_sha256 VARCHAR(64) PRIMARY KEY,
    builtin_skill_uid VARCHAR(64) NOT NULL,
    version VARCHAR(64) NOT NULL,
    tree_sha256 VARCHAR(64) NOT NULL,
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_skill_distribution_artifacts_uid_version
    ON public.skill_distribution_artifacts(builtin_skill_uid, version);

CREATE TABLE IF NOT EXISTS public.skill_distribution_entries (
    archive_sha256 VARCHAR(64) NOT NULL,
    path VARCHAR(1024) NOT NULL,
    entry_type VARCHAR(16) NOT NULL,
    blob_hash VARCHAR(64),
    size BIGINT NOT NULL DEFAULT 0,
    mime VARCHAR(128) NOT NULL DEFAULT '',
    file_type VARCHAR(32) NOT NULL DEFAULT 'unknown',
    "binary" BOOLEAN NOT NULL DEFAULT FALSE,
    mode INTEGER NOT NULL DEFAULT 420,
    PRIMARY KEY (archive_sha256, path)
);
CREATE INDEX IF NOT EXISTS idx_skill_distribution_entries_blob
    ON public.skill_distribution_entries(blob_hash);

CREATE TABLE IF NOT EXISTS public.skill_distribution_bindings (
    skill_id VARCHAR(36) PRIMARY KEY,
    builtin_skill_uid VARCHAR(64) NOT NULL,
    current_archive_sha256 VARCHAR(64) NOT NULL,
    pending_archive_sha256 VARCHAR(64) NOT NULL DEFAULT '',
    conflicts JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL,
    updated_at TIMESTAMP WITHOUT TIME ZONE NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_skill_distribution_bindings_uid
    ON public.skill_distribution_bindings(builtin_skill_uid);

CREATE TABLE IF NOT EXISTS public.skill_revision_distributions (
    revision_id VARCHAR(36) PRIMARY KEY,
    archive_sha256 VARCHAR(64) NOT NULL,
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_skill_revision_distributions_archive
    ON public.skill_revision_distributions(archive_sha256);

-- +migrate Dialect sqlite
CREATE TABLE IF NOT EXISTS `skill_distribution_artifacts` (`archive_sha256` varchar(64),`builtin_skill_uid` varchar(64) NOT NULL,`version` varchar(64) NOT NULL,`tree_sha256` varchar(64) NOT NULL,`created_at` datetime NOT NULL,PRIMARY KEY (`archive_sha256`));
CREATE INDEX IF NOT EXISTS `idx_skill_distribution_artifacts_uid_version` ON `skill_distribution_artifacts`(`builtin_skill_uid`,`version`);

CREATE TABLE IF NOT EXISTS `skill_distribution_entries` (`archive_sha256` varchar(64) NOT NULL,`path` varchar(1024) NOT NULL,`entry_type` varchar(16) NOT NULL,`blob_hash` varchar(64),`size` integer NOT NULL DEFAULT 0,`mime` varchar(128) NOT NULL DEFAULT "",`file_type` varchar(32) NOT NULL DEFAULT "unknown",`binary` numeric NOT NULL DEFAULT false,`mode` integer NOT NULL DEFAULT 420,PRIMARY KEY (`archive_sha256`,`path`));
CREATE INDEX IF NOT EXISTS `idx_skill_distribution_entries_blob` ON `skill_distribution_entries`(`blob_hash`);

CREATE TABLE IF NOT EXISTS `skill_distribution_bindings` (`skill_id` varchar(36),`builtin_skill_uid` varchar(64) NOT NULL,`current_archive_sha256` varchar(64) NOT NULL,`pending_archive_sha256` varchar(64) NOT NULL DEFAULT "",`conflicts` json NOT NULL DEFAULT '[]',`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`skill_id`));
CREATE INDEX IF NOT EXISTS `idx_skill_distribution_bindings_uid` ON `skill_distribution_bindings`(`builtin_skill_uid`);

CREATE TABLE IF NOT EXISTS `skill_revision_distributions` (`revision_id` varchar(36),`archive_sha256` varchar(64) NOT NULL,`created_at` datetime NOT NULL,PRIMARY KEY (`revision_id`));
CREATE INDEX IF NOT EXISTS `idx_skill_revision_distributions_archive` ON `skill_revision_distributions`(`archive_sha256`);
