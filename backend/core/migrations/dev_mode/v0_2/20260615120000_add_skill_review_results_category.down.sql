-- +migrate Dialect postgres
DROP INDEX IF EXISTS public.idx_skill_review_results_pending_identity;

ALTER TABLE public.skill_review_results
DROP COLUMN IF EXISTS category;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
