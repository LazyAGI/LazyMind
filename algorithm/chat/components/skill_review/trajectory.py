from __future__ import annotations

import re
from typing import Iterable

from chat.components.skill_review.schemas import SessionData, SessionMessage, Trajectory, TrajectoryStep

_TOOL_ROLE_NAMES = {'tool', 'function', 'tool_call'}


def build_trajectory(
    session: SessionData,
    *,
    min_user_turns: int,
    min_tool_turns: int,
) -> Trajectory:
    steps: list[TrajectoryStep] = []
    called_tools: list[str] = []
    called_skills: list[str] = []

    for index, message in enumerate(session.messages, start=1):
        role = _normalize_role(message.role)
        tool_name = message.tool_name or _extract_tool_name(message)
        skill_name = message.skill_name or _extract_skill_name(message)
        if tool_name:
            called_tools.append(tool_name)
        if skill_name:
            called_skills.append(skill_name)
        steps.append(
            TrajectoryStep(
                step_index=index,
                role=role,
                action=_shorten(message.content, 1200),
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
        session_id=session.session_id,
        user_turns=user_turns,
        tool_turns=tool_turns,
        called_tools=_unique(called_tools),
        called_skills=_unique(called_skills),
        steps=steps,
        qualified=qualified,
        skip_reason=skip_reason,
    )


def _normalize_role(role: str) -> str:
    lowered = str(role or '').strip().lower()
    if lowered in {'human', 'customer'}:
        return 'user'
    if lowered in {'ai', 'agent', 'bot'}:
        return 'assistant'
    if 'tool' in lowered or 'function' in lowered:
        return 'tool'
    return lowered or 'unknown'


def _extract_tool_name(message: SessionMessage) -> str | None:
    raw_text = str(message.raw or {})
    match = re.search(r'"(?:tool_name|tool|function_name|name)"\s*:\s*"([^"]+)"', raw_text)
    if match:
        return match.group(1)
    return None


def _extract_skill_name(message: SessionMessage) -> str | None:
    raw_text = str(message.raw or {})
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
