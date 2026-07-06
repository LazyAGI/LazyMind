from __future__ import annotations

from lazymind.chat.engine.prompts.system_prompt import build_system_prompt


def test_system_prompt_uses_user_timezone_time() -> None:
    prompt = build_system_prompt(
        set(),
        environment_context={
            'time': {
                'now': '2026-05-11T11:48:00.000Z',
                'timezone': 'Asia/Shanghai',
            },
        },
    )

    assert 'Current user time: 2026-05-11 19:48:00 (Asia/Shanghai)' in prompt
    assert 'Use this context to interpret relative time expressions' not in prompt
    assert 'User timezone:' not in prompt


def test_system_prompt_falls_back_to_raw_time_when_timezone_is_invalid() -> None:
    prompt = build_system_prompt(
        set(),
        environment_context={
            'time': {
                'now': '2026-05-11T11:48:00.000Z',
                'timezone': 'Bad/Timezone',
            },
        },
    )

    assert 'Current user time: 2026-05-11T11:48:00.000Z' in prompt


def test_system_prompt_includes_kb_first_guidance_when_kb_is_active() -> None:
    prompt = build_system_prompt({'kb'})

    assert 'The knowledge base is your primary evidence source.' in prompt
    assert 'Knowledge evidence citation rules' in prompt


def test_system_prompt_omits_kb_first_guidance_without_kb_tools() -> None:
    prompt = build_system_prompt(set())

    assert 'The knowledge base is your primary evidence source.' not in prompt
    assert 'Knowledge evidence citation rules' not in prompt


def test_system_prompt_includes_web_search_guidance_when_web_search_is_active() -> None:
    prompt = build_system_prompt({'web_search'})

    assert 'When using `web_search`, the `query` must represent one search intent.' in prompt
