from __future__ import annotations

import re
import json
from typing import Any, Iterable

from chat.components.skill_review.schemas import Trajectory, TrajectoryStep

_TOOL_ROLE_NAMES = {'tool', 'function', 'tool_call'}


def build_trajectory(
    session: Any,
    *,
    min_user_turns: int,
    min_tool_turns: int,
) -> Trajectory:
    steps: list[TrajectoryStep] = []
    called_tools = set()
    called_skills = {}

    for index, message in enumerate(session.get('messages', []), start=1):
        role = message.get('role')
        if not role:
            continue
        tool_name = message.get('name') if role == 'tool' else None
        skill_name = json.loads(message.get('content')).get('name') if role == 'tool' and tool_name == 'get_skill' else None
        skill_content = json.loads(message.get('content')).get('content') if role == 'tool' and tool_name == 'get_skill' else None
        if tool_name:
            called_tools.add(tool_name)
        if skill_name and skill_content:
            from lazyllm import LOG
            LOG.info(f'find skill_name: {skill_name}')
            called_skills[skill_name] = skill_content
        steps.append(
            TrajectoryStep(
                step_index=index,
                role=role,
                action=_shorten(message.get('content'), 1200),
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

    return Trajectory(
        session_id=str(session.get('conversation_id')),
        user_turns=user_turns,
        tool_turns=tool_turns,
        called_tools=list(called_tools),
        called_skills=called_skills,
        steps=steps,
        steps_text=format_steps_text(steps),
        qualified=qualified,
    )


def format_steps_text(steps: list[TrajectoryStep]) -> str:
    lines: list[str] = []
    for step in steps:
        role = step.role
        if step.tool_name:
            role = f'{role}({step.tool_name})'
        elif step.skill_name:
            role = f'{role}[{step.skill_name}]'
        lines.append(f'- {role}: {step.action}')
    return '\n'.join(lines)


def _shorten(text: str, limit: int) -> str:
    text = str(text or '').strip()
    if len(text) <= limit:
        return text
    return text[: limit - 3].rstrip() + '...'
