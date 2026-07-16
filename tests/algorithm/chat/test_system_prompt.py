from __future__ import annotations

from lazymind.chat.engine.prompts.system_prompt import build_system_prompt


def test_system_prompt_uses_user_timezone_time() -> None:
    prompt = build_system_prompt(
        False,
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
        False,
        environment_context={
            'time': {
                'now': '2026-05-11T11:48:00.000Z',
                'timezone': 'Bad/Timezone',
            },
        },
    )

    assert 'Current user time: 2026-05-11T11:48:00.000Z' in prompt


def test_system_prompt_includes_cross_tool_policy_when_tools_are_active() -> None:
    prompt = build_system_prompt(True)

    assert '# Tool use policy' in prompt
    assert 'get_*Toolkit_methods' in prompt
    assert 'knowledge-base evidence' in prompt


def test_system_prompt_omits_tool_policy_without_tools() -> None:
    prompt = build_system_prompt(False)

    assert '# Tool use policy' not in prompt


def test_system_prompt_does_not_embed_tool_specific_web_guidance() -> None:
    prompt = build_system_prompt(True)

    assert '# Tool use policy' in prompt
    assert 'one search intent' not in prompt


def test_long_url_does_not_override_chinese_request_language() -> None:
    prompt = build_system_prompt(
        True,
        current_query=(
            '帮我看看这个计划 '
            'https://sensetime.feishu.cn/wiki/CTxvwpohviXgZiklv36cEJVwnac '
            '有什么问题，有哪些改进意见'
        ),
        environment_context={'locale': 'en-US'},
    )

    assert 'Selected response language for this turn: Chinese' in prompt
