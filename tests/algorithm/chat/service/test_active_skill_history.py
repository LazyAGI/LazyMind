from lazymind.chat.engine.tools.skill_listing import (
    append_loaded_skill_invocations,
    compose_prompt_skills,
)


def test_prompt_catalog_ignores_get_skill_history():
    history = [{
        'tool_calls': [{
            'function': {
                'name': 'get_skill',
                'arguments': '{"name":"vocabulary-learning"}',
            },
        }],
    }]

    prompt, manager = compose_prompt_skills(
        [], ['research/deep-research', 'vocabulary/vocabulary-learning'],
    )
    assert prompt == []
    assert 'vocabulary/vocabulary-learning' in manager
    assert append_loaded_skill_invocations(history, []) == history


def test_history_does_not_reenable_denied_or_ambiguous_skill_names():
    assert compose_prompt_skills([], ['internal/paper', 'external/paper'])[0] == []
    assert compose_prompt_skills([], ['external/paper'], excluded=['external/paper'])[0] == []


def test_explicit_skill_invocation_enters_tool_history_once():
    loaded = [{
        'skill_key': 'vocabulary/vocabulary-learning',
        'revision_id': 'rev-1',
        'content': '# Vocabulary\nUse spaced repetition.',
    }]
    messages = append_loaded_skill_invocations([], loaded)
    assert messages[0]['tool_calls'][0]['function']['name'] == 'get_skill'
    assert 'vocabulary/vocabulary-learning' in messages[1]['content']
    assert append_loaded_skill_invocations(messages, loaded) == messages
