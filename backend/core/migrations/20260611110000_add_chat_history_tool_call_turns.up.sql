-- 20260611110000_add_multi_answers_chat_history_tool_call_turns
-- +migrate Up

ALTER TABLE public.multi_answers_chat_histories
    ADD COLUMN IF NOT EXISTS tool_call_turns integer DEFAULT 0 NOT NULL;

ALTER TABLE public.multi_answers_chat_histories
    ADD CONSTRAINT chk_multi_answers_chat_histories_tool_call_turns_non_negative CHECK (tool_call_turns >= 0);
