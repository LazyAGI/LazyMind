CREATE TABLE IF NOT EXISTS public.skill_market_installs (
    market_item_id VARCHAR(36) NOT NULL,
    user_id VARCHAR(255) NOT NULL,
    skill_id VARCHAR(36) NOT NULL,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    PRIMARY KEY (market_item_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_skill_market_installs_user
    ON public.skill_market_installs(user_id, market_item_id);

CREATE INDEX IF NOT EXISTS idx_skill_market_installs_skill
    ON public.skill_market_installs(skill_id);

INSERT INTO public.skill_market_installs (
    market_item_id,
    user_id,
    skill_id,
    created_at,
    updated_at
)
SELECT DISTINCT ON (market_items.id, user_skills.owner_user_id)
    market_items.id AS market_item_id,
    user_skills.owner_user_id AS user_id,
    user_skills.id AS skill_id,
    revisions.created_at AS created_at,
    COALESCE(user_skills.updated_at, revisions.created_at) AS updated_at
FROM public.skill_market_items AS market_items
JOIN public.skill_revisions AS revisions
    ON revisions.source_ref_type = 'skill'
   AND revisions.source_ref_id = market_items.source_skill_id
   AND revisions.change_source = 'market_install'
JOIN public.skills AS user_skills
    ON user_skills.id = revisions.skill_id
   AND user_skills.deleted_at IS NULL
WHERE user_skills.owner_user_id <> ''
ORDER BY market_items.id, user_skills.owner_user_id, revisions.created_at DESC
ON CONFLICT (market_item_id, user_id) DO UPDATE SET
    skill_id = EXCLUDED.skill_id,
    updated_at = EXCLUDED.updated_at;
