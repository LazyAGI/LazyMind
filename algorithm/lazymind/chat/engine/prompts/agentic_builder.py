from __future__ import annotations

from lazymind.chat.engine.prompts.agentic import (
    DEFAULT_SYSTEM_PROMPT,
    IMAGE_REFERENCE_MARKDOWN_GUIDANCE,
    MEMORY_GUIDANCE,
    SEARCH_GUIDANCE,
    SKILLS_GUIDANCE,
    TOOL_CALL_STATUS_GUIDANCE,
    VISION_EXTRACTOR_GUIDANCE,
    VOCAB_GUIDANCE,
)


def _build_environment_context_prompt(*, time_now: str | None = None, timezone: str | None = None) -> str:
    lines = []
    if time_now and time_now.strip():
        lines.append(f'Current user time: {time_now.strip()}')
    if timezone and timezone.strip():
        lines.append(f'User timezone: {timezone.strip()}')
    if not lines:
        return ''

    return (
        '## Environment Context\n'
        + '\n'.join(lines)
        + '\n\n'
        + 'Use this context to interpret relative time expressions such as today, tomorrow, now, '
        + 'this morning, tonight, 本周, 今天, 明天, 现在. Do not assume the server timezone is the user timezone.'
    )


def _build_system_prompt(
    available_tools: list[str],
    *,
    time_now: str | None = None,
    timezone: str | None = None,
    use_memory: bool = True,
    user_preference: str | None = None,
    memory: str | None = None,
    image_files: list | None = None,
) -> str:
    prompt_parts = [DEFAULT_SYSTEM_PROMPT]

    environment_prompt = _build_environment_context_prompt(time_now=time_now, timezone=timezone)
    if environment_prompt:
        prompt_parts.append(environment_prompt)

    if use_memory:
        if (isinstance(user_preference, str) and user_preference.strip()) or (
            isinstance(memory, str) and memory.strip()
        ):
            memory_block = []
            if isinstance(user_preference, str) and user_preference.strip():
                memory_block.append(f'## User Profile / Preferences\n{user_preference.strip()}')
            if isinstance(memory, str) and memory.strip():
                memory_block.append(f'## Agent Working Memory\n{memory.strip()}')
            prompt_parts.append('\n\n'.join(memory_block))

    tool_guidance: list[str] = []
    if 'vocab_manage' in available_tools:
        tool_guidance.append(VOCAB_GUIDANCE)
    if 'memory' in available_tools and use_memory:
        tool_guidance.append(MEMORY_GUIDANCE)
    if 'skill_manage' in available_tools:
        tool_guidance.append(SKILLS_GUIDANCE)
    if tool_guidance:
        prompt_parts.append(' '.join(tool_guidance))
    if available_tools:
        prompt_parts.append(TOOL_CALL_STATUS_GUIDANCE)
    if any(tool.startswith('kb_') for tool in available_tools):
        prompt_parts.append(SEARCH_GUIDANCE)
    if any(tool.startswith('kb_') for tool in available_tools) or (image_files or []):
        prompt_parts.append(IMAGE_REFERENCE_MARKDOWN_GUIDANCE)
    if 'vision_extractor' in available_tools:
        prompt_parts.append(VISION_EXTRACTOR_GUIDANCE)

    return '\n\n'.join(prompt_parts)
