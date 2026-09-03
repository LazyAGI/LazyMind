from __future__ import annotations

from lazymind.chat.engine.agent_runtime import AgentRole, PromptBuilder
from lazymind.chat.engine.prompts.system_prompt import add_standard_system_sections, build_system_prompt


def test_runtime_context_uses_user_timezone_time() -> None:
    bundle = add_standard_system_sections(
        PromptBuilder.for_role(AgentRole.CHAT),
        False,
        environment_context={
            'time': {
                'now': '2026-05-11T11:48:00.000Z',
                'timezone': 'Asia/Shanghai',
            },
        },
    ).build()

    assert 'Current user time: 2026-05-11 19:48:00 (Asia/Shanghai)' in bundle.current_input
    assert 'Current user time' not in bundle.system_prompt
    assert 'Environment Context [AUTHORITATIVE]' in bundle.current_input


def test_runtime_context_falls_back_to_raw_time_when_timezone_is_invalid() -> None:
    bundle = add_standard_system_sections(
        PromptBuilder.for_role(AgentRole.CHAT),
        False,
        environment_context={
            'time': {
                'now': '2026-05-11T11:48:00.000Z',
                'timezone': 'Bad/Timezone',
            },
        },
    ).build()

    assert 'Current user time: 2026-05-11T11:48:00.000Z' in bundle.current_input


def test_time_and_task_changes_preserve_system_prefix_and_user_input() -> None:
    from lazymind.chat.engine.prompts.task_profile import resolve_task_profile

    history = [{'role': 'user', 'content': '你好'}, {'role': 'assistant', 'content': '你好！'}]
    bundles = []
    for now, query in [
        ('2026-09-03T01:00:00Z', '教我学习摄影'),
        ('2026-09-03T02:00:00Z', '分析附件里的财报'),
    ]:
        bundles.append(add_standard_system_sections(
            PromptBuilder.for_role(AgentRole.CHAT),
            True,
            environment_context={'time': {'now': now}},
            conversation_history=history,
            current_query=query,
            task_profile=resolve_task_profile(query, enable_llm_fallback=False),
            dynamic_prompt_modules=True,
        ).input(query, source='user').build())

    first, second = bundles
    assert first.system_prompt == second.system_prompt
    assert first.current_input != second.current_input
    assert '2026-09-03T02:00:00+00:00' in second.current_input
    assert '2026-09-03T01:00:00' not in second.current_input
    assert second.input_content == '分析附件里的财报'
    assert history == [{'role': 'user', 'content': '你好'}, {'role': 'assistant', 'content': '你好！'}]


def test_system_prompt_includes_cross_tool_policy_when_tools_are_active() -> None:
    prompt = build_system_prompt(True)

    assert '# Tool use policy' in prompt
    assert 'get_*Toolkit_methods' in prompt


def test_system_prompt_omits_tool_policy_without_tools() -> None:
    prompt = build_system_prompt(False)

    assert '# Tool use policy' not in prompt


def test_main_chat_prompt_describes_editable_writing_blocks() -> None:
    prompt = build_system_prompt(False)

    assert '# Editable writing blocks' in prompt
    assert '```editable' in prompt
    assert 'articles, marketing or sales copy' in prompt


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


def test_system_prompt_appends_partitioned_active_tool_contracts() -> None:
    prompt = build_system_prompt(
        True,
        tool_prompt_appendices={
            'output_contract': ['Preserve the returned citation markers.'],
            'safety': ['Confirm before deleting remote data.'],
        },
    )

    assert '## Tool-specific safety constraints' in prompt
    assert 'Confirm before deleting remote data.' in prompt
    assert '## Tool output contracts' in prompt
    assert 'Preserve the returned citation markers.' in prompt
    assert prompt.index('## Tool-specific safety constraints') < prompt.index('## Tool output contracts')


def test_tool_output_contract_keeps_detailed_image_and_citation_guards() -> None:
    from lazymind.chat.service.component.tool_registry import (
        IMAGE_MARKDOWN_OUTPUT_APPENDIX,
        RETRIEVAL_CITATION_OUTPUT_APPENDIX,
        collect_system_prompt_appendices,
    )

    prompt = build_system_prompt(
        True,
        tool_prompt_appendices=collect_system_prompt_appendices(
            [],
            extra_appendices=(
                IMAGE_MARKDOWN_OUTPUT_APPENDIX,
                RETRIEVAL_CITATION_OUTPUT_APPENDIX,
            ),
        ),
    )

    assert 'NEVER invent hosts or prefixes' in prompt
    assert 'Do not paste bare filesystem paths' in prompt
    assert 'For any used retrieval result containing `ref`' in prompt
    assert 'cite at least one result from each category' in prompt
    assert 'Never invent or rewrite refs' in prompt


def test_system_prompt_ignores_tool_appendices_when_no_tools_are_registered() -> None:
    prompt = build_system_prompt(
        False,
        tool_prompt_appendices={'output_contract': ['Must not be injected.']},
    )

    assert 'Must not be injected.' not in prompt


def test_system_prompt_explains_profile_operations_by_yaml_type() -> None:
    prompt = build_system_prompt(
        True,
        profile=(
            'personal:\n'
            '  nickname: Neo\n'
            '  interests: [AI]\n'
            '  headline: null\n'
        ),
    )

    assert 'nickname: Neo' in prompt
    assert 'A YAML string supports `set` and `clear`' in prompt
    assert 'A YAML `null` supports `set` and `clear`' in prompt
    assert 'A YAML list of strings supports `add`, `remove`, and `clear`' in prompt
    assert 'Use only existing leaf dot paths' in prompt


def test_system_prompt_uses_query_history_and_environment_not_profile_language() -> None:
    prompt = build_system_prompt(
        True,
        current_query='What changed?',
        profile='locale:\n  languages: [Chinese]\n',
        environment_context={'locale': 'zh-CN'},
    )

    assert 'Selected response language for this turn: English' in prompt
    assert 'profile locale.languages' not in prompt
