ALTER TABLE public.skill_review_results
ADD COLUMN IF NOT EXISTS category text DEFAULT '' NOT NULL;

CREATE INDEX IF NOT EXISTS idx_skill_review_results_pending_identity
ON public.skill_review_results (userid, category, skill_name)
WHERE review_status = 'pending';
