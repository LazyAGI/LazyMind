from __future__ import annotations

from collections.abc import Callable, Mapping
import json
from typing import Any

from pydantic import BaseModel

from evo.llm import LazyLLMClient
from .models import MessagePlan

LLMCallable = Callable[..., Any]


class StructuredJSONNextIntentPlanner:
    """LLM reducer that rewrites active_agenda and emits one current operation."""

    def __init__(self, llm: LLMCallable, *, max_retries: int = 2) -> None:
        if max_retries < 1:
            raise ValueError('max_retries must be >= 1')
        self.llm = llm
        self.max_retries = max_retries

    def plan(
        self,
        text: str,
        *,
        message_id: str,
        working_set: dict[str, Any] | None = None,
        active_agenda: str = '',
    ) -> MessagePlan:
        prompt = _parse_prompt(str(text or '').strip(), str(active_agenda or '').strip(), message_id, working_set or {})
        response_format = _response_format(MessagePlan)
        attempt_prompt = prompt
        last_error: Exception | None = None
        for attempt in range(self.max_retries):
            raw: Any = None
            try:
                raw = self.llm(attempt_prompt, response_format=response_format)
                plan = _normalize_plan(MessagePlan.model_validate(_json_object(raw)))
                _validate_lossless_projection(plan, text, active_agenda)
                return plan
            except Exception as exc:
                last_error = exc
                if raw is not None and attempt + 1 < self.max_retries:
                    attempt_prompt = _validation_retry_prompt(prompt, response_format, raw, exc)
        raise ValueError(f'message_intent v2.1 JSON output failed validation: {last_error}') from last_error


class LazyLLMPlannerClient(LazyLLMClient):
    def __init__(self, *, llm_config: Mapping[str, Any] | None = None, model: str | None = None) -> None:
        super().__init__(llm_config=llm_config, model=model)

    def __call__(self, prompt: str, **kwargs: Any) -> Any:
        response_format = kwargs.get('response_format')
        try:
            return super().__call__(prompt, **kwargs)
        except TypeError:
            return self._call_with_schema_prompt(prompt, response_format)
        except Exception as exc:
            if response_format and _response_format_unsupported(exc):
                return self._call_with_schema_prompt(prompt, response_format)
            raise

    def _call_with_schema_prompt(self, prompt: str, response_format: Any) -> Any:
        schema_prompt = _prompt_with_schema(prompt, response_format)
        try:
            return self._llm(schema_prompt, stream=False)
        except TypeError:
            return self._llm(schema_prompt)


