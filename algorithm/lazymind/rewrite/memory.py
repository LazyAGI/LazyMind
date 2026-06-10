"""Memory content generation — prompt, edit operations, date-block parsing, and registration."""

from __future__ import annotations

import re
from collections import OrderedDict
from datetime import datetime, timedelta
from typing import Any, Dict, List, Optional

from .base import (
    UnprocessableContentError,
    _COMMON_LANGUAGE_RULES,
    _EDIT_DISPATCH,
    _PROMPT_BUILDERS,
    _apply_replace_text,
    _compact_len,
    _format_preservation_rules,
    _format_prompt_tail,
    _managed_content_governance_note,
    _parse_edit_operations,
)

_MAX_MANAGED_CONTENT_CHARS = 1500
_MAX_OLDER_MEMORY_SUMMARY_CHARS = 500

_DATE_BULLET_RE = re.compile(r'^-\s+(.+?)(?::\s*(.*))?$')
_ISO_DATE_RE = re.compile(r'^\d{4}-\d{2}-\d{2}$')
_SECTION_HEADER_TO_KEY = OrderedDict((
    ('用户在做', 'doing'),
    ('我们讨论了', 'discussed'),
    ('状态/冲突', 'status'),
))
_SECTION_KEY_TO_HEADER = {v: k for k, v in _SECTION_HEADER_TO_KEY.items()}
_MEMORY_SECTION_KEYS = tuple(_SECTION_KEY_TO_HEADER.keys())

_MEMORY_EDIT_OUTPUT_SPEC = (
    'Output requirements:\n'
    '1. Output only a JSON object; no markdown code blocks, no extra text.\n'
    '2. JSON structure must be {"operations": [...]}.\n'
    '3. operations is a list of edit commands that will be applied inside the generate endpoint and then rendered back to full memory text.\n'  # noqa: E501
    '4. Supported operations are only replace_text and replace_all; do not output any custom operation.\n'  # noqa: E501
    '5. Prefer {"op":"replace_text","old":"<exact old text>","new":"<new text>"} for exact local edits; replace_text always replaces the first matching occurrence only.\n'  # noqa: E501
    '6. To add a new day, output one replace_text with old="" and new as a complete day block beginning with "- YYYY-MM-DD".\n'  # noqa: E501
    '7. A complete day block should include the date plus relevant fields such as 用户在做, 我们讨论了, 状态/冲突, and replacement summaries for sections that should supersede old text.\n'  # noqa: E501
    '8. To modify an existing day, prefer replacing one exact section line or one exact full day block with a corrected full day block.\n'  # noqa: E501
    '9. Use {"op":"replace_all","content":"<new full memory text>"} only as a last resort when the current memory cannot be edited safely with replace_text.\n'  # noqa: E501
)


# ---------------------------------------------------------------------------
# Prompt
# ---------------------------------------------------------------------------

def _build_memory_prompt(
    content: str,
    user_instruct: str,
    previous_error: Optional[str] = None,
) -> str:
    return (
        'You are an agent memory editor. Generate the complete new memory content based on the input; no explanations or summaries.\n'  # noqa: E501
        'memory type: memory\n'
        "memory stores the agent's own working memory about the user across sessions, such as: when a discussion happened, "  # noqa: E501
        'what the user and agent discussed, what the user was working on, ongoing context the agent may need to recall later, and other concise session-history facts.\n'  # noqa: E501
        'The user_instruct may contain direct user edits or approved review suggestions. Treat them as candidate memory events, not final text patches.\n'  # noqa: E501
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
        '- Treat each approved suggestion inside user_instruct as one atomic memory event to absorb into the day summary, not as a ready-made final line that must be copied verbatim.\n'  # noqa: E501
        '- For day-level additions, use replace_text with old="" and new as one complete day block with date, doing, discussed, status, and any section replacement summary.\n'  # noqa: E501
        '- For day-level updates, either replace one exact section line or replace the exact full day block with a corrected full day block. Use replacement summaries when new information supersedes the old summary for that day.\n'  # noqa: E501
        '- When many events happen in one day, merge them into one daily entry and keep only the main threads, decisions, and follow-up context. Do not create a long bullet list of every small action.\n'  # noqa: E501
        '- When merging, deduplicate and consolidate: combine same or similar working-memory items into a more accurate statement; do not stack duplicates.\n'  # noqa: E501
        '- Conflict handling: if a new suggestion clearly supersedes an older memory on the same topic, keep only the new conclusion and record it under `状态/冲突:` as `已更新:` or `已废弃旧方案:` when useful.\n'  # noqa: E501
        '- If conflicting information is still unresolved, keep only the current best summary and mark it as `待定:` or `当前倾向:` under `状态/冲突:`.\n'  # noqa: E501
        '- Keep language concise and objective; compress aggressively so memory remains a compact aide-memoire rather than a diary.\n'  # noqa: E501
        '- `replace_text` always replaces the first matching occurrence only. If first-match replacement is unsafe or too ambiguous, use `replace_all` instead of trying to target a later occurrence.\n'  # noqa: E501
        '- Use `replace_all` only when the current content cannot be edited safely with replace_text operations.\n'  # noqa: E501
        '\n'
        f'{_COMMON_LANGUAGE_RULES}'
        '\n'
        f'{_format_preservation_rules("entries")}'
        '\n'
        '[Length control]\n'
        f'- The final content must be within {_MAX_MANAGED_CONTENT_CHARS} characters after removing all whitespace; if needed, reduce low-value details and keep only the most important concise entries.\n'  # noqa: E501
        f'{_managed_content_governance_note(content, _MAX_MANAGED_CONTENT_CHARS)}'
        '- If memory exceeds the limit, older entries before the most recent week will be summarized into a concise "一周前摘要" section after operations are applied.\n'  # noqa: E501
        '\n'
        f'{_format_prompt_tail(content, user_instruct, _MEMORY_EDIT_OUTPUT_SPEC, previous_error)}'
    )


