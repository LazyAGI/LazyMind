import importlib

memory_mod = importlib.import_module('lazymind.chat.engine.tools.memory_editor')
skill_editor_mod = importlib.import_module('lazymind.chat.engine.tools.skill_editor')


def test_memory_editor_operations_write_memory_review(monkeypatch):
    assert not hasattr(memory_mod, 'memory')

    class FakeUnprocessableContentError(ValueError):
        pass

    records = []

    def fake_insert_memory_review_record(**kwargs):
        records.append(kwargs)
        return {'id': 'review-1', 'review_status': 'pending'}

    monkeypatch.setattr(memory_mod, 'UnprocessableContentError', FakeUnprocessableContentError)
    monkeypatch.setattr(
        memory_mod,
        '_validate_generated_content',
        lambda memory_type, content: content,
    )
    monkeypatch.setattr(
        memory_mod,
        '_apply_memory_edit_operations',
        lambda current, payload: current.replace('old', payload['operations'][0]['new']),
    )
    monkeypatch.setattr(
        memory_mod,
        '_apply_user_preference_edit_operations',
        lambda current, payload: current.replace('old', payload['operations'][0]['new']),
    )
    monkeypatch.setattr(memory_mod, 'insert_memory_review_record', fake_insert_memory_review_record)
    monkeypatch.setattr(
        memory_mod.lazyllm,
        'globals',
        {'agentic_config': {'user_id': 'user-1', 'memory': 'old', 'user_preference': 'old'}},
    )

    memory_result = memory_mod.memory_editor(
        'memory',
        [{'op': 'replace_text', 'old': 'old', 'new': 'new'}],
    )
    user_result = memory_mod.memory_editor(
        'user_preference',
        [{'op': 'replace_text', 'old': 'old', 'new': 'new'}],
    )

    assert memory_result['success'] is True
    assert memory_result['tool'] == 'memory_editor'
    assert memory_result['result']['target'] == 'memory'
    assert memory_result['result']['status'] == 'pending_review'
    assert memory_result['result']['message'] == '记忆修改已提交，等待审核'
    assert user_result['success'] is True
    assert user_result['tool'] == 'memory_editor'
    assert user_result['result']['target'] == 'user_preference'
    assert user_result['result']['status'] == 'pending_review'
    assert user_result['result']['message'] == '记忆修改已提交，等待审核'
    assert records == [
        {
            'target': 'memory',
            'user_id': 'user-1',
            'session_id': '',
            'source_content': 'old',
            'content': 'new',
            'operations': [{'op': 'replace_text', 'old': 'old', 'new': 'new'}],
        },
        {
            'target': 'user_preference',
            'user_id': 'user-1',
            'session_id': '',
            'source_content': 'old',
            'content': 'new',
            'operations': [{'op': 'replace_text', 'old': 'old', 'new': 'new'}],
        },
    ]


