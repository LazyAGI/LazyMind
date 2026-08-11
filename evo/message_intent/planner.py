from __future__ import annotations

import json
from collections.abc import Mapping
from typing import Any

from evo.llm import LazyLLMClient, parse_json_object

from .schemas import TurnPlan


PROMPT = """
Translate one user message into one strict EVO TurnPlan JSON object.
Return JSON only. Do not use markdown or explanations.

The next action kinds are:
- flow: start, approve, pause, resume, retry, cancel. approve requires a stage.
  retry accepts an optional stage when the user names or numbers a stage.
- query: progress, stage_result, artifact, artifact_history.
- artifact: patch, replace, retry, rollback. Always use an artifact id from intent_catalog.
- case: add or delete. add accepts either a complete case object or an instruction.
- config_patch: patch one named product configuration with a JSON pointer.
- confirmation: respond to the pending destructive-action confirmation.
- clarify or final.

Artifact retry means rerun the producer of one concrete artifact with the same inputs.
Flow retry reruns a complete stage and its downstream stages. Without a stage it reruns
the current failed stage.
Do not invent rerun_step, rerun_case_stage, invalidate_from_step, continue, or patch_collection.
Changing an artifact, rolling back, adding/deleting a case, and cancelling a run require a
separate confirmation. Return the executable action first; the application creates that
confirmation. Only return confirmation when projection.has_pending_confirmation is true.
Stage approval is a flow approve action and is different from destructive-action confirmation.

Use intent_catalog as the source of truth for stages, artifact ids and configuration targets.
Resolve ordinal stages from the catalog order. Pick exactly one action. Put remaining user goals
in active_agenda. Never claim that a long-running flow has completed; only describe the action.
If information is missing, return needs_input with a clarify action.
"""


class StructuredPlanError(ValueError):
    pass


def plan_next_turn(context: Mapping[str, Any],
                   llm_config: Mapping[str, Any]
                   ) -> TurnPlan:
    schema = TurnPlan.model_json_schema()
    client = LazyLLMClient(llm_config=llm_config, model='evo_llm')
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
            raw = client(prompt, stream=False, response_format={'type': 'json_object'})
            return TurnPlan.model_validate(parse_json_object(raw))
        except Exception as exc:
            error = str(exc)
    raise StructuredPlanError(error or 'LLM did not return a valid turn plan')


def answer_query(context: Mapping[str, Any], result: object,
                 llm_config: Mapping[str, Any]
                 ) -> str:
    prompt = (
        '你是 EVO 的只读查询回答器。只根据 query_result 和 flow_snapshot 回答，'
        '不编造，不发起操作，用简洁中文直接回答。\n'
        f'Context: {_json(context)}\n'
        f'Query result: {_json(result)}'
    )
    try:
        client = LazyLLMClient(llm_config=llm_config, model='evo_llm')
        return str(client(prompt, stream=False)).strip()
    except Exception:
        return '已读取当前信息，详细结果已写入本次查询记录。'


def _json(value: object) -> str:
    text = json.dumps(value, ensure_ascii=False, sort_keys=True, default=str)
    return text if len(text) <= 12000 else text[:12000]


__all__ = ['StructuredPlanError', 'answer_query', 'plan_next_turn']
