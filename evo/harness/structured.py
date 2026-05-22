from __future__ import annotations
import json
from typing import Any, Callable

try:
    from jsonschema import Draft202012Validator as _JsonSchemaValidator
except ImportError:
    from jsonschema import Draft7Validator as _JsonSchemaValidator
from evo.apply.errors import ApplyError
from evo.harness.react import LLMInvoker
from evo.runtime.config import EVO_MAX_SCHEMA_FAILURES
from evo.runtime.session import AnalysisSession
from evo.utils import strip_thinking

try:
    import json_repair
except ImportError:
    json_repair = None
_ERROR_LIMIT = 20
_PREVIEW_CHARS = 2000
_MAX_SCHEMA_FAILURES = EVO_MAX_SCHEMA_FAILURES
_REPAIR_SYSTEM_PROMPT = (
    'You are a strict JSON formatter. Your ONLY job is to take the user message '
    '(which contains a previous bad model output, validation errors, and the required JSON schema) '
    'and emit ONE JSON object that satisfies the schema. You MUST NOT invoke tools, '
    'MUST NOT use any tool-call syntax ([TOOL_CALL]/<invoke>/<tool_call>/Action:), '
    'MUST NOT include <think> tags, markdown fences, prose, or commentary. '
    'Use [] for empty arrays, never null. Enum fields must use exactly one listed value. '
    'Output ONLY the JSON object.'
)
_TYPE_PLACEHOLDER: dict[str, Any] = {'string': '<string>', 'integer': 0, 'number': 0.0, 'boolean': False, 'null': None}


def _format_error(err) -> str:
    path = '.'.join((str(p) for p in err.absolute_path)) or '<root>'
    return f'{path}: {err.message}'


def _parse(raw: str) -> tuple[dict, bool]:
    text = strip_thinking(raw or '').strip()
    if not text:
        return ({}, False)
    try:
        obj = json.loads(text)
        if isinstance(obj, dict):
            return (obj, False)
    except json.JSONDecodeError:
        pass
    obj = None
    if json_repair is not None:
        try:
            obj = json_repair.loads(text)
        except Exception:
            obj = None
    return (obj if isinstance(obj, dict) else {}, True)


def parse_and_validate(raw: str, schema: dict) -> tuple[dict, list[str], bool]:
    data, repaired = _parse(raw)
    validator = _JsonSchemaValidator(schema)
    errors = sorted(validator.iter_errors(data), key=lambda e: list(e.absolute_path))
    return (data, [_format_error(e) for e in errors], repaired)


def _skeleton_value(s: dict | None) -> Any:
    if not isinstance(s, dict):
        return None
    if 'const' in s:
        return s['const']
    enum = s.get('enum')
    if isinstance(enum, list) and enum:
        if all((isinstance(v, str) for v in enum)):
            return '|'.join(enum)
        return enum[0]
    t = s.get('type')
    if isinstance(t, list):
        for tt in t:
            if tt != 'null':
                return _skeleton_value({**s, 'type': tt})
        return None
    if t == 'object':
        props = s.get('properties') or {}
        return {k: _skeleton_value(v) for (k, v) in props.items()}
    if t == 'array':
        return [_skeleton_value(s.get('items'))]
    return _TYPE_PLACEHOLDER.get(t, '<value>')


def _skeleton_text(schema: dict) -> str:
    return json.dumps(_skeleton_value(schema), ensure_ascii=False, indent=2)


def _format_with_skeleton(user: str, schema: dict) -> str:
    return (
        f'{user}\n\n---\n\n## OUTPUT FORMAT\n'
        'Return ONE JSON object that matches EXACTLY this shape. '
        'No markdown fences, no <think> tags, no commentary.\n\n'
        f'```json\n{_skeleton_text(schema)}\n```'
    )


def _repair_user(original_user: str, raw: str, errors: list[str], schema: dict) -> str:
    joined = '\n'.join((f'- {e}' for e in errors[:_ERROR_LIMIT]))
    preview = (raw or '')[:_PREVIEW_CHARS] or '<empty>'
    return (
        f'## ORIGINAL TASK\n{original_user}\n\n'
        '## PREVIOUS BAD OUTPUT (DO NOT REPEAT — this is what failed)\n```\n'
        + preview
        + f'\n```\n\n## VALIDATION ERRORS\n{joined}\n\n'
        f'## REQUIRED JSON SHAPE\n```json\n{_skeleton_text(schema)}\n```\n\n'
        '## YOUR JOB\n'
        'Emit ONE JSON object satisfying the shape above. Do NOT echo the bad output. '
        'Do NOT call any tool. Do NOT wrap in markdown. Output JSON only.'
    )