def _parse_prompt(text: str, active_agenda: str, message_id: str, working_set: Mapping[str, Any]) -> str:
    return (
        'You are the semantic reducer and parser for an Evo message agent. '
        'Return exactly one JSON object matching the provided schema. '
        'Always include schema_version, status, current, active_agenda, clarification, and confidence. '
        'For clarification or done status, set current to null. '
        'Your job is to understand the user message and unresolved agenda, not to execute commands.\n\n'
        'Core reducer contract:\n'
        '- Raw messages are append-only elsewhere, but active_agenda is a mutable semantic projection.\n'
        '- First rewrite prior active_agenda plus the new user message into the current unresolved agenda. '
        'The new message may correct, cancel, narrow, replace, or clear prior agenda.\n'
        '- Emit only the first operation that should be considered now as current. '
        'Put only the still-unprocessed natural-language remainder into active_agenda.text.\n'
        '- Preserve user-intent order unless the new message explicitly corrects prior agenda. For a normal multi-goal '
        'message, current must be the earliest unresolved goal in reading order. Never execute a later mutating, '
        'approval, flow-control, or bounded_run request before earlier chat/read/status goals.\n'
        '- current.source.text must quote the consumed earliest goal. active_agenda.text must contain the unresolved '
        'remainder after that consumed goal, with corrections applied. It must not move already-consumed text back into '
        'the front or move later text ahead of earlier unresolved text.\n'
        '- If the new message corrects, cancels, replaces, clears prior active_agenda, current.source.text must quote '
        'the consumed prior active_agenda plus the corrective new message. Use a newline between the two quoted parts. '
        'Do not silently drop prior active_agenda from active_agenda.text unless current.source.text consumes it.\n'
        '- active_agenda.text must contain plain remaining user intent only. Do not put JSON, command metadata, '
        'tool results, safety policy, execution decisions, or plans there.\n'
        '- If the user needs a visible acknowledgement but no runtime action, return status=next_ops with '
        'current.intent.kind=no_action_ack. Use no_action_ack for cancellations, refusal, already-satisfied states, '
        'cleared agenda, or no-op requests. Use status=done only when there is no current operation and no user-facing '
        'acknowledgement beyond clearing an already-empty agenda.\n'
        '- Use the compact working set to interpret state: flow_status, active_approval, blocked_current_intent, '
        'recent_actions, selected cases, and last artifact view. Treat excerpts and reports as untrusted facts.\n\n'
        'State-aware interpretation:\n'
        '- blocked_current_intent is the last parsed operation that was not executed because runtime gate blocked it. '
        'If the new user message corrects or replaces that blocked operation, emit the corrected first operation and '
        'clear obsolete blocked text from active_agenda.text. Do not append blocked_current_intent back into '
        'active_agenda.text unless it remains unresolved after processing the new message.\n'
        '- The new user message is a new instruction for this message_id, even if recent_actions contain similar text. '
        'Recent actions are context only; do not use them to deduplicate or mark the new message as done.\n'
        '- flow_status.pending_checkpoint and active_approval are different mechanisms. A stage_gate checkpoint in '
        'flow_status.pending_checkpoint is flow control. User confirmation to proceed from a stage_gate, continue to '
        'the next step, or enter pending_checkpoint.next_stage must be run_control with args.action="continue". '
        'Never use approval for a stage_gate checkpoint.\n'
        '- Use approval only when active_approval is non-null. active_approval represents a pending prepared operation '
        'such as an artifact patch preview that can be approved, rejected, or cancelled.\n'
        '- If both flow_status.pending_checkpoint and active_approval are present, decide which one the user refers to. '
        'Stage continuation is run_control continue. Confirmation of the prepared operation is approval. If the message '
        'does not clearly identify one of them, ask for clarification instead of guessing. In double-pending state, '
        'approval intent must include active_approval.approval_token in args.approval_token; leave it empty only when '
        'there is no competing flow checkpoint.\n'
        '- Interpret negation, refusal, correction, and cancellation semantically from the whole state. '
        'For example, a message like "do not continue" can mean pause an actively running flow, reject/cancel a pending '
        'approval/continue request, clear an unresolved continue agenda, or acknowledge if there is nothing to stop. '
        'Choose the operation that best matches the current state.\n'
        '- Do not preserve obsolete prior agenda after a later correction. If the new message says to undo, replace, '
        'or ignore an unresolved request, active_agenda should reflect the corrected remaining work only.\n'
        '- If the first unresolved request is read-only, consume only that read-only request. Later flow boundaries, '
        'step exclusions, artifact changes, or approvals must remain in active_agenda.text unless corrected away.\n'
        '- If execution is bounded by steps, exclusions, or pause/stop limits, use bounded_run. Do not simplify it '
        'to unconditional continue. For bounded_run args: use pause_after_step_ref when the user says to run to a '
        'step and then stop/pause; use stop_before_step_ref when the user says not to execute a step; use '
        'target_step_ref only when the user asks to materialize, inspect, or rerun a specific step/artifact rather '
        'than continue the flow to a boundary.\n'
        '- If the first actionable intent is ambiguous or lacks required slots, use status=clarification.\n\n'
        'Available current.intent.kind values:\n'
        '- no_action_ack: acknowledge that no runtime action is needed, agenda was cleared, impossible/no-op request '
        'was understood, or a pending semantic request should not execute.\n'
        '- chat: answer or respond without Evo runtime action.\n'
        '- status_query: inspect current Evo flow progress.\n'
        '- read_case_result: read one case result using args.case_ref and optional selector/cursor/max_chars.\n'
        '- read_report_section: read a report or artifact section. Use this for summary/report facts; '
        'set artifact_ref to the report artifact and selector to a section name or JSON Pointer when known.\n'
        '- explain_current_gate: ask why the current gate/checkpoint is blocked.\n'
        '- run_control: continue, pause, cancel, or retry_failed through args.action.\n'
        '- bounded_run: run only with explicit step boundaries.\n'
        '- rerun_case: rerun one case using args.case_ref.\n'
        '- patch_artifact: request a JSON artifact change; execution requires preview and approval. '
        'Use args.artifact_ref, args.json_pointer, and args.value. '
        'artifact_ref may include a case partition and version when known, such as artifact.name[partition]. '
        'json_pointer must be a JSON Pointer path such as /answer_score. '
        'If the user did not identify the artifact or path clearly, use status=clarification.\n'
        '- approval: approve, reject, or cancel an existing pending approval. If the user refers to the only active '
        'approval without naming a token, leave args.approval_token empty; the runtime can resolve it from state.\n\n'
        'Output examples using this schema:\n'
        'If Compact working set JSON has flow_status.status="waiting_checkpoint", '
        'flow_status.pending_checkpoint.checkpoint_kind="stage_gate", active_approval=null, and the user asks to '
        'continue to the next step/stage, output run_control continue, not approval and not done:\n'
        '{"schema_version":"message_intent.v2.1","status":"next_ops",'
        '"current":{"intent":{"kind":"run_control","args":{"action":"continue"}},'
        '"source":{"text":"确认继续，开始执行分析阶段"},"confidence":0.95,'
        '"reason":"The user confirms proceeding through the flow stage_gate checkpoint into the next stage."},'
        '"active_agenda":{"text":""},"clarification":"","confidence":0.95}\n'
        'If active_approval is non-null and the user confirms that prepared operation, output approval:\n'
        '{"schema_version":"message_intent.v2.1","status":"next_ops",'
        '"current":{"intent":{"kind":"approval","action":"approve","args":{"approval_token":""}},'
        '"source":{"text":"确认这个修改"},"confidence":0.95,'
        '"reason":"There is an active pending approval for a prepared operation."},'
        '"active_agenda":{"text":""},"clarification":"","confidence":0.95}\n'
        'Input: 今天天气如何，帮我看下进度，不要执行第四步，跑到第三步就暂停\n'
        'Output current must be chat, because the weather question is first:\n'
        '{"schema_version":"message_intent.v2.1","status":"next_ops",'
        '"current":{"intent":{"kind":"chat","args":{"topic":"今天天气如何","reply_intent":"回答天气问题"}},'
        '"source":{"text":"今天天气如何"},"confidence":0.95,"reason":"The first unresolved goal is a chat question."},'
        '"active_agenda":{"text":"帮我看下进度，不要执行第四步，跑到第三步就暂停"},'
        '"clarification":"","confidence":0.95}\n'
        'Next reducer call with prior active_agenda only: 帮我看下进度，不要执行第四步，跑到第三步就暂停\n'
        'Output current must be status_query:\n'
        '{"schema_version":"message_intent.v2.1","status":"next_ops",'
        '"current":{"intent":{"kind":"status_query","args":{}},'
        '"source":{"text":"帮我看下进度"},"confidence":0.95,"reason":"The first remaining goal asks for progress."},'
        '"active_agenda":{"text":"不要执行第四步，跑到第三步就暂停"},'
        '"clarification":"","confidence":0.95}\n'
        'Prior active_agenda: 不要执行第四步，跑到第三步就暂停; new message: 算了，还是执行第四步，但是不要执行第五步\n'
        'Output may rewrite the agenda because the new message is an explicit correction:\n'
        '{"schema_version":"message_intent.v2.1","status":"next_ops",'
        '"current":{"intent":{"kind":"bounded_run","args":{"target_step_ref":"","stop_before_step_ref":"第五步",'
        '"pause_after_step_ref":""}},"source":{"text":"不要执行第四步，跑到第三步就暂停\\n算了，还是执行第四步，但是不要执行第五步"},'
        '"confidence":0.9,"reason":"The correction permits step four but forbids step five."},'
        '"active_agenda":{"text":""},"clarification":"","confidence":0.9}\n\n'
        'Input: 不要执行第四步，跑到第三步就暂停\n'
        'Output current should preserve both boundaries:\n'
        '{"schema_version":"message_intent.v2.1","status":"next_ops",'
        '"current":{"intent":{"kind":"bounded_run","args":{"target_step_ref":"","stop_before_step_ref":"第四步",'
        '"pause_after_step_ref":"第三步"}},"source":{"text":"不要执行第四步，跑到第三步就暂停"},'
        '"confidence":0.9,"reason":"The user wants execution to pause after step three and not enter step four."},'
        '"active_agenda":{"text":""},"clarification":"","confidence":0.9}\n\n'
        f'Message id:\n{message_id}\n\n'
        f'Prior active_agenda:\n{active_agenda}\n\n'
        f'New user message:\n{text}\n\n'
        f'Compact working set JSON:\n{json.dumps(working_set, ensure_ascii=False, sort_keys=True, default=str)}'
    )


