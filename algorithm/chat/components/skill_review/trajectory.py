from __future__ import annotations

import re
from typing import Any, Iterable

from chat.components.skill_review.schemas import Trajectory, TrajectoryStep

_TOOL_ROLE_NAMES = {'tool', 'function', 'tool_call'}


def build_trajectory(
    session: Any,
    *,
    min_user_turns: int,
    min_tool_turns: int,
) -> Trajectory:
    # TODO: Implement for Trajectory
    steps: list[TrajectoryStep] = []
    called_tools: list[str] = []
    called_skills: list[str] = []

    for index, message in enumerate(_get_messages(session), start=1):
        role = _normalize_role(_get_value(message, 'role') or _get_value(message, 'type') or 'unknown')
        tool_name = _optional_str(_get_value(message, 'tool_name') or _get_value(message, 'name') or _extract_tool_name(message))
        skill_name = _optional_str(_get_value(message, 'skill_name') or _get_value(message, 'skill') or _extract_skill_name(message))
        if tool_name:
            called_tools.append(tool_name)
        if skill_name:
            called_skills.append(skill_name)
        steps.append(
            TrajectoryStep(
                step_index=index,
                role=role,
                action=_shorten(_get_value(message, 'content') or _get_value(message, 'result') or '', 1200),
                state='',
                tool_name=tool_name,
                skill_name=skill_name,
            )
        )

    user_turns = sum(1 for step in steps if step.role == 'user')
    tool_turns = sum(
        1 for step in steps
        if step.role in _TOOL_ROLE_NAMES or step.tool_name
    )
    qualified = user_turns >= min_user_turns and tool_turns >= min_tool_turns
    skip_reason = None
    if not qualified:
        skip_reason = (
            f'trigger threshold not met: user_turns={user_turns}, '
            f'tool_turns={tool_turns}, min_user_turns={min_user_turns}, '
            f'min_tool_turns={min_tool_turns}'
        )

    return Trajectory(
        session_id=str(_get_value(session, 'session_id') or _get_value(session, 'conversation_id') or _get_value(session, 'id') or ''),
        user_turns=user_turns,
        tool_turns=tool_turns,
        called_tools=_unique(called_tools),
        called_skills=_unique(called_skills),
        steps=steps,
        steps_text=format_steps_text(steps),
        qualified=qualified,
        skip_reason=skip_reason,
    )


def format_steps_text(steps: list[TrajectoryStep]) -> str:
    lines: list[str] = []
    for step in steps:
        role = step.role
        if step.tool_name:
            role = f'{role}({step.tool_name})'
        elif step.skill_name:
            role = f'{role}[{step.skill_name}]'
        lines.append(f'[{step.step_index}] {role}: {step.action}')
    return '\n'.join(lines)


def _normalize_role(role: str) -> str:
    lowered = str(role or '').strip().lower()
    if lowered in {'human', 'customer'}:
        return 'user'
    if lowered in {'ai', 'agent', 'bot'}:
        return 'assistant'
    if 'tool' in lowered or 'function' in lowered:
        return 'tool'
    return lowered or 'unknown'


def _get_messages(session: Any) -> list[Any]:
    messages = _get_value(session, 'messages')
    if isinstance(messages, list):
        return messages
    return []


def _get_value(item: Any, key: str) -> Any:
    if isinstance(item, dict):
        return item.get(key)
    return getattr(item, key, None)


def _optional_str(value: Any) -> str | None:
    if value is None:
        return None
    text = str(value).strip()
    return text or None


def _extract_tool_name(message: Any) -> str | None:
    raw_text = str(_get_value(message, 'raw') or message or {})
    match = re.search(r'"(?:tool_name|tool|function_name|name)"\s*:\s*"([^"]+)"', raw_text)
    if match:
        return match.group(1)
    return None


def _extract_skill_name(message: Any) -> str | None:
    raw_text = str(_get_value(message, 'raw') or message or {})
    match = re.search(r'"(?:skill_name|skill|called_skill)"\s*:\s*"([^"]+)"', raw_text)
    if match:
        return match.group(1)
    return None


def _unique(values: Iterable[str]) -> list[str]:
    seen: set[str] = set()
    result: list[str] = []
    for value in values:
        trimmed = str(value or '').strip()
        if not trimmed or trimmed in seen:
            continue
        seen.add(trimmed)
        result.append(trimmed)
    return result


def _shorten(text: str, limit: int) -> str:
    text = str(text or '').strip()
    if len(text) <= limit:
        return text
    return text[: limit - 3].rstrip() + '...'
