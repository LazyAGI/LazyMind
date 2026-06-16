DROP INDEX IF EXISTS public.idx_skill_review_results_pending_identity;

ALTER TABLE public.skill_review_results
DROP COLUMN IF EXISTS category;
