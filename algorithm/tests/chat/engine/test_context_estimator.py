from __future__ import annotations

import asyncio

from lazymind.chat.engine.agent_runtime import (
    AgentRole,
    AgentRunPlan,
    PromptBuilder,
    estimate_context_usage,
    estimate_tokens,
    report_to_dict,
    render_context_markdown,
)


def test_estimate_tokens_handles_language_families_without_tokenizer() -> None:
    assert estimate_tokens('') == 0
    assert estimate_tokens('four') == 1
    assert estimate_tokens('中文') >= 2
    assert estimate_tokens('hello 中文 🙂') > estimate_tokens('hello')


def test_context_report_groups_plan_without_exposing_content() -> None:
    prompt = (
        PromptBuilder.for_role(AgentRole.CHAT)
        .system('identity', '', 'secret system text', 'platform')
        .runtime(
            'state', 'Plugin State', 'secret runtime text', 'plugin',
            authoritative=True, content_kind='state',
        )
        .input('secret user text', source='user')
        .build()
    )
    plan = AgentRunPlan(
        role=AgentRole.CHAT,
        prompt=prompt,
        history=[{'role': 'user', 'content': 'secret history text'}],
    )
    report = asyncio.run(estimate_context_usage(plan, {
        'system_prompt': prompt.system_prompt,
        'tool_definitions': [{
            'type': 'function',
            'function': {'name': 'search', 'description': 'secret tool description'},
        }],
        'skills_prompt': 'secret skill text',
    }))
    payload = report_to_dict(report)
    rendered = str(payload)

    assert report.estimated_tokens == sum(item.estimated_tokens for item in report.categories)
    assert {item.category_id for item in report.categories} >= {
        'system', 'runtime', 'input', 'conversation', 'tools', 'skills',
    }
    assert 'secret system text' not in rendered
    assert 'secret history text' not in rendered
    assert 'secret user text' not in rendered


def test_context_estimation_runs_in_worker_thread(monkeypatch) -> None:
    called = False

    async def fake_to_thread(function, *args):
        nonlocal called
        called = True
        return function(*args)

    monkeypatch.setattr(asyncio, 'to_thread', fake_to_thread)
    prompt = PromptBuilder.for_role(AgentRole.CHAT).input('hello', source='user').build()
    report = asyncio.run(estimate_context_usage(
        AgentRunPlan(role=AgentRole.CHAT, prompt=prompt),
        {'system_prompt': '', 'tool_definitions': [], 'skills_prompt': ''},
    ))

    assert called
    assert report.estimated_tokens > 0


def test_context_markdown_contains_exact_model_facing_parts() -> None:
    prompt = (
        PromptBuilder.for_role(AgentRole.CHAT)
        .system('identity', '', 'system body', 'platform')
        .runtime('state', 'State', 'runtime body', 'runtime')
        .input('user body', source='user')
        .build()
    )
    plan = AgentRunPlan(
        role=AgentRole.CHAT,
        prompt=prompt,
        history=[{'role': 'assistant', 'content': 'history body'}],
    )
    rendered = render_context_markdown(plan, {
        'system_prompt': 'final system body',
        'tool_definitions': [{'type': 'function', 'function': {'name': 'search'}}],
        'skills_prompt': 'skills body',
    })

    assert 'final system body' in rendered
    assert '"name": "search"' in rendered
    assert 'skills body' in rendered
    assert 'history body' in rendered
    assert prompt.current_input in rendered
