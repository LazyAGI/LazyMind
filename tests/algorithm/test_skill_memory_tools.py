from lazymind.chat.engine.tools import memory as memory_mod
from lazymind.chat.engine.tools import skill_manager as skill_manager_mod
from lazymind.chat.engine.tools.infra.suggestion import Suggestion


def test_memory_submits_core_api_suggestion_paths(monkeypatch):
    calls = []

    def fake_post_core_api(path, payload):
        calls.append((path, payload))
        return {'persisted': 'core_api', 'url': f'http://core{path}'}

    monkeypatch.setattr(memory_mod.lazyllm, 'globals', {'agentic_config': {'session_id': 'sid-1'}})
    monkeypatch.setattr(memory_mod, 'post_core_api', fake_post_core_api)

    suggestions = [
        {
            'title': 'Keep replies concise',
            'content': 'The user consistently prefers concise answers.',
            'reason': 'Observed across the session.',
        }
    ]

    memory_result = memory_mod.memory_editor('memory', suggestions)
    user_result = memory_mod.memory_editor('user', suggestions)

    assert memory_result['success'] is True
    assert memory_result['tool'] == 'memory_editor'
    assert memory_result['result']['target'] == 'memory'
    assert user_result['success'] is True
    assert user_result['tool'] == 'memory_editor'
    assert user_result['result']['target'] == 'user'
    assert calls == [
        ('/memory/suggestion', {'session_id': 'sid-1', 'suggestions': suggestions}),
        ('/user_preference/suggestion', {'session_id': 'sid-1', 'suggestions': suggestions}),
    ]


def test_memory_requires_session_id(monkeypatch):
    monkeypatch.setattr(memory_mod.lazyllm, 'globals', {'agentic_config': {}})

    result = memory_mod.memory_editor(
        'memory',
        [{'title': 'Remember this', 'content': 'Store as a durable suggestion.'}],
    )

    assert result == {
        'success': False,
        'tool': 'memory_editor',
        'error': {
            'reason': "'session_id' is required in agentic_config.",
        },
    }


def test_memory_rejects_too_many_suggestions(monkeypatch):
    monkeypatch.setattr(memory_mod.lazyllm, 'globals', {'agentic_config': {'session_id': 'sid-1'}})

    result = memory_mod.memory_editor(
        'memory',
        [{'title': f'item-{i}', 'content': 'x'} for i in range(6)],
    )

    assert result == {
        'success': False,
        'tool': 'memory_editor',
        'error': {
            'reason': 'At most 5 suggestions are allowed per call; got 6.',
        },
    }


def test_skill_editor_create_modify_remove_use_core_api_paths(monkeypatch):
    calls = []

    def fake_post_core_api(path, payload):
        calls.append((path, payload))
        return {'persisted': 'core_api', 'url': f'http://core{path}'}

    monkeypatch.setattr(skill_manager_mod.lazyllm, 'globals', {'agentic_config': {'session_id': 'sid-1'}})
    monkeypatch.setattr(skill_manager_mod, 'post_core_api', fake_post_core_api)
    monkeypatch.setattr(
        skill_manager_mod,
        'list_all_skill_entries',
        lambda _base_dir: {
            'writing/existing': {
                'name': 'existing',
                'category': 'writing',
                'path': '/tmp/skills/writing/existing',
                'source': 'remote',
            }
        },
    )

    content = (
        '---\n'
        'name: new_skill\n'
        'description: A test skill.\n'
        '---\n'
        'Use this skill for tests.\n'
    )
    suggestion = Suggestion(title='Update instructions', content='Tighten the wording.')

    create_result = skill_manager_mod.skill_editor(
        'new_skill',
        'create',
        category='drafts',
        content=content,
    )
    modify_result = skill_manager_mod.skill_editor(
        'existing',
        'modify',
        category='writing',
        suggestions=[suggestion],
    )
    remove_result = skill_manager_mod.skill_editor('existing', 'remove', category='writing')

    assert create_result['success'] is True
    assert create_result['tool'] == 'skill_editor'
    assert modify_result['success'] is True
    assert modify_result['tool'] == 'skill_editor'
    assert remove_result['success'] is True
    assert remove_result['tool'] == 'skill_editor'
    assert calls == [
        (
            '/skill/create',
            {
                'session_id': 'sid-1',
                'category': 'drafts',
                'skill_name': 'new_skill',
                'content': content,
            },
        ),
        (
            '/skill/suggestion',
            {
                'session_id': 'sid-1',
                'skill_name': 'existing',
                'category': 'writing',
                'suggestions': [{'title': 'Update instructions', 'content': 'Tighten the wording.'}],
            },
        ),
        (
            '/skill/remove',
            {'session_id': 'sid-1', 'skill_name': 'existing', 'category': 'writing', 'reason': ''},
        ),
    ]


def test_skill_editor_rejects_missing_skill_without_post(monkeypatch):
    calls = []

    monkeypatch.setattr(skill_manager_mod.lazyllm, 'globals', {'agentic_config': {'session_id': 'sid-1'}})
    monkeypatch.setattr(skill_manager_mod, 'post_core_api', lambda path, payload: calls.append((path, payload)))
    monkeypatch.setattr(skill_manager_mod, 'list_all_skill_entries', lambda _base_dir: {})

    result = skill_manager_mod.skill_editor(
        'missing',
        'modify',
        category='writing',
        suggestions=[{'title': 'Update instructions', 'content': 'Tighten the wording.'}],
    )

    assert result == {
        'success': False,
        'tool': 'skill_editor',
        'error': {
            'reason': "Skill 'missing' does not exist in category 'writing'; use action='create' to add a new skill.",
        },
    }
    assert calls == []
