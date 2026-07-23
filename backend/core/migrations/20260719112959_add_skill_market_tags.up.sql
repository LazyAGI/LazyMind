ALTER TABLE public.skill_market_items
    ADD COLUMN IF NOT EXISTS tags JSONB NOT NULL DEFAULT '[]'::jsonb;

UPDATE public.skill_market_items AS market_items
SET tags = jsonb_build_array(BTRIM(skills.category))
FROM public.skills AS skills
WHERE skills.id = market_items.source_skill_id
  AND market_items.tags = '[]'::jsonb
  AND BTRIM(skills.category) <> ''
  AND LOWER(BTRIM(skills.category)) <> 'external';

UPDATE public.skills AS skills
SET owner_user_id = CONCAT('skill-market:', market_items.id),
    owner_user_name = 'skill-market',
    category = 'External',
    relative_root = CONCAT('External/', skills.skill_name),
    updated_at = CURRENT_TIMESTAMP
FROM public.skill_market_items AS market_items
WHERE skills.id = market_items.source_skill_id
  AND (
      skills.owner_user_id <> CONCAT('skill-market:', market_items.id)
      OR skills.category <> 'External'
      OR skills.relative_root <> CONCAT('External/', skills.skill_name)
  );

UPDATE public.skill_search_indexes AS search_indexes
SET owner_user_id = CONCAT('skill-market:', market_items.id),
    updated_at = CURRENT_TIMESTAMP
FROM public.skill_market_items AS market_items
WHERE search_indexes.skill_id = market_items.source_skill_id
  AND search_indexes.owner_user_id <> CONCAT('skill-market:', market_items.id);
