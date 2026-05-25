from __future__ import annotations

import json
import re
from collections import OrderedDict
from typing import Any, Dict, List, Literal, Optional

from lazyllm import AutoModel
from chat.tools.skill_manager import _validate_skill_content
from chat.utils.load_config import get_config_path

try:
    from json_repair import repair_json as _repair_json  # type: ignore
except Exception:  # pragma: no cover - optional dependency
    _repair_json = None

MemoryType = Literal['skill', 'memory', 'user_preference']

_MAX_GENERATE_ATTEMPTS = 3
_MAX_MANAGED_CONTENT_CHARS = 1400
_JSON_BLOCK_RE = re.compile(r'```json\s*(.*?)\s*```', re.DOTALL)
_CODE_BLOCK_RE = re.compile(r'```(?:[a-zA-Z0-9_+-]+)?\s*(.*?)\s*```', re.DOTALL)
_THINK_BLOCK_RE = re.compile(r'<think>.*?</think\s*>', re.DOTALL | re.IGNORECASE)
_SINGLE_STRING_FIELD_RE = re.compile(
    r'^\{\s*"(?P<key>[^"\\]+)"\s*:\s*"(?P<value>(?:[^"\\]|\\.)*)"\s*,?\s*\}\s*$',
    re.DOTALL,
)
_DATE_BULLET_RE = re.compile(r'^-\s+(.+?)(?::\s*(.*))?$')
_SECTION_HEADER_TO_KEY = OrderedDict((
    ('用户在做', 'doing'),
    ('我们讨论了', 'discussed'),
    ('状态/冲突', 'status'),
))
_SECTION_KEY_TO_HEADER = {v: k for k, v in _SECTION_HEADER_TO_KEY.items()}
_MEMORY_SECTION_KEYS = tuple(_SECTION_KEY_TO_HEADER.keys())


class BadRequestError(ValueError):
    """Raised when request body fields are missing or malformed."""


class UnprocessableContentError(ValueError):
    """Raised when generated content is repeatedly invalid."""


def _normalize_suggestions(raw_suggestions: Optional[List[Dict[str, Any]]]) -> List[Dict[str, Any]]:
    if raw_suggestions is None:
        return []
    if not isinstance(raw_suggestions, list):
        raise BadRequestError("'suggestions' must be an array when provided.")

    normalized: List[Dict[str, Any]] = []
    for idx, item in enumerate(raw_suggestions):
        if not isinstance(item, dict):
            raise BadRequestError(f"'suggestions[{idx}]' must be an object.")

        title = item.get('title')
        content = item.get('content')
        reason = item.get('reason')
        outdated = item.get('outdated')

        if not isinstance(title, str) or not title.strip():
            raise BadRequestError(
                f"'suggestions[{idx}].title' must be a non-empty string."
            )
        if not isinstance(content, str) or not content.strip():
            raise BadRequestError(
                f"'suggestions[{idx}].content' must be a non-empty string."
            )
        if reason is not None and not isinstance(reason, str):
            raise BadRequestError(f"'suggestions[{idx}].reason' must be a string.")
        if outdated is not None and not isinstance(outdated, bool):
            raise BadRequestError(f"'suggestions[{idx}].outdated' must be a boolean.")

        normalized_item: Dict[str, Any] = {
            'title': title.strip(),
            'content': content.strip(),
        }
        if isinstance(reason, str) and reason.strip():
            normalized_item['reason'] = reason.strip()
        if outdated is not None:
            normalized_item['outdated'] = outdated
        normalized.append(normalized_item)
    return normalized


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


def _extract_skill_content(raw: Any) -> str:
    text = str(raw).strip()
    text = _THINK_BLOCK_RE.sub('', text).strip()

    match = _CODE_BLOCK_RE.search(text)
    if match:
        text = match.group(1).strip()

    frontmatter_start = text.find('---')
    if frontmatter_start > 0:
        text = text[frontmatter_start:].strip()

    if text.endswith('```'):
        text = text[:-3].rstrip()
    return text


def _validate_generated_content(memory_type: MemoryType, content: Any) -> str:
    if not isinstance(content, str):
        raise UnprocessableContentError("Generated field 'content' must be a string.")

    if memory_type == 'skill':
        validation_error = _validate_skill_content(content)
        if validation_error:
            raise UnprocessableContentError(
                f'Generated SKILL.md is invalid: {validation_error}'
            )
    elif memory_type in ('memory', 'user_preference'):
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
    return content


