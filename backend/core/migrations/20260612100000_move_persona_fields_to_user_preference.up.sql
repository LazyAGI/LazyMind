ALTER TABLE public.system_user_preferences
    ADD COLUMN IF NOT EXISTS agent_persona text DEFAULT ''::text NOT NULL,
    ADD COLUMN IF NOT EXISTS user_address text DEFAULT ''::text NOT NULL,
    ADD COLUMN IF NOT EXISTS response_style text DEFAULT ''::text NOT NULL;

INSERT INTO public.system_user_preferences (
    id,
    user_id,
    content,
    agent_persona,
    user_address,
    response_style,
    content_hash,
    version,
    auto_evo,
    auto_evo_apply_status,
    auto_evo_error,
    updated_by,
    updated_by_name,
    created_at,
    updated_at
)
SELECT
    substr(h.hash, 1, 8) || '-' || substr(h.hash, 9, 4) || '-' || substr(h.hash, 13, 4) || '-' || substr(h.hash, 17, 4) || '-' || substr(h.hash, 21, 12),
    mem.user_id,
    '',
    mem.agent_persona,
    mem.user_address,
    mem.response_style,
    '',
    1,
    true,
    'idle',
    '',
    COALESCE(NULLIF(mem.updated_by, ''), mem.user_id, 'system'),
    mem.updated_by_name,
    NOW(),
    NOW()
FROM public.system_memories AS mem
CROSS JOIN LATERAL (SELECT md5('system_user_preference:' || mem.user_id) AS hash) AS h
WHERE NOT EXISTS (
    SELECT 1
    FROM public.system_user_preferences AS pref
    WHERE pref.user_id = mem.user_id
);

UPDATE public.system_user_preferences AS pref
SET
    agent_persona = CASE WHEN pref.agent_persona = '' THEN mem.agent_persona ELSE pref.agent_persona END,
    user_address = CASE WHEN pref.user_address = '' THEN mem.user_address ELSE pref.user_address END,
    response_style = CASE WHEN pref.response_style = '' THEN mem.response_style ELSE pref.response_style END
FROM public.system_memories AS mem
WHERE pref.user_id = mem.user_id
    AND (
        pref.agent_persona = ''
        OR pref.user_address = ''
        OR pref.response_style = ''
    );

ALTER TABLE public.system_memories
    DROP COLUMN IF EXISTS response_style,
    DROP COLUMN IF EXISTS user_address,
    DROP COLUMN IF EXISTS agent_persona;