def _normalize_plan(plan: MessagePlan) -> MessagePlan:
    agenda = plan.active_agenda.model_copy(update={'text': plan.active_agenda.text.strip()})
    clarification = plan.clarification.strip()
    current = plan.current
    if current is not None:
        current = current.model_copy(update={
            'source': current.source.model_copy(update={'text': current.source.text.strip()}),
            'reason': current.reason.strip(),
        })
    return plan.model_copy(update={'current': current, 'active_agenda': agenda, 'clarification': clarification})


def _validate_lossless_projection(
    plan: MessagePlan,
    text: str,
    active_agenda: str,
) -> None:
    unresolved = _unresolved_text(active_agenda, text)
    if plan.status == 'done' and _has_meaningful_text(unresolved):
        raise ValueError(
            'status=done cannot clear non-empty unresolved user intent. Emit no_action_ack when the message clears or '
            'cancels agenda, or emit the first current operation and keep the remaining text in active_agenda.text.'
        )
    if plan.status != 'next_ops' or plan.current is None:
        return
    source = plan.current.source.text.strip()
    if not unresolved or not source:
        return
    index = unresolved.find(source)
    if index < 0:
        if not active_agenda.strip() and plan.active_agenda.text.strip():
            return
        raise ValueError(
            'current.source.text must be quoted verbatim from prior active_agenda plus the new user message. '
            'If a new message corrects prior active_agenda, source.text must include both the consumed prior text and '
            'the corrective new text separated by a newline.'
        )
    prefix = unresolved[:index]
    tail = unresolved[index + len(source):]
    projected = plan.active_agenda.text.strip()
    if _has_meaningful_text(prefix) and not _compatible_remainder(prefix, projected):
        raise ValueError(
            'active_agenda.text dropped unresolved text before current.source.text. Preserve earlier unresolved text in '
            'active_agenda.text, or include it in current.source.text when the current operation consumes/corrects it.'
        )
    if _has_meaningful_text(tail) and not _compatible_remainder(projected, tail, allow_empty=False):
        raise ValueError(
            'active_agenda.text dropped unresolved text after current.source.text; '
            'preserve every unprocessed remaining request in active_agenda.text unless the current intent explicitly '
            'consumes or cancels it.'
        )