_COMMON_OUTPUT_SPEC = (
    'Output requirements:\n'
    '1. Output only a JSON object; no markdown code blocks, no extra text.\n'
    '2. JSON structure must be {"content": "<new complete text>"}.\n'
    '3. content must be the final complete text after merging all valid input modification requests; do not provide only a patch.\n'  # noqa: E501
)

_MEMORY_EDIT_OUTPUT_SPEC = (
    'Output requirements:\n'
    '1. Output only a JSON object; no markdown code blocks, no extra text.\n'
    '2. JSON structure must be {"operations": [...]}.\n'
    '3. operations is a list of edit commands that will be applied inside the generate endpoint and then rendered back to full memory text.\n'  # noqa: E501
    '4. Prefer {"op":"upsert_day","date":"YYYY-MM-DD","doing":[...],"discussed":[...],"status":[...],"replace":[...]} for structured day-level updates.\n'  # noqa: E501
    '5. You may use {"op":"replace_text","old":"<exact old text>","new":"<new text>"} for one local text fix; replace_text always replaces the first matching occurrence only.\n'  # noqa: E501
    '6. replace is optional and may contain any of ["doing","discussed","status"]; for listed sections, replace the old section summary for that day instead of merging.\n'  # noqa: E501
    '7. Use {"op":"replace_all","content":"<new full memory text>"} only as a last resort when the current memory is too malformed or legacy to edit safely with local text replacement or day-level operations.\n'  # noqa: E501
)

_USER_PREFERENCE_EDIT_OUTPUT_SPEC = (
    'Output requirements:\n'
    '1. Output only a JSON object; no markdown code blocks, no extra text.\n'
    '2. JSON structure must be {"operations": [...]}.\n'
    '3. operations is a list of edit commands that will be applied inside the generate endpoint and then rendered back to full user_preference text.\n'  # noqa: E501
    '4. When the current text is free-form or not clearly section-structured, prefer {"op":"replace_text","old":"<exact old text>","new":"<new text>"} for local edits.\n'  # noqa: E501
    '5. replace_text always replaces the first matching occurrence only. If that is not safe enough, use replace_all instead of trying to target a later match.\n'  # noqa: E501
    '6. You may output multiple replace_text operations and they will be applied in order.\n'
    '7. Use {"op":"replace_all","content":"<new full user_preference text>"} only as a last resort when the current text is too malformed or legacy to edit safely with local text replacement operations.\n'  # noqa: E501
)

_COMMON_LANGUAGE_RULES = (
    '[Language]\n'
    '- Determine the output language from the language used in current content, suggestions, and user_instruct.\n'
    '- If the majority of the input is in Chinese (简体中文), write the generated content in Chinese.\n'
    '- If the majority of the input is in English, write the generated content in English.\n'
    '- Be consistent: do not mix languages within the generated content.\n'
)


def _format_preservation_rules(entity: str) -> str:
    return (
        '[Content preservation rules (CRITICAL)]\n'
        f'- You MUST preserve ALL existing {entity} that are NOT explicitly targeted by suggestions or user_instruct.\n'  # noqa: E501
        f'- When a suggestion only affects one {entity}, keep all others IDENTICAL to the original (same wording, same order).\n'  # noqa: E501
        '- Do NOT rephrase, reformat, or reorganize anything that is not being changed.\n'
        '- If nothing in the current content needs to change for a particular part, copy it VERBATIM into your output.\n'  # noqa: E501
        '- Only remove content that is explicitly marked as outdated by a suggestion, or explicitly contradicted by user_instruct.\n'  # noqa: E501
    )


def _format_prompt_tail(
    content: str,
    suggestions: List[Dict[str, Any]],
    user_instruct: Optional[str],
    output_spec: str = _COMMON_OUTPUT_SPEC,
    previous_error: Optional[str] = None,
) -> str:
    return (
        f'{_format_retry_note(previous_error)}'
        f'{_format_inputs_block(content, suggestions, user_instruct)}'
        f'{output_spec}'
    )


