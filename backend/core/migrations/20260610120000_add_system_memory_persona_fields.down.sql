ALTER TABLE public.system_memories
    DROP COLUMN IF EXISTS response_style,
    DROP COLUMN IF EXISTS user_address,
    DROP COLUMN IF EXISTS agent_persona;
