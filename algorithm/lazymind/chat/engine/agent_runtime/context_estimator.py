from __future__ import annotations

import asyncio
from dataclasses import asdict
import json
import math
from typing import Any

from .models import (
    AgentRunPlan,
    ContextUsageCategory,
    ContextUsageItem,
    ContextUsageReport,
)


_CATEGORY_TITLES = {
    'system': 'System prompt',
    'tools': 'Tool definitions',
    'runtime': 'Runtime context',
    'skills': 'Skills',
    'conversation': 'Conversation',
    'input': 'Current instruction',
    'formatting': 'Prompt formatting',
}


def estimate_tokens(text: str) -> int:
    """Fast model-agnostic approximation; deliberately avoids tokenizer dependencies."""
    weight = 0.0
    for char in str(text or ''):
        code = ord(char)
        if char.isspace():
            weight += 0.1
        elif code < 128 and char.isalnum():
            weight += 0.25
        elif code < 128:
            weight += 0.4
        elif 0x3400 <= code <= 0x9FFF or 0xF900 <= code <= 0xFAFF:
            weight += 1.1
        elif 0x3040 <= code <= 0x30FF or 0xAC00 <= code <= 0xD7AF:
            weight += 1.1
        else:
            weight += 1.5
    return math.ceil(weight) if weight else 0


def _item(
    item_id: str,
    category: str,
    title: str,
    source: str,
    text: str,
    *,
    item_count: int = 1,
    channel: str | None = None,
    content_kind: str | None = None,
    authoritative: bool = False,
    fixed_overhead: int = 0,
    display_content: str | None = None,
) -> ContextUsageItem:
    return ContextUsageItem(
        item_id=item_id,
        category=category,
        title=title,
        source=source,
        estimated_tokens=estimate_tokens(text) + fixed_overhead,
        char_count=len(text),
        item_count=item_count,
        channel=channel,
        content_kind=content_kind,
        authoritative=authoritative,
        content=text if display_content is None else display_content,
    )


def _history_title(message: dict[str, Any], index: int) -> str:
    role = str(message.get('role') or 'unknown')
    if role == 'tool':
        name = str(message.get('name') or '').strip()
        return f'Tool result · {name}' if name else 'Tool result'
    if role == 'assistant' and message.get('tool_calls'):
        calls = message.get('tool_calls') or []
        names = [
            str(call.get('function', {}).get('name') or '')
            for call in calls if isinstance(call, dict)
        ]
        suffix = ', '.join(name for name in names if name)
        return f'Assistant tool call · {suffix}' if suffix else 'Assistant tool call'
    titles = {'user': 'User message', 'assistant': 'Assistant message', 'system': 'System message'}
    return titles.get(role, f'Message {index + 1} · {role}')


