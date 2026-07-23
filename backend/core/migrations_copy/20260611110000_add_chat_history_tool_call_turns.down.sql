-- 20260611110000_add_multi_answers_chat_history_tool_call_turns
-- +migrate Down

ALTER TABLE public.multi_answers_chat_histories
    DROP CONSTRAINT IF EXISTS chk_multi_answers_chat_histories_tool_call_turns_non_negative;

ALTER TABLE public.multi_answers_chat_histories
    DROP COLUMN IF EXISTS tool_call_turns;