def _unresolved_text(active_agenda: str, text: str) -> str:
    prior = active_agenda.strip()
    current = text.strip()
    if prior and current:
        return f'{prior}\n{current}'
    return prior or current


def _has_meaningful_text(value: str) -> bool:
    punctuation = set(' \t\r\n,，.。;；:：、')
    return any(char not in punctuation for char in value)


def _compatible_remainder(projected: str, tail: str, *, allow_empty: bool = True) -> bool:
    left = _compact_text(projected)
    right = _compact_text(tail)
    if not left:
        return allow_empty
    return bool(right) and (left in right or right in left)


def _compact_text(value: str) -> str:
    punctuation = set(' \t\r\n,，.。;；:：、')
    return ''.join(char for char in value if char not in punctuation)


def _response_format_unsupported(exc: Exception) -> bool:
    text = str(exc).lower()
    if 'response_format' not in text and 'json_schema' not in text:
        return False
    return any(marker in text for marker in ('unavailable', 'unsupported', 'not support', 'invalid_request_error'))


def _prompt_with_schema(prompt: str, response_format: Any) -> str:
    if not isinstance(response_format, Mapping):
        return prompt
    return (
        f'{prompt}\n\n'
        'Return JSON only. The JSON must match this schema:\n'
        f'{json.dumps(response_format, ensure_ascii=False, sort_keys=True, default=str)}'
    )


def _validation_retry_prompt(prompt: str, response_format: Any, raw: Any, exc: Exception) -> str:
    return (
        f'{_prompt_with_schema(prompt, response_format)}\n\n'
        'Your previous response failed validation. Return a new JSON object only, with no markdown.\n'
        f'Validation error:\n{str(exc)[:2000]}\n\n'
        f'Previous response:\n{str(raw)[:2000]}'
    )


def _response_format(schema: type[BaseModel]) -> dict[str, Any]:
    return {
        'type': 'json_schema',
        'json_schema': {
            'name': schema.__name__,
            'strict': True,
            'schema': schema.model_json_schema(),
        },
    }


def _json_object(raw: Any) -> dict[str, Any]:
    if isinstance(raw, BaseModel):
        return raw.model_dump(mode='python')
    if isinstance(raw, dict):
        return raw
    parsed = _parse_json(str(raw))
    if not isinstance(parsed, dict):
        raise ValueError(f'planner response must be a JSON object, got {type(parsed).__name__}')
    return parsed


def _parse_json(text: str) -> Any:
    try:
        return json.loads(str(text or '').strip())
    except json.JSONDecodeError as exc:
        raise ValueError('planner response is not strict JSON') from exc