def _format_inputs_block(
    content: str,
    suggestions: List[Dict[str, Any]],
    user_instruct: Optional[str],
) -> str:
    sections = [
        'Input information:\n'
        '1) Current content (full old text):\n'
        f'{content}\n\n'
    ]

    next_index = 2
    if suggestions:
        sections.append(
            f'{next_index}) suggestions (JSON array; each item may contain an outdated field):\n'
            '- outdated=TRUE means the suggestion is expired and for reference only; ignore if irrelevant to the current modification.\n'  # noqa: E501
            '- outdated=FALSE or missing means the suggestion is still valid and content should be updated accordingly.\n'  # noqa: E501
            f'{json.dumps(suggestions, ensure_ascii=False)}\n\n'
        )
        next_index += 1

    if user_instruct:
        sections.append(
            f'{next_index}) user_instruct (direct user instruction):\n{user_instruct}\n\n'
        )

    return ''.join(sections)


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
    return f'\nPrevious output was invalid, error: {previous_error}\nPlease correct and regenerate.\n'


def _compact_len(text: Any) -> int:
    return len(''.join(str(text).split()))


def _managed_content_governance_note(
    content: str,
    suggestions: List[Dict[str, Any]],
    limit: int,
) -> str:
    suggestions_length = sum(
        _compact_len(item.get('title', ''))
        + _compact_len(item.get('content', ''))
        + _compact_len(item.get('reason', ''))
        for item in suggestions
    )
    current_length = _compact_len(content)
    remaining = limit - current_length
    return (
        f'- Current content length after removing whitespace: {current_length} characters.\n'
        f'- Suggestions total length after removing whitespace: {suggestions_length} characters.\n'
        f'- Remaining budget before merging suggestions: {remaining} characters.\n'
        '- Treat existing content as a bounded, continuously maintained store, not an append-only log.\n'  # noqa: E501
        '- Outdated=TRUE is only one stale signal; also remove or rewrite existing content that is proven outdated, wrong, conflicting, redundant, overly specific, or low-value based on the new suggestions, user_instruct, or current context.\n'  # noqa: E501
        '- Even when the limit is not exceeded, proactively compress, consolidate, or delete stale information instead of preserving it by default.\n'  # noqa: E501
        '- Add new information only after resolving stale or conflicting old information; keep the final content concise and useful.\n'  # noqa: E501
    )


def _build_skill_prompt(
    content: str,
    suggestions: List[Dict[str, Any]],
    user_instruct: Optional[str],
    previous_error: Optional[str] = None,
) -> str:
    return (
        'You are a SKILL.md editor. Generate the complete new SKILL.md content based on the input; no explanations or summaries.\n'  # noqa: E501
        'memory type: skill\n'
        'SKILL.md is an abstract SOP (Standard Operating Procedure) that guides the agent to complete tasks '
        'using a unified methodology when the description scope is satisfied.\n'
        '\n'
        '[Format requirements]\n'
        '1. Must start with YAML frontmatter containing at least name and description fields, '
        'followed by a blank line, then the markdown body.\n'
        '2. Keep the existing name value; do not rename unless user_instruct explicitly requests it.\n'
        '3. description should describe the applicable scope and trigger conditions in one sentence; '
        'this is the sole basis for routing/recalling this skill.\n'
        '\n'
        '[Scope and description linkage (important)]\n'
        '- When suggestions or user_instruct involve expanding/narrowing/adjusting the skill scope, trigger scenarios, or coverage, '  # noqa: E501
        'update the frontmatter description accordingly to accurately reflect the new scope.\n'
        '- When changes only affect methodology details in the body without changing the scope, keep description unchanged.\n'  # noqa: E501
        '\n'
        '[Body content rules]\n'
        '- The body must be an abstract SOP: steps, decision criteria, checklists, general rules, output format requirements, etc.\n'  # noqa: E501
        '- Do not include specific cases, project names, specific data, conversation snippets, or one-time examples in the SKILL.md body; '  # noqa: E501
        'if examples are needed, use only highly abstract placeholder illustrations.\n'
        '- If suggestions or user_instruct contain specific cases, abstract the reusable experience into general rules '
        'before writing to the body; do not copy cases verbatim.\n'
        '- Recommended body structure: Applicable conditions / Steps / Judgment & validation / Common pitfalls / Output spec (trim as needed).\n'  # noqa: E501
        '\n'
        f'{_COMMON_LANGUAGE_RULES}'
        '\n'
        f'{_format_preservation_rules("body content")}'
        '\n'
        '[Length control]\n'
        '- Total length of SKILL.md (including frontmatter) must be within 2000 characters; keep it concise.\n'
        f'{_managed_content_governance_note(content, suggestions, 2000)}'
        '\n'
        f'{_format_prompt_tail(content, suggestions, user_instruct, previous_error)}'
    )