# ---------------------------------------------------------------------------
# Memory date-block helpers
# ---------------------------------------------------------------------------

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


def _parse_iso_date(value: str) -> Optional[datetime]:
    if not _ISO_DATE_RE.match(value.strip()):
        return None
    try:
        return datetime.strptime(value.strip(), '%Y-%m-%d')
    except ValueError:
        return None


def _trim_text_to_chars(text: str, limit: int) -> str:
    text = ' '.join(text.split())
    if len(text) <= limit:
        return text
    return text[:max(0, limit - 1)].rstrip() + '…'


def _memory_day_summary(date: str, sections: Dict[str, List[str]]) -> str:
    parts: List[str] = []
    for key in _MEMORY_SECTION_KEYS:
        values = sections.get(key) or []
        if values:
            parts.append(f'{_SECTION_KEY_TO_HEADER[key]}：{"；".join(values)}')
    if not parts:
        return ''
    return f'{date}：{"；".join(parts)}'


def _compact_memory_to_recent_week(content: str) -> str:
    if _compact_len(content) <= _MAX_MANAGED_CONTENT_CHARS:
        return content.strip()

    days = _parse_existing_memory(content)
    dated_days = [
        (day, parsed)
        for day in days
        for parsed in [_parse_iso_date(day)]
        if parsed is not None
    ]
    if not dated_days:
        return content.strip()

    latest_day = max(parsed for _, parsed in dated_days)
    cutoff = latest_day - timedelta(days=6)
    older: 'OrderedDict[str, Dict[str, List[str]]]' = OrderedDict()
    recent: 'OrderedDict[str, Dict[str, List[str]]]' = OrderedDict()

    for day, sections in days.items():
        parsed = _parse_iso_date(day)
        if parsed is not None and parsed >= cutoff:
            recent[day] = sections
        else:
            older[day] = sections

    result_days: 'OrderedDict[str, Dict[str, List[str]]]' = OrderedDict()
    if older:
        older_summary = '；'.join(
            summary
            for day, sections in older.items()
            for summary in [_memory_day_summary(day, sections)]
            if summary
        )
        recent_text = _render_memory(recent)
        summary_budget = min(
            _MAX_OLDER_MEMORY_SUMMARY_CHARS,
            max(0, _MAX_MANAGED_CONTENT_CHARS - _compact_len(recent_text) - 20),
        )
        if older_summary and summary_budget > 0:
            result_days['一周前摘要'] = _new_day_record()
            result_days['一周前摘要']['discussed'] = [
                _trim_text_to_chars(older_summary, summary_budget)
            ]

    result_days.update(recent)
    result = _render_memory(result_days)
    return result or content.strip()


# ---------------------------------------------------------------------------
# Memory edit operations
# ---------------------------------------------------------------------------

def _extract_memory_day_date(block: str) -> Optional[str]:
    for line in block.strip().splitlines():
        stripped = line.strip()
        if not stripped:
            continue
        match = re.match(r'^-\s+(\d{4}-\d{2}-\d{2})(?:\s.*)?$', stripped)
        if match:
            return match.group(1)
        return None
    return None


def _find_memory_day_block(content: str, date: str) -> Optional[tuple[int, int]]:
    lines = content.splitlines(keepends=True)
    position = 0
    start: Optional[int] = None

    for line in lines:
        stripped = line.strip()
        match = re.match(r'^-\s+(\d{4}-\d{2}-\d{2})(?:\s.*)?$', stripped)
        if match:
            if start is not None:
                return (start, position)
            if match.group(1) == date:
                start = position
        position += len(line)

    if start is not None:
        return (start, len(content))
    return None


def _insert_or_replace_memory_day_block(content: str, day_block: str) -> str:
    day_block = day_block.strip()
    date = _extract_memory_day_date(day_block)
    if date is None:
        raise UnprocessableContentError(
            'replace_text with empty old requires new to be a complete memory day block beginning with "- YYYY-MM-DD".'  # noqa: E501
        )

    found = _find_memory_day_block(content, date)
    if found is None:
        base = content.strip()
        if not base:
            return day_block
        return f'{base}\n{day_block}'

    start, end = found
    prefix = content[:start].rstrip()
    suffix = content[end:].lstrip('\n')
    parts = [part for part in (prefix, day_block, suffix.strip()) if part]
    return '\n'.join(parts)


def _apply_memory_replace_text_operation(current: str, old: str, new: str) -> str:
    if old == '' and new.strip():
        return _insert_or_replace_memory_day_block(current, new)
    if old not in current and _extract_memory_day_date(new):
        return _insert_or_replace_memory_day_block(current, new)
    return _apply_replace_text(current, old, new, entity_name='memory')


def _apply_memory_edit_operations(current_content: str, payload: Dict[str, Any]) -> str:
    operations = _parse_edit_operations(payload, entity_name='memory', allow_empty_old=True)
    if operations[0]['op'] == 'replace_all':
        return _compact_memory_to_recent_week(operations[0]['content'])

    current = _compact_memory_to_recent_week(current_content)
    for op in operations:
        if op['op'] == 'replace_text':
            if op['old'] == op['new']:
                continue
            current = _apply_memory_replace_text_operation(current, op['old'], op['new'])

    return _compact_memory_to_recent_week(current.strip())


# Register
_PROMPT_BUILDERS['memory'] = _build_memory_prompt
_EDIT_DISPATCH['memory'] = _apply_memory_edit_operations
