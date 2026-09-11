from lazymind.chat.service.chat_service import _active_skills_from_history


def test_active_skill_carries_to_next_turn_from_openai_tool_call():
    history = [{
        'tool_calls': [{
            'function': {
                'name': 'get_skill',
                'arguments': '{"name":"vocabulary-learning"}',
            },
        }],
    }]

    assert _active_skills_from_history(
        history, ['research/deep-research', 'vocabulary/vocabulary-learning'],
    ) == ['vocabulary/vocabulary-learning']


def test_active_skill_carries_to_next_turn_from_flat_tool_call():
    history = [{
        'tool_calls': [{
            'name': 'get_skill',
            'arguments': {'name': 'vocabulary/vocabulary-learning'},
        }],
    }]

    assert _active_skills_from_history(
        history, ['vocabulary/vocabulary-learning'],
    ) == ['vocabulary/vocabulary-learning']
