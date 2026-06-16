from __future__ import annotations

import json
import re
from typing import Any, Dict, List, Literal, Optional

from lazyllm import AutoModel
from lazymind.chat.engine.tools.infra import (
    validate_skill_content,
    validate_user_preference_content,
)

try:
    from json_repair import repair_json as _repair_json  # type: ignore
except Exception:  # pragma: no cover - optional dependency
    _repair_json = None

RewriteTaskType = Literal['skill', 'memory', 'user_preference', 'polish']

_MAX_REWRITE_ATTEMPTS = 3
_MAX_MANAGED_CONTENT_CHARS = 1500
_JSON_BLOCK_RE = re.compile(r'```json\s*(.*?)\s*```', re.DOTALL)
_THINK_BLOCK_RE = re.compile(r'<think>.*?</think\s*>', re.DOTALL | re.IGNORECASE)
_SINGLE_STRING_FIELD_RE = re.compile(
    r'^\{\s*"(?P<key>[^"\\]+)"\s*:\s*"(?P<value>(?:[^"\\]|\\.)*)"\s*,?\s*\}\s*$',
    re.DOTALL,
)


class BadRequestError(ValueError):
    """Raised when request body fields are missing or malformed."""


class UnprocessableContentError(ValueError):
    """Raised when generated content is repeatedly invalid."""


# ---------------------------------------------------------------------------
# JSON extraction
# ---------------------------------------------------------------------------

def _extract_json_object(raw: Any) -> Dict[str, Any]:
    text = str(raw).strip()
    text = _THINK_BLOCK_RE.sub('', text).strip()

    match = _JSON_BLOCK_RE.search(text)
    if match:
        text = match.group(1).strip()

    candidates: List[str] = [text]
    left = text.find('{')
    right = text.rfind('}')
    if left >= 0 and right > left:
        trimmed = text[left: right + 1]
        if trimmed != text:
            candidates.append(trimmed)

    parsed: Any = None
    last_error: Optional[json.JSONDecodeError] = None
    for candidate in candidates:
        try:
            parsed = json.loads(candidate)
            break
        except json.JSONDecodeError as exc:
            last_error = exc
    else:
        try:
            if _repair_json is None:
                raise ImportError('json_repair is not installed')
            for candidate in candidates:
                repaired = _repair_json(candidate, return_objects=True)
                if isinstance(repaired, dict):
                    parsed = repaired
                    break
        except Exception:
            pass

    if parsed is None:
        for candidate in candidates:
            parsed = _extract_single_string_field_object(candidate)
            if isinstance(parsed, dict):
                break

    if parsed is None:
        if last_error is not None:
            raise UnprocessableContentError(
                f'Model output is not valid JSON: {last_error}'
            ) from last_error
        raise UnprocessableContentError('Model output is not valid JSON.')

    if not isinstance(parsed, dict):
        raise UnprocessableContentError('Model output must be a JSON object.')
    return parsed


def _extract_single_string_field_object(text: str) -> Optional[Dict[str, str]]:
    match = _SINGLE_STRING_FIELD_RE.match(text.strip())
    if not match:
        return None

    key = match.group('key').strip()
    raw_value = match.group('value').strip()
    if raw_value.endswith(','):
        raw_value = raw_value[:-1].rstrip()
    if len(raw_value) < 2 or not raw_value.startswith('"') or not raw_value.endswith('"'):
        return None

    inner = raw_value[1:-1]
    try:
        value = json.loads(f'"{inner}"')
    except json.JSONDecodeError:
        value = (
            inner.replace('\\"', '"')
            .replace('\\\\', '\\')
            .replace('\\r', '\r')
            .replace('\\n', '\n')
            .replace('\\t', '\t')
        )
    return {key: value}


# ---------------------------------------------------------------------------
# Validation
# ---------------------------------------------------------------------------

def _validate_generated_content(task_type: RewriteTaskType, content: Any) -> str:
    if not isinstance(content, str):
        raise UnprocessableContentError("Generated field 'content' must be a string.")

    if task_type == 'skill':
        validation_error = validate_skill_content(content)
        if validation_error:
            raise UnprocessableContentError(
                f'Generated SKILL.md is invalid: {validation_error}'
            )
    elif task_type in ('memory', 'user_preference'):
        compact_content = ''.join(content.split())
        content_length = len(compact_content)
        if content_length > _MAX_MANAGED_CONTENT_CHARS:
            raise UnprocessableContentError(
                f'Generated content exceeds {_MAX_MANAGED_CONTENT_CHARS} characters '
                f'after removing whitespace; current length is {content_length}. '
                f'Reduce the content length to {_MAX_MANAGED_CONTENT_CHARS} characters '
                'or less after removing whitespace, keeping only the most important '
                'concise entries.'
            )
        if task_type == 'user_preference':
            validation_error = validate_user_preference_content(content)
            if validation_error:
                raise UnprocessableContentError(
                    f'Generated user_preference is invalid: {validation_error}'
                )
    elif task_type == 'polish' and not content.strip():
        raise UnprocessableContentError("Generated field 'content' must be a non-empty string.")
    return content


