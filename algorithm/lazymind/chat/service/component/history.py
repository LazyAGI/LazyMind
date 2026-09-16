from __future__ import annotations

import json
import re
from typing import Any

from lazyllm.tools.agent.base import (
    TOOL_OBSERVATION_KEY,
    attachable_tool_observation,
)

from lazymind.chat.engine.tools.session_env import redact_session_env_arguments
from lazymind.chat.service.component.tool_rendering import (
    _TOOL_CALL_TAG,
    _TOOL_PREVIEW_TAG,
    _TOOL_RESULT_PREVIEW_TAG,
    _TOOL_RESULT_TAG,
)
from lazymind.chat.service.utils.citations import (
    SOURCE_LINK_PATTERN,
    SOURCE_REF_PATTERN,
)

_HISTORY_TAG_PATTERN = re.compile(
    r'<(?P<tag>tp|trp|tool_call|tool_result)(?P<attrs>[^>]*)>(?P<body>.*?)</(?P=tag)>',
    re.DOTALL,
)
_WHITESPACE_BEFORE_PUNCT_PATTERN = re.compile(r'\s+([。！？，、.!?,;:])')
_MULTI_SPACE_PATTERN = re.compile(r'[ \t]{2,}')
_COMPACT_WORKFLOW_TOOL_RESULTS = {
    'advance_step',
    'advance_step_and_hand_off',
    'get_workflow_state',
}
_WORKFLOW_REWIND_QUERY_PATTERNS = (
    re.compile(r'^请重新执行步骤\s+[A-Za-z0-9_.:-]+$'),
    re.compile(r'^Please re-run step\s+[A-Za-z0-9_.:-]+$', re.IGNORECASE),
)


def is_workflow_rewind_action(query: str, workflow_context: Any) -> bool:
    """Recognize the deterministic command emitted by the Workflow rewind UI."""
    context = workflow_context if isinstance(workflow_context, dict) else {}
    if not str(context.get('session_id') or '').strip():
        return False
    normalized = str(query or '').strip()
    return any(pattern.fullmatch(normalized) for pattern in _WORKFLOW_REWIND_QUERY_PATTERNS)


def _history_message_content(message: dict[str, Any]) -> str:
    content = message.get('content')
    return content if isinstance(content, str) else ''


def _strip_history_citations(text: str) -> str:
    if not text:
        return ''
    text = SOURCE_LINK_PATTERN.sub('', text)
    text = SOURCE_REF_PATTERN.sub('', text)
    text = _WHITESPACE_BEFORE_PUNCT_PATTERN.sub(r'\1', text)
    return _MULTI_SPACE_PATTERN.sub(' ', text)


def _sanitize_history_tool_result(result: Any) -> Any:
    if isinstance(result, str):
        return _strip_history_citations(result)
    if isinstance(result, list):
        return [_sanitize_history_tool_result(item) for item in result]
    if isinstance(result, dict):
        sanitized = {}
        for key, value in result.items():
            if key in ('citation_index', 'ref'):
                continue
            sanitized[key] = _sanitize_history_tool_result(value)
        return sanitized
    return result


def _compact_workflow_history_payload(payload: Any) -> Any:
    """Remove authoritative graph bodies from a model-visible Workflow receipt."""
    if not isinstance(payload, dict):
        return payload
    if isinstance(payload.get('value'), dict):
        return {
            **payload,
            'value': _compact_workflow_history_payload(payload['value']),
        }

    projection = payload.get('projection')
    projection = projection if isinstance(projection, dict) else {}
    workflow_state = payload.get('workflow_state')
    workflow_state = workflow_state if isinstance(workflow_state, dict) else {}
    compact = {
        key: value
        for key, value in payload.items()
        if key not in {'projection', 'workflow_state', 'graph', 'compiled_graph'}
    }
    for result_key, projection_key in (
        ('ready_steps', 'ready'),
        ('retryable_steps', 'retryable'),
        ('rewindable_steps', 'rewindable'),
        ('continue_steps', 'continue'),
    ):
        if result_key not in compact and projection_key in projection:
            compact[result_key] = projection.get(projection_key) or []
    if (
        projection.get('completed') is True
        or workflow_state.get('status') == 'completed'
    ):
        compact['status'] = 'completed'
        compact.setdefault('outcome', 'workflow_completed')
    elif not compact.get('status') and workflow_state.get('status'):
        compact['status'] = workflow_state['status']
    return compact


