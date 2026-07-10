DROP INDEX IF EXISTS public.idx_skills_owner_deleted;
DROP INDEX IF EXISTS public.uk_skills_owner_identity;
DROP INDEX IF EXISTS public.uk_skills_owner_relative_root;

CREATE UNIQUE INDEX IF NOT EXISTS uk_skills_owner_identity
    ON public.skills(owner_user_id, category, skill_name);

CREATE UNIQUE INDEX IF NOT EXISTS uk_skills_owner_relative_root
    ON public.skills(owner_user_id, relative_root);

ALTER TABLE public.skills
    DROP COLUMN IF EXISTS deleted_by,
    DROP COLUMN IF EXISTS deleted_at;
