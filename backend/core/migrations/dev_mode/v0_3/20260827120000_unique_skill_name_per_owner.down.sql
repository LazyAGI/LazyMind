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

-- Partial unique indexes allowed a live skill to reuse a trashed name or
-- relative_root. Restore the historical full unique indexes by removing those
-- colliding trashed rows first.
DROP INDEX IF EXISTS uk_skills_owner_identity;
DROP INDEX IF EXISTS uk_skills_owner_relative_root;

DELETE FROM skills
WHERE id IN (
    SELECT id FROM (
        SELECT trashed.id
        FROM skills AS trashed
        INNER JOIN skills AS live
            ON live.deleted_at IS NULL
            AND live.owner_user_id = trashed.owner_user_id
            AND live.category = trashed.category
            AND live.skill_name = trashed.skill_name
        WHERE trashed.deleted_at IS NOT NULL
    ) AS identity_conflicts
);

DELETE FROM skills
WHERE id IN (
    SELECT id FROM (
        SELECT trashed.id
        FROM skills AS trashed
        INNER JOIN skills AS live
            ON live.deleted_at IS NULL
            AND live.owner_user_id = trashed.owner_user_id
            AND live.relative_root = trashed.relative_root
        WHERE trashed.deleted_at IS NOT NULL
    ) AS relative_root_conflicts
);

CREATE UNIQUE INDEX uk_skills_owner_identity
    ON skills(owner_user_id, category, skill_name);

CREATE UNIQUE INDEX uk_skills_owner_relative_root
    ON skills(owner_user_id, relative_root);
