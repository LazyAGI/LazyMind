ALTER TABLE public.system_memories
    ADD COLUMN IF NOT EXISTS agent_persona text DEFAULT ''::text NOT NULL,
    ADD COLUMN IF NOT EXISTS user_address text DEFAULT ''::text NOT NULL,
    ADD COLUMN IF NOT EXISTS response_style text DEFAULT ''::text NOT NULL;

UPDATE public.system_memories AS mem
SET
    agent_persona = CASE WHEN mem.agent_persona = '' THEN pref.agent_persona ELSE mem.agent_persona END,
    user_address = CASE WHEN mem.user_address = '' THEN pref.user_address ELSE mem.user_address END,
    response_style = CASE WHEN mem.response_style = '' THEN pref.response_style ELSE mem.response_style END
FROM public.system_user_preferences AS pref
WHERE mem.user_id = pref.user_id
    AND (
        mem.agent_persona = ''
        OR mem.user_address = ''
        OR mem.response_style = ''
    );

ALTER TABLE public.system_user_preferences
    DROP COLUMN IF EXISTS response_style,
    DROP COLUMN IF EXISTS user_address,
    DROP COLUMN IF EXISTS agent_persona;