def _dump_raw(session: AnalysisSession, agent: str, raw: str) -> str | None:
    try:
        path = session.config.storage.runs_dir / session.run_id / 'raw' / f"{agent.replace(':', '_')}.txt"
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(raw if raw else '<empty>', encoding='utf-8')
        return str(path)
    except OSError:
        return None


def _call_llm(session: AnalysisSession, *, producer: Callable[[], str], cache_key: str | None, agent: str) -> str:
    return session.llm.call(producer=producer, cache_key=cache_key, use_cache=cache_key is not None, agent=agent)


def _invoke(invoker: LLMInvoker, user: str, *, system_prompt: str | None = None) -> str:
    if isinstance(invoker, LLMInvoker):
        return invoker.invoke(user, system_prompt=system_prompt, gateway=False)
    if system_prompt is None:
        return invoker.invoke(user)
    try:
        return invoker.invoke(user, system_prompt=system_prompt)
    except TypeError:
        return invoker.invoke(user)


def invoke_structured(
    session: AnalysisSession,
    invoker: LLMInvoker,
    user_text: str,
    *,
    agent: str,
    schema: dict,
    cache_key: str | None = None,
    max_repair: int = 2,
    producer: Callable[[str], str] | None = None,
) -> dict:
    wrapped_user = _format_with_skeleton(user_text, schema)
    parsed: dict = {}
    errors: list[str] = []
    raw_last = ''
    current_user = wrapped_user
    for attempt in range(max_repair + 1):
        agent_tag = agent if attempt == 0 else f'{agent}:repair{attempt}'
        if attempt == 0:
            if producer is not None:
                raw = producer(wrapped_user)
            else:
                raw = _call_llm(
                    session, producer=lambda u=current_user: _invoke(invoker, u), cache_key=cache_key, agent=agent_tag
                )
        else:
            raw = _call_llm(
                session,
                producer=lambda u=current_user: _invoke(invoker, u, system_prompt=_REPAIR_SYSTEM_PROMPT),
                cache_key=None,
                agent=agent_tag,
            )
        raw_last = (raw or '').strip()
        _dump_raw(session, agent_tag, raw_last)
        session.telemetry.emit('llm.answer', actor=agent_tag, agent=agent_tag, answer=raw_last, attempt=attempt)
        if not raw_last:
            errors = ['<root>: empty response (model returned only reasoning/whitespace)']
            session.telemetry.emit('empty_response', agent=agent_tag)
        else:
            parsed, errors, repaired = parse_and_validate(raw_last, schema)
            if not errors:
                session.telemetry.emit(
                    'schema.validated', agent=agent_tag, attempt=attempt, repaired=repaired, output=parsed
                )
                if repaired:
                    session.telemetry.emit('schema_repaired', agent=agent_tag)
                return parsed
            session.telemetry.emit(
                'schema.validation_failed', agent=agent_tag, attempt=attempt, errors=errors[:10], raw=raw_last
            )
        if attempt >= max_repair:
            break
        current_user = _repair_user(wrapped_user, raw_last, errors, schema)
    raw_path = _dump_raw(session, agent, raw_last)
    session.telemetry.emit(
        'schema_repair_failed',
        agent=agent,
        errors=errors[:10],
        raw_preview=raw_last[:500],
        raw_path=raw_path,
        partial_keys=sorted(parsed.keys()) if parsed else [],
    )
    session.schema_failure_count += 1
    if session.schema_failure_count >= _MAX_SCHEMA_FAILURES:
        raise ApplyError(
            'SCHEMA_FOLLOW_FAILED',
            f'model failed to follow JSON schema {session.schema_failure_count} times across agents; '
            'this model likely cannot drive evo. '
            'Switch evo_llm to qwen3-max / deepseek-v3 / claude-sonnet.',
            {'failures': session.schema_failure_count, 'agent': agent, 'last_errors': errors[:5]},
            kind='permanent',
        )
    return parsed


__all__ = ['invoke_structured', 'parse_and_validate']
