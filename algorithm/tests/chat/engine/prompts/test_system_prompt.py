from lazymind.chat.engine.prompts.system_prompt import (
    build_standard_prompt_bundle,
    build_system_prompt,
)


def test_response_language_policy_uses_ui_locale_as_session_default():
    bundle = build_standard_prompt_bundle(False, environment_context={'locale': 'en-US'})

    assert '# Response language (mandatory)' in bundle.system_prompt
    assert '1. An explicit language preference or instruction from the user.' in bundle.system_prompt
    assert "2. The user's saved language preference." in bundle.system_prompt
    assert '3. The dominant natural language of the current user request.' in bundle.system_prompt
    assert "4. The dominant language of the user's recent conversation messages." in bundle.system_prompt
    assert '5. The session default language from the UI locale supplied below.' in bundle.system_prompt
    assert 'Default UI locale for this conversation: en-US.' in bundle.system_prompt
    assert 'Session default response language: English.' in bundle.system_prompt
    assert 'Selected response language for this turn' not in bundle.system_prompt
    assert 'Selected response language for this turn: English (default UI locale en-US).' in bundle.current_input


def test_response_language_policy_defaults_to_product_locale():
    bundle = build_standard_prompt_bundle(False)

    assert 'Default UI locale for this conversation: zh-CN.' in bundle.system_prompt
    assert 'Session default response language: Chinese.' in bundle.system_prompt
    assert 'Selected response language for this turn: Chinese (default UI locale zh-CN).' in bundle.current_input


def test_response_language_policy_covers_entire_tool_call_chain():
    prompt = build_system_prompt(True)

    assert 'status sentences before tool calls' in prompt
    assert 'clarifying questions' in prompt
    assert 'progress updates' in prompt
    assert 'the final answer' in prompt
    assert 'Do not switch languages merely because tool names, tool results' in prompt


def test_current_request_language_beats_opposite_ui_locale():
    chinese_bundle = build_standard_prompt_bundle(
        False,
        current_query='请简短解释 API rate limit 是什么。',
        environment_context={'locale': 'en-US'},
    )
    english_bundle = build_standard_prompt_bundle(
        False,
        current_query='Explain why leaves look green.',
        environment_context={'locale': 'zh-CN'},
    )

    assert 'Session default response language: English.' in chinese_bundle.system_prompt
    assert 'Session default response language: Chinese.' in english_bundle.system_prompt
    assert 'Selected response language for this turn: Chinese' in chinese_bundle.current_input
    assert 'Selected response language for this turn: English' in english_bundle.current_input
    assert 'Selected response language for this turn' not in chinese_bundle.system_prompt
    assert 'Selected response language for this turn' not in english_bundle.system_prompt


def test_explicit_switch_beats_conversation_language():
    bundle = build_standard_prompt_bundle(
        False,
        current_query='Please answer this turn in English: what was the result?',
        conversation_history=[{'role': 'user', 'content': '请用中文回答之前的问题。'}],
        environment_context={'locale': 'zh-CN'},
    )

    assert 'Selected response language for this turn: English (explicit instruction' in bundle.current_input
    assert 'Session default response language: Chinese.' in bundle.system_prompt


def test_common_explicit_language_phrasings_are_recognized():
    cases = (
        ('use English', 'English'),
        ('in English', 'English'),
        ('English please', 'English'),
        ('请用 English 回答', 'English'),
        ('use Mandarin', 'Chinese'),
        ('Mandarin please', 'Chinese'),
        ('请用 Chinese 回答', 'Chinese'),
    )

    for query, expected_language in cases:
        bundle = build_standard_prompt_bundle(
            False,
            current_query=query,
            environment_context={'locale': 'zh-CN' if expected_language == 'English' else 'en-US'},
        )

        assert (
            f'Selected response language for this turn: {expected_language} '
            '(explicit instruction in the current request)' in bundle.current_input
        )


def test_dominant_language_detection_only_samples_first_2000_characters():
    bundle = build_standard_prompt_bundle(
        False,
        current_query='?' * 2000 + ' This English text is outside the detection sample.',
        environment_context={'locale': 'zh-CN'},
    )

    assert 'Selected response language for this turn: Chinese (default UI locale zh-CN).' in bundle.current_input


def test_recent_user_language_beats_ui_locale_for_ambiguous_follow_up():
    bundle = build_standard_prompt_bundle(
        False,
        current_query='👍',
        conversation_history=[{'role': 'user', 'content': '请介绍一下这个功能。'}],
        environment_context={'locale': 'en-US'},
    )

    assert 'Selected response language for this turn: Chinese' in bundle.current_input
    assert 'Session default response language: English.' in bundle.system_prompt


def test_saved_language_preference_beats_current_request_language():
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

    assert (
        'Selected response language for this turn: Chinese (profile locale.languages)'
        in bundle.current_input
    )
    assert 'Selected response language for this turn' not in bundle.system_prompt


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
    assert 'Current user time: 2026-05-11 09:15:30 (Asia/Shanghai)' in morning.current_input
    assert 'Current user time: 2026-05-11 23:48:00 (Asia/Shanghai)' in evening.current_input
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