def _build_memory_prompt(
    content: str,
    suggestions: List[Dict[str, Any]],
    user_instruct: Optional[str],
    previous_error: Optional[str] = None,
) -> str:
    return (
        'You are an agent memory editor. Generate the complete new memory content based on the input; no explanations or summaries.\n'  # noqa: E501
        'memory type: memory\n'
        "memory stores the agent's own working memory about the user across sessions, such as: when a discussion happened, "  # noqa: E501
        'what the user and agent discussed, what the user was working on, ongoing context the agent may need to recall later, and other concise session-history facts.\n'  # noqa: E501
        'The input suggestions are candidate memory events, not final text patches. Your job is to merge those events into the existing memory and regenerate the full compact memory.\n'  # noqa: E501
        '\n'
        '[Content boundaries]\n'
        '- Only record concise working-memory entries with future recall value; do not write raw chat logs, full transcript summaries, pure emotional expressions, or unrelated small talk.\n'  # noqa: E501
        '- Do not record user profile information (identity, role, long-term preferences, communication style, etc.) here; those belong to user_preference.\n'  # noqa: E501
        '- Each entry should be self-contained and easy to scan: prefer a time anchor when known, then state what was discussed, what the user was doing, or what active context the agent should remember.\n'  # noqa: E501
        '\n'
        '[Writing and merging rules]\n'
        '- You do NOT output final memory text directly unless you must use replace_all. Normally you output edit operations that will be applied to the existing memory inside the generate endpoint.\n'  # noqa: E501
        '- If the current memory has local wording problems, slight format drift, or a user-edited phrase that should be corrected without rewriting the whole day structure, you may use `replace_text` for a local fix.\n'  # noqa: E501
        '- Preferred final format after editing: group by day. Use one top-level bullet per day, ideally `- YYYY-MM-DD`, then summarize that day under concise sub-lines such as `用户在做:`, `我们讨论了:`, and `状态/冲突:` when needed.\n'  # noqa: E501
        '- If the exact date is unknown, use the best available time anchor such as month, week, or relative session marker, but still merge nearby events together when they clearly belong to the same day or session window.\n'  # noqa: E501
        '- Treat each suggestion as one atomic memory event to absorb into the day summary, not as a ready-made final line that must be copied verbatim.\n'  # noqa: E501
        '- For day-level edits, prefer one `upsert_day` operation per affected day. Put concise final section summaries for that day into `doing`, `discussed`, and `status`.\n'  # noqa: E501
        '- Use `replace` to overwrite a section when new information should supersede the old summary for that day; omit `replace` when simple merge is enough.\n'  # noqa: E501
        '- When many events happen in one day, merge them into one daily entry and keep only the main threads, decisions, and follow-up context. Do not create a long bullet list of every small action.\n'  # noqa: E501
        '- When merging, deduplicate and consolidate: combine same or similar working-memory items into a more accurate statement; do not stack duplicates.\n'  # noqa: E501
        '- Conflict handling: if a new suggestion clearly supersedes an older memory on the same topic, keep only the new conclusion and record it under `状态/冲突:` as `已更新:` or `已废弃旧方案:` when useful.\n'  # noqa: E501
        '- If conflicting information is still unresolved, keep only the current best summary and mark it as `待定:` or `当前倾向:` under `状态/冲突:`.\n'  # noqa: E501
        '- Keep language concise and objective; compress aggressively so memory remains a compact aide-memoire rather than a diary.\n'  # noqa: E501
        '- `replace_text` always replaces the first matching occurrence only. If first-match replacement is unsafe or too ambiguous, use `replace_all` instead of trying to target a later occurrence.\n'  # noqa: E501
        '- Use `replace_all` only when the current content cannot be edited safely with local text replacement or day-level operations.\n'  # noqa: E501
        '\n'
        f'{_COMMON_LANGUAGE_RULES}'
        '\n'
        f'{_format_preservation_rules("entries")}'
        '\n'
        '[Length control]\n'
        f'- The final content must be within {_MAX_MANAGED_CONTENT_CHARS} characters after removing all whitespace; if needed, reduce low-value details and keep only the most important concise entries.\n'  # noqa: E501
        f'{_managed_content_governance_note(content, suggestions, _MAX_MANAGED_CONTENT_CHARS)}'
        '\n'
        f'{_format_prompt_tail(content, suggestions, user_instruct, _MEMORY_EDIT_OUTPUT_SPEC, previous_error)}'
    )


