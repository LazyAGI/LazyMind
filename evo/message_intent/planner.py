from __future__ import annotations

import json
import re
from collections.abc import Mapping
from typing import Any

from json_repair import repair_json

from evo.llm import LazyLLMClient

from .schemas import TurnPlan

PROMPT = """
You translate one user message into a strict Evo message_intent TurnPlan.
Return only one JSON object. Do not explain. Do not use markdown.
Allowed next_action.kind: flow, query, mutation, config_patch, approval, clarify, final.
For flow/query/mutation/config_patch, set turn_decision to next_action.
For needs_input/final, put the response text in next_action.message.
Set assistant_text to a short user-facing reply for this turn.
For operations, say what will be submitted or requires confirmation; never claim a long flow already finished.
Use approval kind only when projection.has_pending_approval is true.
For a new risky operation that needs human confirmation, output the executable flow/mutation/config_patch action;
the code will create pending approval. Never output approval for a new operation.
Allowed flow command: continue, pause, resume, cancel, retry.
Allowed query: progress_snapshot, read_step_root, read_case_artifact.
Allowed mutation: edit_artifact, rerun_case_stage, rerun_step, invalidate_from_step.
Allowed config_patch target: run_config, source_config, target_config, eval_policy, repair_policy, candidate_config.
Use step_catalog as the source of truth for step order, names, and user-facing ordinal references.
Do not infer step numbers from current_step or from recent progress alone.
If pending_approval exists, use approval decision approve/reject/amend/replace/unclear,
or output a replacement executable action with user_message_effect amend/replace.
When projection.has_pending_approval is true, a user request to cancel/reject/stop
the pending confirmation means approval decision reject, not a flow cancel command.
For mixed requests, pick exactly one next_action for this turn and put the remaining
user goals in active_agenda. Do not emit multiple actions.
"""


class StructuredPlanError(ValueError):
    pass


def plan_next_turn(context: Mapping[str, Any], llm_config: Mapping[str, Any]) -> TurnPlan:
    schema = TurnPlan.model_json_schema()
    llm = LazyLLMClient(llm_config=llm_config, model='evo_llm')
    error = ''
    raw: Any = None
    for _ in range(2):
        retry_note = f'\nPrevious validation error: {error}' if error else ''
        prompt = (
            f'{PROMPT}\n'
            f'TurnPlan JSON schema: {json.dumps(schema, ensure_ascii=False)}\n'
            f'Context: {json.dumps(context, ensure_ascii=False, sort_keys=True, default=str)}'
            f'{retry_note}'
        )
        try:
            raw = llm(prompt, stream=False, response_format={'type': 'json_object'})
            return TurnPlan.model_validate(_json_object(raw))
        except Exception as exc:
            error = str(exc)
    snippet = re.sub(r'\s+', ' ', str(raw or '')).strip()[:500]
    raise StructuredPlanError(f'{error}; response={snippet}')


def answer_query(context: Mapping[str, Any], result: object, llm_config: Mapping[str, Any]) -> str:
    prompt = (
        '你是 Evo message_intent 的只读查询回答器。'
        '只根据 query_result 和 flow_snapshot 回答用户问题，不编造，不发起操作。'
        '用简洁中文直接回答。\n'
        f'Context: {json.dumps(context, ensure_ascii=False, sort_keys=True, default=str)}\n'
        f'Query result: {_json(result)}'
    )
    try:
        return str(LazyLLMClient(llm_config=llm_config, model='evo_llm')(prompt, stream=False)).strip()
    except Exception:
        return '已读取当前信息，详细结果已写入 observation。'


def _json(value: object) -> str:
    text = json.dumps(value, ensure_ascii=False, sort_keys=True, default=str)
    return text if len(text) <= 12000 else text[:12000]


def _json_object(raw: Any) -> Mapping[str, Any]:
    if isinstance(raw, Mapping):
        return raw
    text = re.sub(r'<think>.*?</think>', '', str(raw), flags=re.S).strip()
    fenced = re.search(r'```(?:json)?\s*(\{.*\})\s*```', text, re.S)
    if fenced:
        text = fenced.group(1)
    else:
        start, end = text.find('{'), text.rfind('}')
        if start >= 0 and end > start:
            text = text[start:end + 1]
    value = repair_json(text, return_objects=True)
    if not isinstance(value, Mapping):
        raise ValueError(f'LLM response JSON must be an object, got {type(value).__name__}')
    return value