def _sanitize_named_history_tool_result(
    tool_name: str,
    result: Any,
    *,
    compact_workflow_receipts: bool,
) -> Any:
    sanitized = _sanitize_history_tool_result(result)
    if not compact_workflow_receipts or tool_name not in _COMPACT_WORKFLOW_TOOL_RESULTS:
        return sanitized
    if isinstance(sanitized, str):
        try:
            decoded = json.loads(sanitized)
        except json.JSONDecodeError:
            return sanitized
        return json.dumps(
            _compact_workflow_history_payload(decoded),
            ensure_ascii=False,
            separators=(',', ':'),
        )
    return _compact_workflow_history_payload(sanitized)


def _parse_history_assistant_content(
    content: str,
) -> list[dict[str, Any]]:
    segments: list[dict[str, Any]] = []
    cursor = 0
    content = content or ''

    while cursor < len(content):
        think_start = content.find('<think>', cursor)
        tag_match = _HISTORY_TAG_PATTERN.search(content, cursor)
        tag_start = tag_match.start() if tag_match else -1

        next_start = len(content)
        next_kind = ''
        if think_start >= 0 and think_start < next_start:
            next_start = think_start
            next_kind = 'think'
        if tag_start >= 0 and tag_start < next_start:
            next_start = tag_start
            next_kind = 'tag'

        if not next_kind:
            remaining = content[cursor:]
            if remaining:
                segments.append({'type': 'text', 'content': remaining})
            break

        if next_start > cursor:
            segments.append({'type': 'text', 'content': content[cursor:next_start]})

        if next_kind == 'think':
            think_body_start = next_start + len('<think>')
            think_end = content.find('</think>', think_body_start)
            if think_end >= 0:
                think_content = content[think_body_start:think_end]
                cursor = think_end + len('</think>')
            else:
                think_content = content[think_body_start:]
                cursor = len(content)
            segments.append({'type': 'reasoning', 'content': think_content})
            continue

        assert tag_match is not None
        cursor = tag_match.end()
        tag = tag_match.group('tag')
        body = tag_match.group('body') or ''
        if tag in (_TOOL_PREVIEW_TAG, _TOOL_RESULT_PREVIEW_TAG):
            continue
        try:
            payload = json.loads(body)
        except json.JSONDecodeError:
            continue
        if not isinstance(payload, dict):
            continue
        if tag == _TOOL_CALL_TAG:
            tool_call_id = str(payload.get('id') or '')
            tool_name = str(payload.get('name') or '')
            if not tool_call_id or not tool_name:
                continue
            arguments = payload.get('arguments', {})
            if not isinstance(arguments, dict):
                arguments = {}
            arguments = redact_session_env_arguments(tool_name, arguments)
            segments.append({
                'type': 'tool_call',
                'id': tool_call_id,
                'name': tool_name,
                'arguments': arguments,
            })
        elif tag == _TOOL_RESULT_TAG:
            segments.append({
                'type': 'tool_result',
                'id': str(payload.get('id') or ''),
                'name': str(payload.get('name') or ''),
                'result': payload.get('result'),
            })
    return segments


def _append_pending_assistant(
    normalized: list[dict[str, Any]],
    pending_reasoning_parts: list[str],
    pending_text_parts: list[str],
    pending_tool_calls: list[dict[str, Any]],
    saw_structured_segments: bool,
    history_seq: Any = None,
) -> None:
    reasoning = '\n'.join(
        part.strip() for part in pending_reasoning_parts if str(part).strip()
    ).strip()
    text = ''.join(pending_text_parts).strip()
    if not reasoning and not text and not pending_tool_calls:
        return
    msg: dict[str, Any] = {'role': 'assistant', 'content': text}
    if saw_structured_segments:
        msg['reasoning_content'] = reasoning
    if pending_tool_calls:
        msg['tool_calls'] = list(pending_tool_calls)
    if history_seq is not None:
        msg['history_seq'] = history_seq
    normalized.append(msg)
    pending_reasoning_parts.clear()
    pending_text_parts.clear()
    pending_tool_calls.clear()


