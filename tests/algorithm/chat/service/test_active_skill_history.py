from lazymind.chat.engine.tools.skill_listing import compose_prompt_skills


def test_active_skill_carries_to_next_turn_from_openai_tool_call():
    history = [{
        'tool_calls': [{
            'function': {
                'name': 'get_skill',
                'arguments': '{"name":"vocabulary-learning"}',
            },
        }],
    }]

    assert compose_prompt_skills(
        [], ['research/deep-research', 'vocabulary/vocabulary-learning'], history,
    )[0] == ['vocabulary/vocabulary-learning']


def test_history_does_not_reenable_denied_or_ambiguous_skill_names():
    history = [{'tool_calls': [{'name': 'get_skill', 'arguments': {'name': 'paper'}}]}]
    assert compose_prompt_skills([], ['internal/paper', 'external/paper'], history)[0] == []
    assert compose_prompt_skills([], ['external/paper'], history, excluded=['external/paper'])[0] == []


def test_active_skill_carries_to_next_turn_from_flat_tool_call():
    history = [{
        'tool_calls': [{
            'name': 'get_skill',
            'arguments': {'name': 'vocabulary/vocabulary-learning'},
        }],
    }]

    assert compose_prompt_skills(
        [], ['vocabulary/vocabulary-learning'], history,
    )[0] == ['vocabulary/vocabulary-learning']