def _build_user_preference_prompt(
    content: str,
    suggestions: List[Dict[str, Any]],
    user_instruct: Optional[str],
    previous_error: Optional[str] = None,
) -> str:
    return (
        'You are a user_preference editor. Generate the complete new user_preference content based on the input; no explanations or summaries.\n'  # noqa: E501
        'memory type: user_preference\n'
        'user_preference stores long-term stable user profile information, such as: user identity / role / domain, '
        'long-term preferences (communication tone, output format, language, level of detail), taboos, common workflow preferences, default context assumptions, etc.\n'  # noqa: E501
        '\n'
        '[Content boundaries]\n'
        '- Only record long-term stable profile information that can be reused in every future interaction.\n'
        '- Do not record specific experiences, specific project knowledge, or one-time events here; those belong to memory.\n'  # noqa: E501
        '- Do not write as chat logs or journals; organize as itemized profile entries that the agent can quickly read.\n'  # noqa: E501
        '\n'
        '[Writing and merging rules]\n'
        '- You do NOT output final user_preference text directly unless you must use replace_all. Normally you output edit operations that will be applied to the existing user_preference inside the generate endpoint.\n'  # noqa: E501
        "- If the current text is free-form, paragraph-based, or otherwise not clearly section-structured, prefer `replace_text` with an exact old substring and the desired new substring so the user's own writing structure is preserved.\n"  # noqa: E501
        '- `replace_text` always replaces the first matching occurrence only. If first-match replacement is unsafe or not enough, use `replace_all` instead.\n'  # noqa: E501
        '- Prefer small, local `replace_text` edits over rewriting the whole text. Keep untouched user-authored wording and structure exactly as-is whenever possible.\n'  # noqa: E501
        '- When preferences conflict, the new preference should replace the old text directly, and user_instruct takes precedence.\n'  # noqa: E501
        '- Keep language concise and neutral; no anthropomorphic comments; only state factual user profile entries.\n'
        '- Use `replace_all` only when the current content cannot be edited safely with local text replacement operations.\n'  # noqa: E501
        '\n'
        f'{_COMMON_LANGUAGE_RULES}'
        '\n'
        f'{_format_preservation_rules("profile entries")}'
        '\n'
        '[Length control]\n'
        f'- The final content must be within {_MAX_MANAGED_CONTENT_CHARS} characters after removing all whitespace; if needed, reduce low-value details and keep only the most important concise entries.\n'  # noqa: E501
        f'{_managed_content_governance_note(content, suggestions, _MAX_MANAGED_CONTENT_CHARS)}'
        '\n'
        f'{_format_prompt_tail(content, suggestions, user_instruct, _USER_PREFERENCE_EDIT_OUTPUT_SPEC, previous_error)}'
    )


_PROMPT_BUILDERS = {
    'skill': _build_skill_prompt,
    'memory': _build_memory_prompt,
    'user_preference': _build_user_preference_prompt,
}


def _build_generate_prompt(
    memory_type: MemoryType,
    content: str,
    suggestions: List[Dict[str, Any]],
    user_instruct: Optional[str],
    previous_error: Optional[str] = None,
) -> str:
    try:
        builder = _PROMPT_BUILDERS[memory_type]
    except KeyError as exc:
        raise BadRequestError(f'Unsupported memory type: {memory_type!r}') from exc
    return builder(
        content=content,
        suggestions=suggestions,
        user_instruct=user_instruct,
        previous_error=previous_error,
    )


