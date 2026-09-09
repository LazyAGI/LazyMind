from lazymind.chat.engine.prompts.system_prompt import (
    build_standard_prompt_bundle,
    build_system_prompt,
)


def test_response_language_policy_follows_user_language_not_tool_results():
    bundle = build_standard_prompt_bundle(False, environment_context={'locale': 'en-US'})

    assert '# Response language' in bundle.system_prompt
    assert 'Reply in the language the user is currently using.' in bundle.system_prompt
    assert 'Do not switch languages merely because tool names, tool results' in bundle.system_prompt
    assert 'An explicit instruction about the reply language takes priority.' in bundle.system_prompt
    assert 'writing an email in English or translating into English' in bundle.system_prompt
    assert 'Selected response language' not in bundle.system_prompt
    assert 'Selected response language' not in bundle.current_input
    assert 'Session default response language' not in bundle.system_prompt
    assert 'UI locale: en-US' in bundle.system_prompt


def test_response_language_policy_defaults_include_product_locale():
    bundle = build_standard_prompt_bundle(False)

    assert 'UI locale: zh-CN' in bundle.system_prompt
    assert 'Selected response language' not in bundle.current_input


def test_response_language_policy_covers_entire_tool_call_chain():
    prompt = build_system_prompt(True)

    assert 'status sentences before tool calls' in prompt
    assert 'clarifying questions' in prompt
    assert 'progress updates' in prompt
    assert 'the final answer' in prompt
    assert 'Do not switch languages merely because tool names, tool results' in prompt


def test_profile_languages_are_not_treated_as_selected_reply_language():
    profile = (
        '---\n'
        'schema_version: 1\n'
        'locale:\n'
        '  languages: ["zh-CN"]\n'
        '---\n'
    )
    bundle = build_standard_prompt_bundle(
        False,
        current_query='Explain the result briefly.',
        profile=profile,
        environment_context={'locale': 'en-US'},
    )

    assert 'Selected response language' not in bundle.current_input
    assert 'profile locale.languages' not in bundle.current_input
    assert 'profile locale.languages' not in bundle.system_prompt
    assert 'languages: ["zh-CN"]' in bundle.system_prompt


def test_system_prompt_injects_soul_profile_preference():
    prompt = build_system_prompt(
        False,
        soul='---\nschema_version: 1\nidentity:\n  name: "LazyMind"\n---\n',
        profile='---\nschema_version: 1\nidentity:\n  preferred_name: "Alice"\n---\n',
        preference=(
            '---\nschema_version: 1\nupdated_at: 2026-07-20\n---\n'
            '# Preference Index\n'
            '- name: pref.response.detail\n'
            '  summary: Prefer concise answers.\n'
            '  ref: references/response.md\n'
        ),
    )

    assert '## Agent Soul' in prompt
    assert '## User Profile' in prompt
    assert '## User Preference Index' in prompt
    assert 'Alice' in prompt
    assert 'pref.response.detail' in prompt
    assert '`read_memory_reference`' in prompt
    assert '## Agent Working Memory' not in prompt
    assert 'agent_persona' not in prompt


def test_same_calendar_day_keeps_stable_environment_system_prefix():
    morning = build_system_prompt(
        False,
        environment_context={
            'locale': 'zh-CN',
            'time': {'now': '2026-05-11T01:15:30.000Z', 'timezone': 'Asia/Shanghai'},
        },
    )
    evening = build_system_prompt(
        False,
        environment_context={
            'locale': 'zh-CN',
            'time': {'now': '2026-05-11T15:48:00.000Z', 'timezone': 'Asia/Shanghai'},
        },
    )

    assert morning == evening
    assert 'Current user date: 2026-05-11 (Asia/Shanghai)' in morning
    assert '19:48:00' not in morning
    assert '09:15:30' not in morning


def test_precise_current_time_lives_in_runtime_context():
    morning = build_standard_prompt_bundle(
        False,
        environment_context={
            'locale': 'zh-CN',
            'time': {'now': '2026-05-11T01:15:30.000Z', 'timezone': 'Asia/Shanghai'},
        },
    )
    evening = build_standard_prompt_bundle(
        False,
        environment_context={
            'locale': 'zh-CN',
            'time': {'now': '2026-05-11T15:48:00.000Z', 'timezone': 'Asia/Shanghai'},
        },
    )

    assert morning.system_prompt == evening.system_prompt
    assert 'Current user time: 09:15:30 (Asia/Shanghai)' in morning.current_input
    assert 'Current user time: 23:48:00 (Asia/Shanghai)' in evening.current_input
    assert 'Current user time:' not in morning.system_prompt


def test_unparseable_time_is_omitted_from_system_and_kept_in_runtime():
    bundle = build_standard_prompt_bundle(
        False,
        environment_context={
            'locale': 'zh-CN',
            'time': {'now': 'Monday morning', 'timezone': 'Asia/Shanghai'},
        },
    )

    assert 'Current user date:' not in bundle.system_prompt
    assert 'Monday morning' not in bundle.system_prompt
    assert 'Current user time: Monday morning' in bundle.current_input
