-- +migrate Dialect postgres
DROP INDEX IF EXISTS public.idx_skill_revision_distributions_archive;
DROP TABLE IF EXISTS public.skill_revision_distributions;
DROP INDEX IF EXISTS public.idx_skill_distribution_bindings_uid;
DROP TABLE IF EXISTS public.skill_distribution_bindings;
DROP INDEX IF EXISTS public.idx_skill_distribution_entries_blob;
DROP TABLE IF EXISTS public.skill_distribution_entries;
DROP INDEX IF EXISTS public.idx_skill_distribution_artifacts_uid_version;
DROP TABLE IF EXISTS public.skill_distribution_artifacts;

-- +migrate Dialect sqlite
DROP INDEX IF EXISTS `idx_skill_revision_distributions_archive`;
DROP TABLE IF EXISTS `skill_revision_distributions`;
DROP INDEX IF EXISTS `idx_skill_distribution_bindings_uid`;
DROP TABLE IF EXISTS `skill_distribution_bindings`;
DROP INDEX IF EXISTS `idx_skill_distribution_entries_blob`;
DROP TABLE IF EXISTS `skill_distribution_entries`;
DROP INDEX IF EXISTS `idx_skill_distribution_artifacts_uid_version`;
DROP TABLE IF EXISTS `skill_distribution_artifacts`;
