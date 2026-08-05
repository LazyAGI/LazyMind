-- 20260611100000_create_skill_review_tables
-- +migrate Down
-- +migrate Dialect postgres

DROP TABLE IF EXISTS public.memory_review;
DROP TABLE IF EXISTS public.skill_review_run_stats;
DROP TABLE IF EXISTS public.skill_review_stats;
DROP TABLE IF EXISTS public.skill_review_results;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