def test_skill_editor_create_modify_remove_core_paths(monkeypatch):
    records = []
    create_calls = []
    remove_calls = []
    pending_checks = []

    def fake_insert_skill_review_result(**kwargs):
        records.append(kwargs)
        return {'id': f'review-{len(records)}', 'review_status': 'pending'}

    def fake_apply_skill_edit_operations(current, operations):
        return (
            current
            .replace('name: existing', 'name: renamed')
            .replace('category: writing', 'category: drafts')
            .replace('Use this skill for tests.', 'Use this skill for focused tests.'),
            [dict(op) for op in operations],
        )

    monkeypatch.setattr(
        skill_editor_mod.lazyllm,
        'globals',
        {'agentic_config': {'user_id': 'user-1', 'session_id': 'session-1'}},
    )
    monkeypatch.setattr(skill_editor_mod, 'insert_skill_review_result', fake_insert_skill_review_result)
    monkeypatch.setattr(skill_editor_mod, 'create_remote_skill', lambda *args: create_calls.append(args))
    monkeypatch.setattr(skill_editor_mod, 'remove_remote_skill', lambda *args: remove_calls.append(args))
    def fake_find_pending_skill_review(category, name, user_id):
        pending_checks.append((category, name, user_id))
        return None

    monkeypatch.setattr(skill_editor_mod, 'find_pending_skill_review', fake_find_pending_skill_review)
    monkeypatch.setattr(skill_editor_mod, 'apply_skill_edit_operations', fake_apply_skill_edit_operations)

    existing_content = (
        '---\n'
        'name: existing\n'
        'category: writing\n'
        'description: Existing skill.\n'
        '---\n'
        'Use this skill for tests.\n'
    )
    monkeypatch.setattr(
        skill_editor_mod,
        'list_all_skill_entries',
        lambda _base_dir: {
            'writing/existing': {
                'name': 'existing',
                'category': 'writing',
                'path': '/tmp/skills/writing/existing',
                'source': 'remote',
                'content': existing_content,
            }
        },
    )

    content = (
        '---\n'
        'name: new_skill\n'
        'category: drafts\n'
        'description: A test skill.\n'
        '---\n'
        'Use this skill for tests.\n'
    )
    create_result = skill_editor_mod.skill_editor(
        'new_skill',
        'create',
        category='drafts',
        content=content,
    )
    modify_result = skill_editor_mod.skill_editor(
        'existing',
        'modify',
        category='writing',
        operations=[
            {
                'op': 'replace_text',
                'old': 'Use this skill for tests.',
                'new': 'Use this skill for focused tests.',
            }
        ],
    )
    remove_result = skill_editor_mod.skill_editor('existing', 'remove', category='writing')

    assert create_result['success'] is True
    assert create_result['tool'] == 'skill_editor'
    assert modify_result['success'] is True
    assert modify_result['tool'] == 'skill_editor'
    assert remove_result['success'] is True
    assert remove_result['tool'] == 'skill_editor'
    assert create_result['result'] == {
        'status': 'created',
        'message': 'Skill was created and is now active.',
    }
    assert modify_result['result'] == {
        'status': 'pending_review',
        'message': 'Skill changes were submitted and are pending review.',
    }
    assert remove_result['result'] == {
        'status': 'removed',
        'message': 'Skill was removed and is no longer active.',
    }
    assert create_calls == [('drafts', 'new_skill', content)]
    assert remove_calls == [('writing', 'existing')]
    assert pending_checks == [
        ('drafts', 'new_skill', 'user-1'),
        ('drafts', 'renamed', 'user-1'),
        ('writing', 'existing', 'user-1'),
    ]
    assert records == [
        {
            'category': 'writing',
            'skill_name': 'existing',
            'review_type': 'patch',
            'skill_content': (
                existing_content
                .replace('name: existing', 'name: renamed')
                .replace('category: writing', 'category: drafts')
                .replace('Use this skill for tests.', 'Use this skill for focused tests.')
            ),
            'user_id': 'user-1',
            'requestid': 'session-1',
            'summary': 'skill_editor operations: 1',
        },
    ]


def test_skill_editor_rejects_missing_skill_without_write(monkeypatch):
    calls = []

    monkeypatch.setattr(skill_editor_mod.lazyllm, 'globals', {'agentic_config': {}})
    monkeypatch.setattr(skill_editor_mod, 'insert_skill_review_result', lambda **kwargs: calls.append(kwargs))
    monkeypatch.setattr(skill_editor_mod, 'list_all_skill_entries', lambda _base_dir: {})

    result = skill_editor_mod.skill_editor(
        'missing',
        'modify',
        category='writing',
        operations=[{'op': 'replace_text', 'old': 'old', 'new': 'new'}],
    )

    assert result == {
        'success': False,
        'tool': 'skill_editor',
        'error': {
            'reason': "Skill 'missing' does not exist in category 'writing'; use action='create' to add a new skill.",
        },
    }
    assert calls == []


def test_skill_editor_blocks_modify_and_remove_when_pending_review_exists(monkeypatch):
    monkeypatch.setattr(skill_editor_mod.lazyllm, 'globals', {'agentic_config': {'user_id': 'user-1'}})
    monkeypatch.setattr(
        skill_editor_mod,
        'list_all_skill_entries',
        lambda _base_dir: {
            'writing/existing': {
                'name': 'existing',
                'category': 'writing',
                'path': '/tmp/skills/writing/existing',
                'source': 'remote',
                'content': (
                    '---\n'
                    'name: existing\n'
                    'category: writing\n'
                    'description: Existing skill.\n'
                    '---\n'
                    'Use this skill for tests.\n'
                ),
            }
        },
    )
    monkeypatch.setattr(
        skill_editor_mod,
        'find_pending_skill_review',
        lambda category, name, user_id: {'id': 'pending-1', 'category': category, 'skill_name': name},
    )
    monkeypatch.setattr(
        skill_editor_mod,
        'apply_skill_edit_operations',
        lambda current, operations: (
            current.replace('Use this skill for tests.', 'Use this skill for focused tests.'),
            [dict(op) for op in operations],
        ),
    )

    modify_result = skill_editor_mod.skill_editor(
        'existing',
        'modify',
        category='writing',
        operations=[{'op': 'replace_text', 'old': 'old', 'new': 'new'}],
    )
    remove_result = skill_editor_mod.skill_editor('existing', 'remove', category='writing')

    assert modify_result['success'] is False
    assert modify_result['tool'] == 'skill_editor'
    assert modify_result['error']['reason'] == (
        'There is an unresolved pending change; handle it before submitting another edit.'
    )
    assert remove_result['success'] is False
    assert remove_result['tool'] == 'skill_editor'
    assert remove_result['error']['reason'] == (
        'There is an unresolved pending change; handle it before submitting another edit.'
    )