def _estimate(plan: AgentRunPlan, agent_context: dict[str, Any]) -> ContextUsageReport:
    grouped: dict[str, list[ContextUsageItem]] = {key: [] for key in _CATEGORY_TITLES}
    for section in plan.prompt.sections:
        category = 'system' if section.channel == 'system' else 'runtime'
        grouped[category].append(_item(
            section.section_id,
            category,
            section.title or section.section_id,
            section.source,
            section.content,
            channel=section.channel,
            content_kind=section.content_kind,
            authoritative=section.authoritative,
        ))

    grouped['input'].append(_item(
        'current_input', 'input', plan.prompt.input_title, 'user',
        plan.prompt.input_content, fixed_overhead=4,
    ))

    model_history = agent_context.get('history', plan.history)
    for index, message in enumerate(model_history):
        compact = json.dumps(message, ensure_ascii=False, separators=(',', ':'), default=str)
        rendered = json.dumps(message, ensure_ascii=False, indent=2, default=str)
        grouped['conversation'].append(_item(
            f'history_{index}', 'conversation', _history_title(message, index),
            f"history.{message.get('role', 'unknown')}", compact,
            fixed_overhead=4, display_content=rendered,
        ))

    tool_definitions = agent_context.get('tool_definitions') or []
    for index, tool in enumerate(tool_definitions):
        rendered = json.dumps(tool, ensure_ascii=False, separators=(',', ':'), default=str)
        function = tool.get('function', {}) if isinstance(tool, dict) else {}
        name = function.get('name') or (tool.get('name') if isinstance(tool, dict) else '')
        grouped['tools'].append(_item(
            f'tool_{name or index}', 'tools', str(name or f'Tool {index + 1}'),
            'tool.registry', rendered, fixed_overhead=2,
            display_content=json.dumps(tool, ensure_ascii=False, indent=2, default=str),
        ))

    skill_prompt_parts = agent_context.get('skill_prompt_parts') or []
    if skill_prompt_parts:
        for index, part in enumerate(skill_prompt_parts):
            grouped['skills'].append(_item(
                str(part.get('item_id') or f'skill_part_{index}'),
                'skills', str(part.get('title') or f'Skill {index + 1}'),
                str(part.get('source') or 'skill.registry'),
                str(part.get('content') or ''),
                content_kind=str(part.get('content_kind') or 'reference'),
            ))
    else:
        skills_prompt = str(agent_context.get('skills_prompt') or '')
        if skills_prompt:
            grouped['skills'].append(_item(
                'skills_prompt', 'skills', 'Available skills', 'skill.registry', skills_prompt,
            ))

    visible_system = str(agent_context.get('system_prompt') or plan.prompt.system_prompt)
    accounted_system = sum(item.estimated_tokens for item in grouped['system'])
    accounted_system_chars = sum(item.char_count for item in grouped['system'])
    formatting_tokens = max(0, estimate_tokens(visible_system) - accounted_system)
    formatting_chars = max(0, len(visible_system) - accounted_system_chars)
    rendered_input_tokens = estimate_tokens(plan.prompt.current_input)
    accounted_input = sum(item.estimated_tokens for item in grouped['runtime'] + grouped['input'])
    accounted_input_chars = sum(item.char_count for item in grouped['runtime'] + grouped['input'])
    formatting_tokens += max(0, rendered_input_tokens - accounted_input)
    formatting_chars += max(0, len(plan.prompt.current_input) - accounted_input_chars)
    if formatting_tokens:
        grouped['formatting'].append(ContextUsageItem(
            item_id='prompt_boundaries', category='formatting', title='Prompt boundaries',
            source='prompt.renderer', estimated_tokens=formatting_tokens,
            char_count=formatting_chars,
            content=(
                'Prompt section headings, separators, role wrappers, and other '
                'serialization overhead added around the displayed content.'
            ),
        ))

    categories = []
    for category_id, title in _CATEGORY_TITLES.items():
        items = tuple(grouped[category_id])
        if not items:
            continue
        categories.append(ContextUsageCategory(
            category_id=category_id,
            title=title,
            estimated_tokens=sum(item.estimated_tokens for item in items),
            char_count=sum(item.char_count for item in items),
            item_count=sum(item.item_count for item in items),
            items=items,
        ))
    return ContextUsageReport(
        scope='next_request',
        estimated_tokens=sum(category.estimated_tokens for category in categories),
        categories=tuple(categories),
    )


async def estimate_context_usage(
    plan: AgentRunPlan,
    agent_context: dict[str, Any],
) -> ContextUsageReport:
    return await asyncio.to_thread(_estimate, plan, agent_context)


def report_to_dict(report: ContextUsageReport) -> dict[str, Any]:
    return asdict(report)


def render_context_markdown(plan: AgentRunPlan, agent_context: dict[str, Any]) -> str:
    """Render the complete next-request model context as a readable Markdown export."""
    sections = [
        '# ChatAgent Context',
        '',
        '> Complete model-facing context assembled for the next request.',
        '',
        '## System Prompt',
        '',
        str(agent_context.get('system_prompt') or plan.prompt.system_prompt),
        '',
        '## Tool Definitions',
        '',
        '```json',
        json.dumps(agent_context.get('tool_definitions') or [], ensure_ascii=False, indent=2, default=str),
        '```',
    ]
    skills_prompt = str(agent_context.get('skills_prompt') or '')
    if skills_prompt:
        sections.extend(['', '## Skills Prompt', '', skills_prompt])
    sections.extend(['', '## Conversation History', ''])
    model_history = agent_context.get('history', plan.history)
    if model_history:
        for index, message in enumerate(model_history, 1):
            role = str(message.get('role') or 'unknown')
            content = message.get('content', '')
            if not isinstance(content, str):
                content = json.dumps(content, ensure_ascii=False, indent=2, default=str)
            sections.extend([f'### Message {index} · {role}', '', content, ''])
    else:
        sections.extend(['_No conversation history._', ''])
    sections.extend(['## Current Input', '', plan.prompt.current_input, ''])
    return '\n'.join(sections)