# ---------------------------------------------------------------------------
# Shared prompt building blocks
# ---------------------------------------------------------------------------

_COMMON_OUTPUT_SPEC = (
    'Output requirements:\n'
    '1. Output only a JSON object; no markdown code blocks, no extra text.\n'
    '2. JSON structure must be {"content": "<new complete text>"}.\n'
    '3. content must be the final complete text after merging all valid input modification requests; do not provide only a patch.\n'  # noqa: E501
)

_COMMON_LANGUAGE_RULES = (
    '[Language]\n'
    '- Determine the output language from the language used in current content and user_instruct.\n'
    '- If the majority of the input is in Chinese (简体中文), write the generated content in Chinese.\n'
    '- If the majority of the input is in English, write the generated content in English.\n'
    '- Be consistent: do not mix languages within the generated content.\n'
)


def _format_preservation_rules(entity: str) -> str:
    return (
        '[Content preservation rules (CRITICAL)]\n'
        f'- You MUST preserve ALL existing {entity} that are NOT explicitly targeted by user_instruct.\n'  # noqa: E501
        f'- When user_instruct only affects one {entity}, keep all others IDENTICAL to the original (same wording, same order).\n'  # noqa: E501
        '- Do NOT rephrase, reformat, or reorganize anything that is not being changed.\n'
        '- If nothing in the current content needs to change for a particular part, copy it VERBATIM into your output.\n'  # noqa: E501
        '- Only remove content that is explicitly marked as outdated or explicitly contradicted by user_instruct.\n'  # noqa: E501
    )


def _format_prompt_tail(
    content: str,
    user_instruct: str,
    output_spec: str = _COMMON_OUTPUT_SPEC,
    previous_error: Optional[str] = None,
) -> str:
    return (
        f'{_format_retry_note(previous_error)}'
        f'{_format_inputs_block(content, user_instruct)}'
        f'{output_spec}'
    )


def _format_inputs_block(
    content: str,
    user_instruct: str,
) -> str:
    return (
        'Input information:\n'
        '1) Current content (full old text):\n'
        f'{content}\n\n'
        f'2) user_instruct (direct user instruction):\n{user_instruct}\n\n'
    )


def _normalize_user_instruct(raw_user_instruct: Any) -> Optional[str]:
    if raw_user_instruct is None:
        return None
    if not isinstance(raw_user_instruct, str):
        raise BadRequestError("'user_instruct' must be a string when provided.")
    normalized = raw_user_instruct.strip()
    return normalized or None


def _format_retry_note(previous_error: Optional[str]) -> str:
    if not previous_error:
        return ''
    if 'replace_text could not find' in previous_error or "field 'old'" in previous_error:
        return (
            f'\nPrevious output was invalid, error: {previous_error}\n'
            'Correction requirement: do not retry with any replace_text operation unless each old value is copied '
            'verbatim from current content and can be found by exact plain string search. If you cannot guarantee '
            'a safe local edit, output full {"content": "..."} instead of operations.\n'
        )
    return f'\nPrevious output was invalid, error: {previous_error}\nPlease correct and regenerate.\n'


def _compact_len(text: Any) -> int:
    return len(''.join(str(text).split()))


def _managed_content_governance_note(content: str, limit: int) -> str:
    current_length = _compact_len(content)
    remaining = limit - current_length
    return (
        f'- Current content length after removing whitespace: {current_length} characters.\n'
        f'- Remaining budget before applying user_instruct: {remaining} characters.\n'
        '- Treat existing content as a bounded, continuously maintained store, not an append-only log.\n'  # noqa: E501
        '- Outdated=TRUE is only one stale signal when it appears inside user_instruct; also remove or rewrite existing content that is proven outdated, wrong, conflicting, redundant, overly specific, or low-value based on user_instruct or current context.\n'  # noqa: E501
        '- Even when the limit is not exceeded, proactively compress, consolidate, or delete stale information instead of preserving it by default.\n'  # noqa: E501
        '- Add new information only after resolving stale or conflicting old information; keep the final content concise and useful.\n'  # noqa: E501
    )


# ---------------------------------------------------------------------------
# Edit operations (shared)
# ---------------------------------------------------------------------------

