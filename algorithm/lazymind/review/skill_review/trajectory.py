from __future__ import annotations

import json
from pathlib import Path
from typing import Any

from lazymind.review.skill_review.config import STAGE_FILES, STAGE_TRAJECTORY
from lazymind.review.skill_review.schemas import Trajectory, TrajectoryStep
from lazymind.review.skill_review.reports import finish_stage_report, stage_error, start_stage, write_json_file


def build_trajectory(
    session: Any,
    *,
    min_user_turns: int,
    min_tool_turns: int,
) -> Trajectory:
    steps: list[TrajectoryStep] = []
    called_tools = set()
    called_skills = {}
    tool_calls_by_id: dict[str, dict[str, Any]] = {}
    tool_turn_count = 0

    for _index, message in enumerate(session.get('messages', []), start=1):
        role = message.get('role')
        if not role:
            continue
        if role == 'assistant':
            for tool_call in _iter_tool_calls(message.get('tool_calls')):
                tool_call_id = str(tool_call.get('id') or '')
                tool_name = str(tool_call.get('name') or '').strip()
                arguments = tool_call.get('arguments') or {}
                if tool_name:
                    called_tools.add(tool_name)
                if tool_call_id:
                    tool_calls_by_id[tool_call_id] = tool_call
                tool_turn_count += 1
                steps.append(
                    TrajectoryStep(
                        step_index=len(steps) + 1,
                        role='tool_call',
                        action=_shorten(json.dumps(arguments, ensure_ascii=False), 1200),
                        state='',
                        tool_name=tool_name or None,
                    )
                )

        tool_name = _tool_name_for_message(message, tool_calls_by_id) if role == 'tool' else None
        if role == 'tool' and not _has_known_tool_call(message, tool_calls_by_id):
            tool_turn_count += 1
        skill_payload = _skill_payload_for_tool_result(message, tool_name, tool_calls_by_id)
        skill_name = skill_payload.get('name') if skill_payload else None
        skill_content = skill_payload.get('content') if skill_payload else None
        if tool_name:
            called_tools.add(tool_name)
        if skill_name and skill_content:
            called_skills[skill_name] = skill_content
        steps.append(
            TrajectoryStep(
                step_index=len(steps) + 1,
                role=role,
                action=_shorten(message.get('content'), 1200),
                state='',
                tool_name=tool_name,
                skill_name=skill_name,
            )
        )

    user_turns = sum(1 for step in steps if step.role == 'user')
    tool_turns = tool_turn_count
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


def build_trajectories(
    sessions: list[dict[str, Any]],
    *,
    min_user_turns: int,
    min_tool_turns: int,
    artifact_dir: Path | None = None,
) -> tuple[list[Trajectory], dict[str, Any]]:
    started_at = start_stage()
    trajectories = []
    errors: list[dict] = []
    for index, session in enumerate(sessions):
        item_id = str(session.get('conversation_id') or index)
        try:
            trajectories.append(
                build_trajectory(
                    session,
                    min_user_turns=min_user_turns,
                    min_tool_turns=min_tool_turns,
                )
            )
        except Exception as exc:
            errors.append(stage_error(STAGE_TRAJECTORY, item_id, exc))
    if artifact_dir is not None:
        write_json_file(artifact_dir / STAGE_FILES[STAGE_TRAJECTORY], trajectories)
    return trajectories, finish_stage_report(
        STAGE_TRAJECTORY,
        started_at,
        input_count=len(sessions),
        output_count=len(trajectories),
        errors=errors,
        status='failed' if errors and not trajectories else 'completed',
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


def _json_object(value: Any) -> dict[str, Any]:
    if isinstance(value, dict):
        return value
    try:
        parsed = json.loads(value or '{}')
    except Exception:
        return {}
    return parsed if isinstance(parsed, dict) else {}


def _iter_tool_calls(value: Any) -> list[dict[str, Any]]:
    if not isinstance(value, list):
        return []
    calls = []
    for raw in value:
        if not isinstance(raw, dict):
            continue
        function = raw.get('function') if isinstance(raw.get('function'), dict) else {}
        name = raw.get('name') or function.get('name')
        arguments = raw.get('arguments') if 'arguments' in raw else function.get('arguments')
        if isinstance(arguments, str):
            arguments = _json_object(arguments)
        if not isinstance(arguments, dict):
            arguments = {}
        calls.append({
            'id': raw.get('id'),
            'name': name,
            'arguments': arguments,
        })
    return calls


def _tool_name_for_message(
    message: dict[str, Any],
    tool_calls_by_id: dict[str, dict[str, Any]],
) -> str | None:
    name = str(message.get('name') or message.get('tool_name') or '').strip()
    if name:
        return name
    tool_call_id = str(message.get('tool_call_id') or '').strip()
    if tool_call_id:
        tool_call = tool_calls_by_id.get(tool_call_id) or {}
        name = str(tool_call.get('name') or '').strip()
    return name or None


def _has_known_tool_call(
    message: dict[str, Any],
    tool_calls_by_id: dict[str, dict[str, Any]],
) -> bool:
    tool_call_id = str(message.get('tool_call_id') or '').strip()
    return bool(tool_call_id and tool_call_id in tool_calls_by_id)


def _skill_payload_for_tool_result(
    message: dict[str, Any],
    tool_name: str | None,
    tool_calls_by_id: dict[str, dict[str, Any]],
) -> dict[str, Any]:
    if tool_name != 'get_skill':
        return {}

    content = message.get('content')
    payload = _json_object(content)
    tool_call = tool_calls_by_id.get(str(message.get('tool_call_id') or '').strip()) or {}
    arguments = tool_call.get('arguments') if isinstance(tool_call.get('arguments'), dict) else {}

    name = payload.get('name') or payload.get('skill_name') or arguments.get('name') or arguments.get('skill_name')
    skill_content = payload.get('content') or payload.get('skill_content')
    if skill_content is None:
        skill_content = content
    if not name:
        return {}
    return {
        'name': str(name),
        'content': str(skill_content or ''),
    }
