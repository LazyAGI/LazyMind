from __future__ import annotations

from datetime import datetime, timezone as datetime_timezone
from zoneinfo import ZoneInfo, ZoneInfoNotFoundError

from .guidance import (
    ATTACHED_FILES_GUIDANCE,
    DEFAULT_SYSTEM_PROMPT,
    IMAGE_REFERENCE_MARKDOWN_GUIDANCE,
    KNOWLEDGE_EVIDENCE_CITATION_GUIDANCE,
    TOOL_CALL_STATUS_GUIDANCE,
)

_KNOWLEDGE_EVIDENCE_GROUPS = {'kb', 'temp_kb'}


def _format_user_time(time_now: object, timezone: object) -> str:
    raw_time = str(time_now).strip()
    if not raw_time:
        return ''

    timezone_name = str(timezone).strip() if timezone is not None else ''
    try:
        normalized_time = raw_time[:-1] + '+00:00' if raw_time.endswith('Z') else raw_time
        parsed_time = datetime.fromisoformat(normalized_time)
        if parsed_time.tzinfo is None:
            parsed_time = parsed_time.replace(tzinfo=datetime_timezone.utc)
        if timezone_name:
            user_time = parsed_time.astimezone(ZoneInfo(timezone_name))
            return f'{user_time:%Y-%m-%d %H:%M:%S} ({timezone_name})'
        return parsed_time.isoformat()
    except (ValueError, TypeError, ZoneInfoNotFoundError):
        return raw_time


def _build_environment_context_prompt(environment_context: dict | None = None) -> str:
    time_now = None
    timezone = None
    if isinstance(environment_context, dict):
        time_info = environment_context.get('time') or {}
        if isinstance(time_info, dict):
            time_now = time_info.get('now')
            timezone = time_info.get('timezone')

    user_time = _format_user_time(time_now, timezone) if time_now else ''
    if not user_time:
        return ''

    return f'## Environment Context\nCurrent user time: {user_time}'


def _build_attached_files_prompt(files: list | None = None) -> str:
    clean = [str(path).strip() for path in (files or []) if str(path).strip()]
    if not clean:
        return ''
    lines = ['## Attached Files']
    lines.extend(f'- {path}' for path in clean)
    return '\n'.join(lines) + '\n\n' + ATTACHED_FILES_GUIDANCE


def build_system_prompt(
    active_groups: set[str],
    *,
    environment_context: dict | None = None,
    use_memory: bool = True,
    user_preference: str | None = None,
    memory: str | None = None,
    files: list | None = None,
) -> str:
    prompt_parts = [DEFAULT_SYSTEM_PROMPT]

    environment_prompt = _build_environment_context_prompt(environment_context)
    if environment_prompt:
        prompt_parts.append(environment_prompt)

    attached_files_prompt = _build_attached_files_prompt(files)
    if attached_files_prompt:
        prompt_parts.append(attached_files_prompt)

    if use_memory:
        if isinstance(user_preference, str) and user_preference.strip():
            preference_block = (
                '## User Profile / Preferences\n'
                "The following profile entries describe the user's long-term preferences"
                ' and identity.\n'
                "Apply a preference **only when it is relevant to the user's current"
                ' intent**.\n'
                'If a preference conflicts with or is unrelated to what the user is'
                ' actually asking for in this turn, ignore it.\n'
                'Do not force-apply style, format, or persona preferences when the'
                " user's question is factual, technical, or unrelated to that"
                ' preference.\n\n'
                + user_preference.strip()
                + '\n\n<!-- end of User Profile / Preferences -->'
            )
            prompt_parts.append(preference_block)
        if isinstance(memory, str) and memory.strip():
            prompt_parts.append(f'## Agent Working Memory\n{memory.strip()}')

    if active_groups:
        prompt_parts.append(TOOL_CALL_STATUS_GUIDANCE)
    if active_groups & _KNOWLEDGE_EVIDENCE_GROUPS:
        prompt_parts.append(KNOWLEDGE_EVIDENCE_CITATION_GUIDANCE)
    if (
        files
        or 'image_generator' in active_groups
        or 'image_editor' in active_groups
    ):
        prompt_parts.append(IMAGE_REFERENCE_MARKDOWN_GUIDANCE)

    return '\n\n'.join(prompt_parts)
