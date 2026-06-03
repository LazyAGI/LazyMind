from __future__ import annotations

from .guidance import (
    ATTACHED_FILES_GUIDANCE,
    DEFAULT_SYSTEM_PROMPT,
    IMAGE_REFERENCE_MARKDOWN_GUIDANCE,
    MEMORY_GUIDANCE,
    SEARCH_GUIDANCE,
    SKILLS_GUIDANCE,
    TOOL_CALL_STATUS_GUIDANCE,
    VISION_EXTRACTOR_GUIDANCE,
    VOCAB_GUIDANCE,
)


def _build_environment_context_prompt(environment_context: dict | None = None) -> str:
    time_now = None
    timezone = None
    if isinstance(environment_context, dict):
        time_info = environment_context.get('time') or {}
        if isinstance(time_info, dict):
            time_now = time_info.get('now')
            timezone = time_info.get('timezone')

    lines = []
    if time_now and str(time_now).strip():
        lines.append(f'Current user time: {str(time_now).strip()}')
    if timezone and str(timezone).strip():
        lines.append(f'User timezone: {str(timezone).strip()}')
    if not lines:
        return ''

    return (
        '## Environment Context\n'
        + '\n'.join(lines)
        + '\n\n'
        + 'Use this context to interpret relative time expressions such as today, tomorrow, now, '
        + 'this morning, tonight, 本周, 今天, 明天, 现在. Do not assume the server timezone is the user timezone.'
    )


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
            prompt_parts.append(f'## User Profile / Preferences\n{user_preference.strip()}')
        if isinstance(memory, str) and memory.strip():
            prompt_parts.append(f'## Agent Working Memory\n{memory.strip()}')

    tool_guidance: list[str] = []
    if 'vocab_learn' in active_groups:
        tool_guidance.append(VOCAB_GUIDANCE)
    if 'memory_editor' in active_groups and use_memory:
        tool_guidance.append(MEMORY_GUIDANCE)
    if 'skill_editor' in active_groups:
        tool_guidance.append(SKILLS_GUIDANCE)
    if tool_guidance:
        prompt_parts.append(' '.join(tool_guidance))
    if active_groups:
        prompt_parts.append(TOOL_CALL_STATUS_GUIDANCE)
    if 'kb' in active_groups or 'temp_kb' in active_groups:
        prompt_parts.append(SEARCH_GUIDANCE)
    if files:
        prompt_parts.append(IMAGE_REFERENCE_MARKDOWN_GUIDANCE)
    if 'multimodal' in active_groups:
        prompt_parts.append(VISION_EXTRACTOR_GUIDANCE)

    return '\n\n'.join(prompt_parts)
