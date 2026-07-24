DROP INDEX IF EXISTS public.uk_skill_resources_owner_origin_builtin_uid;

DROP INDEX IF EXISTS public.idx_skill_resources_origin_builtin_uid;

ALTER TABLE public.skill_resources
    DROP COLUMN IF EXISTS origin_builtin_skill_uid;
