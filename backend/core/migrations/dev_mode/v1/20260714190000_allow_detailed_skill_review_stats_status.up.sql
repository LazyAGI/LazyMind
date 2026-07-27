-- 20260714190000_allow_detailed_skill_review_stats_status
-- +migrate Up

ALTER TABLE public.skill_review_stats
    DROP CONSTRAINT IF EXISTS chk_skill_review_stats_status;
