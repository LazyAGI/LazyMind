-- +migrate Dialect postgres
DROP INDEX IF EXISTS public.uniq_skill_resources_owner_parent_skill_name;

CREATE UNIQUE INDEX uniq_skill_resources_owner_parent_skill_name
    ON public.skill_resources(owner_user_id, category, skill_name)
    WHERE node_type = 'parent';

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