class MemoryGeneratePipeline:
    def __init__(self) -> None:
        self.llm = AutoModel(model='llm', config=get_config_path())

    def generate(
        self,
        memory_type: MemoryType,
        content: Any,
        suggestions: Optional[List[Dict[str, Any]]],
        user_instruct: Any,
    ) -> str:
        if not isinstance(content, str):
            raise BadRequestError("'content' is required and must be a string.")

        normalized_suggestions = _normalize_suggestions(suggestions)
        normalized_user_instruct = _normalize_user_instruct(user_instruct)
        if not normalized_suggestions and normalized_user_instruct is None:
            raise BadRequestError(
                "At least one of 'suggestions' or 'user_instruct' must be provided."
            )

        error: Optional[str] = None
        for _ in range(_MAX_GENERATE_ATTEMPTS):
            prompt = _build_generate_prompt(
                memory_type=memory_type,
                content=content,
                suggestions=normalized_suggestions,
                user_instruct=normalized_user_instruct,
                previous_error=error,
            )
            raw = self.llm(prompt)
            try:
                parsed = _extract_json_object(raw)
                if memory_type == 'memory':
                    edited_content = _apply_memory_edit_operations(content, parsed)
                    return _validate_generated_content(memory_type, edited_content)
                if memory_type == 'user_preference':
                    edited_content = _apply_user_preference_edit_operations(content, parsed)
                    return _validate_generated_content(memory_type, edited_content)
                return _validate_generated_content(memory_type, parsed.get('content'))
            except UnprocessableContentError as exc:
                if memory_type == 'skill':
                    skill_content = _extract_skill_content(raw)
                    validation_error = _validate_skill_content(skill_content)
                    if validation_error is None:
                        return skill_content
                    error = f'{exc}; raw skill fallback invalid: {validation_error}'
                    continue
                error = str(exc)

        raise UnprocessableContentError(
            f'Failed to generate valid content after {_MAX_GENERATE_ATTEMPTS} attempts: {error}'
        )


memory_generate_pipeline = MemoryGeneratePipeline()


def generate_memory_content(
    memory_type: MemoryType,
    content: Any,
    suggestions: Optional[List[Dict[str, Any]]],
    user_instruct: Any,
) -> str:
    return memory_generate_pipeline.generate(
        memory_type=memory_type,
        content=content,
        suggestions=suggestions,
        user_instruct=user_instruct,
    )


def _normalize_string_list(raw: Any, *, field_name: str) -> List[str]:
    if raw is None:
        return []
    if not isinstance(raw, list):
        raise UnprocessableContentError(f"Operation field '{field_name}' must be an array of strings.")

    normalized: List[str] = []
    for idx, item in enumerate(raw):
        if not isinstance(item, str) or not item.strip():
            raise UnprocessableContentError(
                f"Operation field '{field_name}[{idx}]' must be a non-empty string."
            )
        value = item.strip()
        if value not in normalized:
            normalized.append(value)
    return normalized


def _parse_memory_operations(payload: Dict[str, Any]) -> List[Dict[str, Any]]:
    operations = payload.get('operations')
    if not isinstance(operations, list) or not operations:
        raise UnprocessableContentError("Model output for memory must contain a non-empty 'operations' array.")

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
            if not isinstance(old, str) or not old:
                raise UnprocessableContentError("replace_text requires a non-empty string field 'old'.")
            if not isinstance(new, str):
                raise UnprocessableContentError("replace_text requires a string field 'new'.")
            normalized_ops.append({
                'op': 'replace_text',
                'old': old,
                'new': new,
            })
            continue

        if op_name != 'upsert_day':
            raise UnprocessableContentError(
                f"Unsupported memory operation {op_name!r}; expected 'replace_text', 'upsert_day' or 'replace_all'."
            )

        date = raw_op.get('date')
        if not isinstance(date, str) or not date.strip():
            raise UnprocessableContentError("upsert_day requires a non-empty string field 'date'.")

        replace = _normalize_string_list(raw_op.get('replace'), field_name='replace')
        invalid_keys = [key for key in replace if key not in _MEMORY_SECTION_KEYS]
        if invalid_keys:
            raise UnprocessableContentError(
                f"replace contains unsupported sections: {', '.join(invalid_keys)}."
            )

        normalized_ops.append({
            'op': 'upsert_day',
            'date': date.strip(),
            'doing': _normalize_string_list(raw_op.get('doing'), field_name='doing'),
            'discussed': _normalize_string_list(raw_op.get('discussed'), field_name='discussed'),
            'status': _normalize_string_list(raw_op.get('status'), field_name='status'),
            'replace': replace,
        })

    return normalized_ops


def _append_unique(existing: List[str], values: List[str]) -> List[str]:
    merged = list(existing)
    for value in values:
        if value not in merged:
            merged.append(value)
    return merged


def _new_day_record() -> Dict[str, List[str]]:
    return {key: [] for key in _MEMORY_SECTION_KEYS}


