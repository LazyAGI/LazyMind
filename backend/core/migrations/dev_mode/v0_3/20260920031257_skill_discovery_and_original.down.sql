-- +migrate Dialect postgres
DROP INDEX IF EXISTS public.idx_skills_owner_call_mode_sort;
ALTER TABLE public.skills DROP COLUMN IF EXISTS keywords;
ALTER TABLE public.skills DROP COLUMN IF EXISTS aliases;
ALTER TABLE public.skills DROP COLUMN IF EXISTS field;
ALTER TABLE public.skills DROP COLUMN IF EXISTS original_revision_id;
ALTER TABLE public.skills DROP COLUMN IF EXISTS sort_rank;
ALTER TABLE public.skills DROP COLUMN IF EXISTS call_mode;

-- +migrate Dialect sqlite
DROP INDEX IF EXISTS idx_skills_owner_call_mode_sort;
ALTER TABLE skills DROP COLUMN keywords;
ALTER TABLE skills DROP COLUMN aliases;
ALTER TABLE skills DROP COLUMN field;
ALTER TABLE skills DROP COLUMN original_revision_id;
ALTER TABLE skills DROP COLUMN sort_rank;
ALTER TABLE skills DROP COLUMN call_mode;