def _drop_incomplete_tool_exchanges(
    messages: list[dict[str, Any]],
) -> list[dict[str, Any]]:
    """Keep only provider-valid assistant tool-call/result groups."""
    cleaned: list[dict[str, Any]] = []
    index = 0
    while index < len(messages):
        message = messages[index]
        if message.get('role') == 'tool':
            index += 1
            continue

        tool_calls = message.get('tool_calls') if message.get('role') == 'assistant' else None
        if not isinstance(tool_calls, list) or not tool_calls:
            cleaned.append(message)
            index += 1
            continue

        call_ids = [
            str(tool_call.get('id') or '')
            for tool_call in tool_calls
            if isinstance(tool_call, dict)
        ]
        expected_ids = set(call_ids)
        cursor = index + 1
        results_by_id: dict[str, dict[str, Any]] = {}
        while cursor < len(messages) and messages[cursor].get('role') == 'tool':
            result = messages[cursor]
            result_id = str(result.get('tool_call_id') or '')
            if result_id in expected_ids and result_id not in results_by_id:
                results_by_id[result_id] = result
            cursor += 1

        complete = (
            bool(expected_ids)
            and len(call_ids) == len(expected_ids)
            and set(results_by_id) == expected_ids
        )
        if complete:
            cleaned.append(message)
            cleaned.extend(results_by_id[call_id] for call_id in call_ids)
        else:
            assistant_without_calls = {
                key: value for key, value in message.items() if key != 'tool_calls'
            }
            if (
                str(assistant_without_calls.get('content') or '').strip()
                or str(assistant_without_calls.get('reasoning_content') or '').strip()
            ):
                cleaned.append(assistant_without_calls)
        index = cursor
    return cleaned


def normalize_history_for_agent(
    history: list[dict[str, Any]],
    *,
    compact_workflow_receipts: bool = False,
) -> list[dict[str, Any]]:
    normalized: list[dict[str, Any]] = []
    for message in history or []:
        if not isinstance(message, dict):
            continue
        role = str(message.get('role') or '').strip()
        if role == 'assistant':
            content = _history_message_content(message)
            history_seq = message.get('history_seq')
            segments = _parse_history_assistant_content(content)

            pending_reasoning_parts: list[str] = []
            pending_text_parts: list[str] = []
            pending_tool_calls: list[dict[str, Any]] = []
            saw_structured_segments = False
            ephemeral_tool_ids: set[str] = set()

            for seg in segments:
                seg_type = seg['type']
                if seg_type == 'reasoning':
                    saw_structured_segments = True
                    pending_reasoning_parts.append(seg['content'])
                elif seg_type == 'text':
                    pending_text_parts.append(_strip_history_citations(seg['content']))
                elif seg_type == 'tool_call':
                    saw_structured_segments = True
                    if seg['name'] == 'intentwrite':
                        ephemeral_tool_ids.add(seg['id'])
                        continue
                    pending_tool_calls.append({
                        'id': seg['id'],
                        'type': 'function',
                        'function': {
                            'name': seg['name'],
                            'arguments': json.dumps(
                                seg['arguments'],
                                ensure_ascii=False,
                            ),
                        },
                    })
                elif seg_type == 'tool_result':
                    saw_structured_segments = True
                    if seg['id'] in ephemeral_tool_ids or seg['name'] == 'intentwrite':
                        continue
                    _append_pending_assistant(
                        normalized,
                        pending_reasoning_parts,
                        pending_text_parts,
                        pending_tool_calls,
                        saw_structured_segments,
                        history_seq=history_seq,
                    )
                    sanitized_result = _sanitize_named_history_tool_result(
                        seg['name'],
                        seg['result'],
                        compact_workflow_receipts=compact_workflow_receipts,
                    )
                    tool_msg = {
                        'role': 'tool',
                        'tool_call_id': seg['id'],
                        'name': seg['name'],
                        'content': (
                            sanitized_result
                            if isinstance(sanitized_result, str)
                            else json.dumps(
                                sanitized_result,
                                ensure_ascii=False,
                                separators=(',', ':'),
                            )
                        ),
                    }
                    observation = attachable_tool_observation(sanitized_result)
                    if observation is not None:
                        tool_msg[TOOL_OBSERVATION_KEY] = observation
                    if history_seq is not None:
                        tool_msg['history_seq'] = history_seq
                    normalized.append(tool_msg)

            _append_pending_assistant(
                normalized,
                pending_reasoning_parts,
                pending_text_parts,
                pending_tool_calls,
                saw_structured_segments,
                history_seq=history_seq,
            )
            continue

        if role == 'user':
            content = _history_message_content(message)
            if content:
                user_msg: dict[str, Any] = {'role': 'user', 'content': content}
                if message.get('history_seq') is not None:
                    user_msg['history_seq'] = message.get('history_seq')
                normalized.append(user_msg)
            continue

        content = _history_message_content(message)
        if content:
            other: dict[str, Any] = {'role': role or 'assistant', 'content': content}
            if message.get('history_seq') is not None:
                other['history_seq'] = message.get('history_seq')
            normalized.append(other)
    return _drop_incomplete_tool_exchanges(normalized)
