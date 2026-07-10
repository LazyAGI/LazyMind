ALTER TABLE public.skills
    ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMP,
    ADD COLUMN IF NOT EXISTS deleted_by VARCHAR(255);

DROP INDEX IF EXISTS public.uk_skills_owner_identity;
DROP INDEX IF EXISTS public.uk_skills_owner_relative_root;

CREATE UNIQUE INDEX IF NOT EXISTS uk_skills_owner_identity
    ON public.skills(owner_user_id, category, skill_name)
    WHERE deleted_at IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS uk_skills_owner_relative_root
    ON public.skills(owner_user_id, relative_root)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_skills_owner_deleted
    ON public.skills(owner_user_id, deleted_at);
