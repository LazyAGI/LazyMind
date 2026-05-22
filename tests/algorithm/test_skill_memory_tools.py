from chat.tools import memory as memory_mod
from chat.tools import skill_manager as skill_manager_mod
from chat.tools.skill_manager import Suggestion


def test_core_api_endpoint_uses_internal_core_base_url():
    assert (
        memory_mod._core_api_endpoint(
            '/memory/suggestion',
            {'core_api_url': 'http://core:8000'},
        )
        == 'http://core:8000/memory/suggestion'
    )


def test_memory_submits_core_api_suggestion_paths(monkeypatch):
    calls = []

    def fake_post_core_api(path, payload):
        calls.append((path, payload))
        return {'persisted': 'core_api', 'url': f'http://core{path}'}

    monkeypatch.setattr(
        memory_mod,
        '_agentic_config',
        lambda: {'session_id': 'sid-1', 'core_api_url': 'http://10.119.24.129:9090'},
    )
    monkeypatch.setattr(memory_mod, '_post_core_api', fake_post_core_api)

    suggestions = [
        {
            'title': 'Keep replies concise',
            'content': 'The user consistently prefers concise answers.',
            'reason': 'Observed across the session.',
        }
    ]

    memory_result = memory_mod.memory('memory', suggestions)
    user_result = memory_mod.memory('user', suggestions)

    assert memory_result['success'] is True
    assert user_result['success'] is True
    assert calls == [
        ('/memory/suggestion', {'session_id': 'sid-1', 'suggestions': suggestions}),
        ('/user_preference/suggestion', {'session_id': 'sid-1', 'suggestions': suggestions}),
    ]


def test_memory_requires_session_id(monkeypatch):
    monkeypatch.setattr(memory_mod, '_agentic_config', lambda: {})
    monkeypatch.setattr(memory_mod, '_session_id', lambda _config: '')

    result = memory_mod.memory(
        'memory',
        [{'title': 'Remember this', 'content': 'Store as a durable suggestion.'}],
    )

    assert result == {
        'success': False,
        'reason': "'session_id' is required in agentic_config.",
    }


def test_memory_rejects_too_many_suggestions(monkeypatch):
    monkeypatch.setattr(memory_mod, '_agentic_config', lambda: {'session_id': 'sid-1'})

    result = memory_mod.memory(
        'memory',
        [{'title': f'item-{i}', 'content': 'x'} for i in range(6)],
    )

    assert result == {
        'success': False,
        'reason': 'At most 5 suggestions are allowed per call; got 6.',
    }


def test_skill_manage_create_modify_remove_use_core_api_paths(monkeypatch):
    calls = []

    def fake_post_core_api(path, payload):
        calls.append((path, payload))
        return {'persisted': 'core_api', 'url': f'http://core{path}'}

    monkeypatch.setattr(
        skill_manager_mod,
        '_agentic_config',
        lambda: {'session_id': 'sid-1', 'skill_fs_url': 'file:///tmp/skills'},
    )
    monkeypatch.setattr(skill_manager_mod, '_post_core_api', fake_post_core_api)
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

    create_result = skill_manager_mod.skill_manage(
        'new_skill',
        'create',
        category='drafts',
        content=content,
    )
    modify_result = skill_manager_mod.skill_manage(
        'existing',
        'modify',
        category='writing',
        suggestions=[suggestion],
    )
    remove_result = skill_manager_mod.skill_manage('existing', 'remove', category='writing')

    assert create_result['success'] is True
    assert modify_result['success'] is True
    assert remove_result['success'] is True
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


def test_skill_manage_rejects_missing_skill_without_post(monkeypatch):
    calls = []

    monkeypatch.setattr(
        skill_manager_mod,
        '_agentic_config',
        lambda: {'session_id': 'sid-1', 'skill_fs_url': 'file:///tmp/skills'},
    )
    monkeypatch.setattr(skill_manager_mod, '_post_core_api', lambda path, payload: calls.append((path, payload)))
    monkeypatch.setattr(skill_manager_mod, 'list_all_skill_entries', lambda _base_dir: {})

    result = skill_manager_mod.skill_manage(
        'missing',
        'modify',
        category='writing',
        suggestions=[{'title': 'Update instructions', 'content': 'Tighten the wording.'}],
    )

    assert result == {
        'success': False,
        'reason': "Skill 'missing' does not exist in category 'writing'; use action='create' to add a new skill.",
    }
    assert calls == []


def test_skill_manage_rejects_writes_to_non_remote_skills(monkeypatch):
    monkeypatch.setattr(
        skill_manager_mod,
        '_agentic_config',
        lambda: {'session_id': 'sid-1', 'skill_fs_url': 'remote://skills,.agentic/skills'},
    )
    monkeypatch.setattr(
        skill_manager_mod,
        'list_all_skill_entries',
        lambda _base_dir: {
            'writing/builtin': {
                'name': 'builtin',
                'category': 'writing',
                'path': '.agentic/skills/writing/builtin',
                'source': 'file',
            }
        },
    )

    modify_result = skill_manager_mod.skill_manage(
        'builtin',
        'modify',
        category='writing',
        suggestions=[{'title': 'Update instructions', 'content': 'Tighten the wording.'}],
    )
    remove_result = skill_manager_mod.skill_manage('builtin', 'remove', category='writing')

    assert modify_result == {
        'success': False,
        'reason': "Skill 'builtin' in category 'writing' has read-only source 'file'; skill_manage can only modify remote skills.",
    }
    assert remove_result == {
        'success': False,
        'reason': "Skill 'builtin' in category 'writing' has read-only source 'file'; skill_manage can only remove remote skills.",
    }
