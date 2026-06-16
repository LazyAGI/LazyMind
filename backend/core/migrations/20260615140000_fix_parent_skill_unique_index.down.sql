DROP INDEX IF EXISTS public.uniq_skill_resources_owner_parent_skill_name;

CREATE UNIQUE INDEX uniq_skill_resources_owner_parent_skill_name
    ON public.skill_resources(owner_user_id, skill_name)
    WHERE node_type = 'parent';
