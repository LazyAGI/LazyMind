UPDATE public.skill_review_results
SET summary = ''
WHERE summary IS NULL;

ALTER TABLE public.skill_review_results
ALTER COLUMN userid SET DEFAULT '',
ALTER COLUMN requestid SET DEFAULT '',
ALTER COLUMN summary SET DEFAULT '',
ALTER COLUMN summary SET NOT NULL;
