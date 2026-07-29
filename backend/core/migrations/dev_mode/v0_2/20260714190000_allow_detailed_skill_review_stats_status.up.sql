-- 20260714190000_allow_detailed_skill_review_stats_status
-- +migrate Up
-- +migrate Dialect postgres

ALTER TABLE public.skill_review_stats
    DROP CONSTRAINT IF EXISTS chk_skill_review_stats_status;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