def _parse_edit_operations(
    payload: Dict[str, Any],
    *,
    entity_name: str,
    allow_empty_old: bool = False,
) -> List[Dict[str, Any]]:
    if 'content' in payload and 'operations' not in payload:
        content = payload.get('content')
        if not isinstance(content, str):
            raise UnprocessableContentError("Generated field 'content' must be a string.")
        return [{'op': 'replace_all', 'content': content.strip()}]

    operations = payload.get('operations')
    if not isinstance(operations, list) or not operations:
        raise UnprocessableContentError(
            f"Model output for {entity_name} must contain a non-empty 'operations' array."
        )

    normalized_ops: List[Dict[str, Any]] = []
    for idx, raw_op in enumerate(operations):
        if not isinstance(raw_op, dict):
            raise UnprocessableContentError(f"'operations[{idx}]' must be an object.")
        op_name = str(raw_op.get('op') or '').strip()
        if op_name == 'replace_all':
            content = raw_op.get('content')
            if not isinstance(content, str):
                raise UnprocessableContentError("replace_all requires a string field 'content'.")
            if len(operations) != 1:
                raise UnprocessableContentError('replace_all must be the only operation when used.')
            return [{'op': 'replace_all', 'content': content.strip()}]
        if op_name == 'replace_text':
            old = raw_op.get('old')
            new = raw_op.get('new')
            if not isinstance(old, str):
                raise UnprocessableContentError("replace_text requires a string field 'old'.")
            if not isinstance(new, str):
                raise UnprocessableContentError("replace_text requires a string field 'new'.")
            if old == '' and new != '' and not allow_empty_old:
                raise UnprocessableContentError(
                    "replace_text with an empty 'old' is only allowed when 'new' is also empty."
                )
            normalized_ops.append({'op': 'replace_text', 'old': old, 'new': new})
            continue
        raise UnprocessableContentError(
            f"Unsupported {entity_name} operation {op_name!r}; expected 'replace_text' or 'replace_all'."
        )
    return normalized_ops


def _apply_replace_text(current: str, old: str, new: str, *, entity_name: str) -> str:
    if old not in current:
        raise UnprocessableContentError(
            f"replace_text could not find the requested 'old' substring in current {entity_name} content. "
            'Please correct the old text or use replace_all if necessary.'
        )
    return current.replace(old, new, 1)


def _apply_replace_text_operation(current: str, old: str, new: str, *, entity_name: str) -> str:
    """Shared by skill and user_preference: handles line deletion when new is empty."""
    replacement = '' if not new.strip() else new
    if not replacement:
        lines = current.splitlines()
        for idx, line in enumerate(lines):
            if line == old:
                return '\n'.join(lines[:idx] + lines[idx + 1:])
    return _apply_replace_text(current, old, replacement, entity_name=entity_name)


def _normalize_numbered_lists(content: str) -> str:
    lines = content.splitlines()
    normalized: List[str] = []
    expected: Optional[int] = None
    last_indent: Optional[str] = None
    item_re = re.compile(r'^(\s*)(\d+)\.\s+(.*)$')

    for line in lines:
        match = item_re.match(line)
        if not match:
            normalized.append(line)
            if line.strip():
                expected = None
                last_indent = None
            continue

        indent, number, body = match.groups()
        if expected is None or indent != last_indent:
            expected = int(number)
            last_indent = indent
        normalized.append(f'{indent}{expected}. {body}')
        expected += 1

    return '\n'.join(normalized)


# ---------------------------------------------------------------------------
# Dispatch tables (populated by business modules on import)
# ---------------------------------------------------------------------------

_PROMPT_BUILDERS: Dict[str, Any] = {}
_EDIT_DISPATCH: Dict[str, Any] = {}


# ---------------------------------------------------------------------------
# Rewrite
# ---------------------------------------------------------------------------

def rewrite_content(
    task_type: RewriteTaskType,
    content: Any,
    user_instruct: Any,
) -> str:
    if not isinstance(content, str):
        raise BadRequestError("'content' is required and must be a string.")

    normalized_user_instruct = _normalize_user_instruct(user_instruct)
    if normalized_user_instruct is None:
        raise BadRequestError("'user_instruct' must be a non-empty string.")

    if task_type == 'memory':
        from .memory import _compact_memory_to_recent_week
        content = _compact_memory_to_recent_week(content)

    error: Optional[str] = None
    for _ in range(_MAX_REWRITE_ATTEMPTS):
        prompt = _PROMPT_BUILDERS[task_type](
            content=content,
            user_instruct=normalized_user_instruct,
            previous_error=error,
        )
        raw = AutoModel(model='llm')(prompt)
        try:
            parsed = _extract_json_object(raw)
            editor = _EDIT_DISPATCH.get(task_type)
            if editor is not None:
                edited_content = editor(content, parsed)
            else:
                edited_content = parsed.get('content')
            return _validate_generated_content(task_type, edited_content)
        except UnprocessableContentError as exc:
            error = str(exc)

    raise UnprocessableContentError(
        f'Failed to generate valid content after {_MAX_REWRITE_ATTEMPTS} attempts: {error}'
    )