def _parse_existing_memory(content: str) -> 'OrderedDict[str, Dict[str, List[str]]]':
    days: 'OrderedDict[str, Dict[str, List[str]]]' = OrderedDict()
    current_date: Optional[str] = None
    current_section: Optional[str] = None

    for raw_line in content.splitlines():
        line = raw_line.rstrip()
        stripped = line.strip()
        if not stripped:
            continue

        date_match = _DATE_BULLET_RE.match(stripped)
        if line.startswith('- ') and date_match:
            current_date = date_match.group(1).strip()
            current_section = None
            days.setdefault(current_date, _new_day_record())
            inline_text = (date_match.group(2) or '').strip()
            if inline_text:
                days[current_date]['discussed'] = _append_unique(
                    days[current_date]['discussed'],
                    [inline_text],
                )
            continue

        if current_date is None:
            continue

        header = stripped.rstrip(':')
        if header in _SECTION_HEADER_TO_KEY:
            current_section = _SECTION_HEADER_TO_KEY[header]
            continue

        bullet_value = stripped
        if stripped.startswith('- '):
            bullet_value = stripped[2:].strip()
        if current_section and bullet_value:
            days[current_date][current_section] = _append_unique(
                days[current_date][current_section],
                [bullet_value],
            )

    return days


def _render_memory(days: 'OrderedDict[str, Dict[str, List[str]]]') -> str:
    lines: List[str] = []
    for date, sections in days.items():
        has_content = any(sections.get(key) for key in _MEMORY_SECTION_KEYS)
        if not has_content:
            continue
        lines.append(f'- {date}')
        for key in _MEMORY_SECTION_KEYS:
            items = sections.get(key) or []
            if not items:
                continue
            lines.append(f'  {_SECTION_KEY_TO_HEADER[key]}:')
            for item in items:
                lines.append(f'  - {item}')
    return '\n'.join(lines).strip()


def _apply_memory_edit_operations(current_content: str, payload: Dict[str, Any]) -> str:
    operations = _parse_memory_operations(payload)
    if operations[0]['op'] == 'replace_all':
        return operations[0]['content']

    current = current_content
    days: Optional['OrderedDict[str, Dict[str, List[str]]]'] = None
    for op in operations:
        if op['op'] == 'replace_text':
            if days is not None:
                # Flush pending day-level edits before applying free-form text replacement.
                current = _render_memory(days)
            current = _apply_replace_text(current, op['old'], op['new'], entity_name='memory')
            days = None
            continue

        if days is None:
            days = _parse_existing_memory(current)
        date = op['date']
        day = days.setdefault(date, _new_day_record())
        replace = set(op.get('replace') or [])
        for key in _MEMORY_SECTION_KEYS:
            new_values = op.get(key) or []
            if not new_values and key not in replace:
                continue
            if key in replace:
                day[key] = list(new_values)
            else:
                day[key] = _append_unique(day.get(key, []), new_values)

    if days is None:
        return current.strip()

    return _render_memory(days)


def _parse_user_preference_operations(payload: Dict[str, Any]) -> List[Dict[str, Any]]:
    operations = payload.get('operations')
    if not isinstance(operations, list) or not operations:
        raise UnprocessableContentError("Model output for user_preference must contain a non-empty 'operations' array.")

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
            if not isinstance(old, str) or not old:
                raise UnprocessableContentError("replace_text requires a non-empty string field 'old'.")
            if not isinstance(new, str):
                raise UnprocessableContentError("replace_text requires a string field 'new'.")
            normalized_ops.append({
                'op': 'replace_text',
                'old': old,
                'new': new,
            })
            continue
        raise UnprocessableContentError(
            f"Unsupported user_preference operation {op_name!r}; expected 'replace_text' or 'replace_all'."
        )
    return normalized_ops


def _apply_user_preference_edit_operations(current_content: str, payload: Dict[str, Any]) -> str:
    operations = _parse_user_preference_operations(payload)
    if operations[0]['op'] == 'replace_all':
        return operations[0]['content']

    current = current_content
    for op in operations:
        if op['op'] == 'replace_text':
            current = _apply_replace_text(current, op['old'], op['new'], entity_name='user_preference')
    return current.strip()


def _apply_replace_text(current: str, old: str, new: str, *, entity_name: str) -> str:
    if old not in current:
        raise UnprocessableContentError(
            f"replace_text could not find the requested 'old' substring in current {entity_name} content. "
            'Please correct the old text or use replace_all if necessary.'
        )
    return current.replace(old, new, 1)
