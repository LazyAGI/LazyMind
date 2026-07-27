-- +migrate Dialect postgres
ALTER TABLE public.skill_review_results
ADD COLUMN IF NOT EXISTS category text DEFAULT '' NOT NULL;

CREATE INDEX IF NOT EXISTS idx_skill_review_results_pending_identity
ON public.skill_review_results (userid, category, skill_name)
WHERE review_status = 'pending';

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
