from lazymind.chat.engine.prompts.system_prompt import build_system_prompt


def test_response_language_policy_uses_ui_locale_as_last_resort():
    prompt = build_system_prompt(False, environment_context={'locale': 'en-US'})

    assert '# Response language (mandatory)' in prompt
    assert '1. An explicit language preference or instruction from the user.' in prompt
    assert '2. The dominant natural language of the current user request.' in prompt
    assert "3. The dominant language of the user's recent conversation messages." in prompt
    assert '4. The default UI locale supplied below.' in prompt
    assert 'Default UI locale for this request: en-US.' in prompt
    assert 'Selected response language for this turn: English (default UI locale en-US).' in prompt


def test_response_language_policy_defaults_to_product_locale():
    prompt = build_system_prompt(False)

    assert 'Default UI locale for this request: zh-CN.' in prompt
    assert 'Selected response language for this turn: Chinese (default UI locale zh-CN).' in prompt


def test_response_language_policy_covers_entire_tool_call_chain():
    prompt = build_system_prompt(True)

    assert 'status sentences before tool calls' in prompt
    assert 'clarifying questions' in prompt
    assert 'progress updates' in prompt
    assert 'the final answer' in prompt
    assert 'Do not switch languages merely because tool names, tool results' in prompt


def test_current_request_language_beats_opposite_ui_locale():
    chinese_prompt = build_system_prompt(
        False,
        current_query='请简短解释 API rate limit 是什么。',
        environment_context={'locale': 'en-US'},
    )
    english_prompt = build_system_prompt(
        False,
        current_query='Explain why leaves look green.',
        environment_context={'locale': 'zh-CN'},
    )

    assert 'Selected response language for this turn: Chinese' in chinese_prompt
    assert 'Selected response language for this turn: English' in english_prompt


def test_explicit_switch_beats_conversation_language():
    prompt = build_system_prompt(
        False,
        current_query='Please answer this turn in English: what was the result?',
        conversation_history=[{'role': 'user', 'content': '请用中文回答之前的问题。'}],
        environment_context={'locale': 'zh-CN'},
    )

    assert 'Selected response language for this turn: English (explicit instruction' in prompt


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
        prompt = build_system_prompt(
            False,
            current_query=query,
            environment_context={'locale': 'zh-CN' if expected_language == 'English' else 'en-US'},
        )

        assert (
            f'Selected response language for this turn: {expected_language} '
            '(explicit instruction in the current request)' in prompt
        )


def test_dominant_language_detection_only_samples_first_2000_characters():
    prompt = build_system_prompt(
        False,
        current_query='?' * 2000 + ' This English text is outside the detection sample.',
        environment_context={'locale': 'zh-CN'},
    )

    assert 'Selected response language for this turn: Chinese (default UI locale zh-CN).' in prompt


def test_recent_user_language_beats_ui_locale_for_ambiguous_follow_up():
    prompt = build_system_prompt(
        False,
        current_query='👍',
        conversation_history=[{'role': 'user', 'content': '请介绍一下这个功能。'}],
        environment_context={'locale': 'en-US'},
    )

    assert 'Selected response language for this turn: Chinese' in prompt


def test_saved_language_preference_beats_current_request_language():
    prompt = build_system_prompt(
        False,
        current_query='Explain the result briefly.',
        user_preference='首选语言：中文',
        environment_context={'locale': 'en-US'},
    )

    assert 'Selected response language for this turn: Chinese (explicit saved user preference)' in prompt
