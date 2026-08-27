-- 20260827120000_unique_skill_name_per_owner
-- +migrate Down
-- +migrate Dialect postgres

DROP INDEX IF EXISTS public.uk_skills_owner_identity;
CREATE UNIQUE INDEX uk_skills_owner_identity
    ON public.skills(owner_user_id, category, skill_name)
    WHERE deleted_at IS NULL;

DROP INDEX IF EXISTS public.uk_skills_owner_relative_root;
CREATE UNIQUE INDEX uk_skills_owner_relative_root
    ON public.skills(owner_user_id, relative_root)
    WHERE deleted_at IS NULL;

-- +migrate Dialect sqlite

DROP INDEX IF EXISTS uk_skills_owner_identity;
CREATE UNIQUE INDEX uk_skills_owner_identity
    ON skills(owner_user_id, category, skill_name);

DROP INDEX IF EXISTS uk_skills_owner_relative_root;
CREATE UNIQUE INDEX uk_skills_owner_relative_root
    ON skills(owner_user_id, relative_root);
