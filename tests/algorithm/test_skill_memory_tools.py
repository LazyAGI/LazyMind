import importlib

memory_mod = importlib.import_module('lazymind.chat.engine.tools.memory_editor')
memory_reader_mod = importlib.import_module('lazymind.chat.engine.tools.memory_reader')
skill_editor_mod = importlib.import_module('lazymind.chat.engine.tools.skill_editor')


class FakeMemoryStore:
    def __init__(self, contents=None, read_error=None, write_error=None):
        self.contents = dict(contents or {})
        self.read_error = read_error
        self.write_error = write_error
        self.writes = []

    def read(self, target):
        if self.read_error:
            raise self.read_error
        return self.contents[target]

    def write(self, target, content):
        if self.write_error:
            raise self.write_error
        self.writes.append((target, content))
        self.contents[target] = content


class FakeSkillStore:
    def __init__(self, packages=None):
        self.root = 'remote://skills'
        self.fs = object()
        self.packages = dict(packages or {})
        self.calls = []

    def resolve_existing_identity(self, name, category=None):
        self.calls.append(('resolve_existing_identity', name, category))
        raw_name = str(name or '').strip()
        if category:
            return {'category': category, 'name': raw_name.rsplit('/', 1)[-1]}
        if '/' in raw_name:
            resolved_category, resolved_name = raw_name.split('/', 1)
            return {'category': resolved_category, 'name': resolved_name}
        matches = [
            {'category': skill_category, 'name': skill_name}
            for skill_category, skill_name in sorted(self.packages)
            if skill_name == raw_name
        ]
        if not matches:
            return {'error': f"Skill {raw_name!r} was not found; provide category or full skill key."}
        if len(matches) > 1:
            first = f"{matches[0]['category']}/{matches[0]['name']}"
            return {'error': f"Ambiguous skill name {raw_name!r}; use the full skill key such as {first!r}."}
        return matches[0]

    def list_files(self, category, name):
        self.calls.append(('list_files', category, name))
        return dict(self.packages.get((category, name), {}))

    def replace_files(self, category, name, before, after):
        self.calls.append(('replace_files', category, name, before, after))
        self.packages[(category, name)] = dict(after)
        return {
            'written': sorted(path for path in after if before.get(path) != after.get(path)),
            'deleted': sorted(set(before) - set(after)),
        }

    def create(self, category, name, content):
        self.calls.append(('create', category, name, content))
        self.packages[(category, name)] = {'SKILL.md': content}
        return {'action': 'create'}

    def rename(self, old_category, old_name, new_category, new_name, *, skill_content):
        self.calls.append(('rename', old_category, old_name, new_category, new_name, skill_content))
        self.packages[(new_category, new_name)] = {'SKILL.md': skill_content}
        self.packages.pop((old_category, old_name), None)
        return {'action': 'rename'}

    def remove(self, category, name):
        self.calls.append(('remove', category, name))
        self.packages.pop((category, name), None)
        return {'action': 'remove'}


def test_memory_editor_operation_writes_remote_fs(monkeypatch):
    assert not hasattr(memory_mod, 'memory')
    assert not hasattr(memory_mod, 'insert_memory_review_record')

    store = FakeMemoryStore({'memory': 'old', 'user_preference': 'old'})
    monkeypatch.setattr(
        memory_mod,
        '_validate_generated_content',
        lambda memory_type, content: content,
    )
    monkeypatch.setattr(memory_mod, 'MemoryRemoteStore', lambda: store)

    memory_result = memory_mod.memory_editor(
        'memory',
        op='patch',
        old_text='old',
        new_text='new',
    )
    user_result = memory_mod.memory_editor(
        'user_preference',
        op='append',
        content='newer',
    )

    assert memory_result['success'] is True
    assert memory_result['tool'] == 'memory_editor'
    assert memory_result['result']['target'] == 'memory'
    assert memory_result['result']['status'] == 'pending_review'
    assert memory_result['result']['message'] == 'Memory changes were written to draft and are pending review.'
    assert memory_result['result']['operation_count'] == 1
    assert user_result['success'] is True
    assert user_result['tool'] == 'memory_editor'
    assert user_result['result']['target'] == 'user_preference'
    assert user_result['result']['status'] == 'pending_review'
    assert user_result['result']['message'] == 'Memory changes were written to draft and are pending review.'
    assert store.writes == [
        ('memory', 'new'),
        ('user_preference', 'old\nnewer'),
    ]


def test_memory_editor_patch_match_controls(monkeypatch):
    store = FakeMemoryStore({'memory': 'old and old'})
    monkeypatch.setattr(memory_mod, 'MemoryRemoteStore', lambda: store)
    monkeypatch.setattr(memory_mod, '_validate_generated_content', lambda memory_type, content: content)

    ambiguous = memory_mod.memory_editor(
        'memory',
        op='patch',
        old_text='old',
        new_text='new',
    )
    missing = memory_mod.memory_editor(
        'memory',
        op='patch',
        old_text='missing',
        new_text='new',
    )
    empty_old = memory_mod.memory_editor(
        'memory',
        op='patch',
        old_text='',
        new_text='new',
    )
    replace_all = memory_mod.memory_editor(
        'memory',
        op='patch',
        old_text='old',
        new_text='new',
        replace_all_matches=True,
    )

    assert ambiguous['success'] is False
    assert 'matched multiple locations' in ambiguous['error']['reason']
    assert missing['success'] is False
    assert 'could not find' in missing['error']['reason']
    assert empty_old['success'] is False
    assert "non-empty 'old_text'" in empty_old['error']['reason']
    assert replace_all['success'] is True
    assert store.writes == [('memory', 'new and new')]


def test_memory_editor_append_requires_content(monkeypatch):
    store = FakeMemoryStore({'memory': ''})
    monkeypatch.setattr(memory_mod, 'MemoryRemoteStore', lambda: store)

    empty_append = memory_mod.memory_editor('memory', op='append', content='  ')
    unknown = memory_mod.memory_editor('memory', op='replace_all', content='new')

    assert empty_append['success'] is False
    assert 'append requires non-empty content' in empty_append['error']['reason']
    assert unknown['success'] is False
    assert "expected 'patch' or 'append'" in unknown['error']['reason']
    assert store.writes == []


def test_memory_editor_remote_fs_errors_return_tool_error(monkeypatch):
    monkeypatch.setattr(
        memory_mod,
        'MemoryRemoteStore',
        lambda: FakeMemoryStore({'memory': 'old'}, read_error=RuntimeError('backend down')),
    )
    read_result = memory_mod.memory_editor(
        'memory',
        op='patch',
        old_text='old',
        new_text='new',
    )

    monkeypatch.setattr(
        memory_mod,
        'MemoryRemoteStore',
        lambda: FakeMemoryStore({'memory': 'old'}, write_error=RuntimeError('conflict')),
    )
    monkeypatch.setattr(memory_mod, '_validate_generated_content', lambda memory_type, content: content)
    blocked_write_result = memory_mod.memory_editor(
        'memory',
        op='patch',
        old_text='old',
        new_text='new',
    )

    monkeypatch.setattr(
        memory_mod,
        'MemoryRemoteStore',
        lambda: FakeMemoryStore({'memory': 'old'}, write_error=RuntimeError('backend down')),
    )
    failed_write_result = memory_mod.memory_editor(
        'memory',
        op='patch',
        old_text='old',
        new_text='new',
    )

    assert read_result['success'] is False
    assert 'Failed to read memory via RemoteFS: backend down' in read_result['error']['reason']
    assert blocked_write_result['success'] is False
    assert blocked_write_result['error']['reason'] == (
        'There are pending changes. Please ask the user to handle them before modifying.'
    )
    assert failed_write_result['success'] is False
    assert 'Failed to write memory via RemoteFS: backend down' in failed_write_result['error']['reason']


def test_read_memory_reads_remote_fs(monkeypatch):
    store = FakeMemoryStore({'memory': 'remote memory'})
    monkeypatch.setattr(memory_reader_mod, 'MemoryRemoteStore', lambda: store)

    result = memory_reader_mod.read_memory('memory')

    assert result['success'] is True
    assert result['tool'] == 'read_memory'
    assert result['result'] == {
        'target': 'memory',
        'content': 'remote memory',
        'content_length': len('remote memory'),
    }


def test_skill_editor_create_file_tools_remove_core_paths():
    existing_content = (
        '---\n'
        'name: existing\n'
        'category: writing\n'
        'description: Existing skill.\n'
        '---\n'
        'Use this skill for tests.\n'
    )
    content = (
        '---\n'
        'name: new_skill\n'
        'category: drafts\n'
        'description: A test skill.\n'
        '---\n'
        'Use this skill for tests.\n'
    )
    store = FakeSkillStore({
        ('writing', 'existing'): {
            'SKILL.md': existing_content,
            'references/old.md': 'old reference\n',
        },
    })
    tool_group = skill_editor_mod.SkillManagementToolkit(store=store)

    create_result = tool_group.create_skill(
        'new_skill',
        category='drafts',
        content=content,
    )
    patch_result = tool_group.patch_file(
        'existing',
        category='writing',
        path='SKILL.md',
        old_text='Use this skill for tests.',
        new_text='Use this skill for focused tests.',
        reason='patch skill body',
    )
    file_create_result = tool_group.create_file(
        'writing/existing',
        category='writing',
        path='scripts/check.py',
        content='print("ok")\n',
        reason='new helper script',
    )
    delete_result = tool_group.delete_file(
        'existing',
        category='writing',
        path='references/old.md',
        reason='remove stale reference',
    )
    remove_result = tool_group.remove_skill('existing', category='writing')

    assert create_result['success'] is True
    assert create_result['tool'] == 'create_skill'
    assert patch_result['success'] is True
    assert patch_result['tool'] == 'patch_file'
    assert file_create_result['success'] is True
    assert file_create_result['tool'] == 'create_file'
    assert delete_result['success'] is True
    assert delete_result['tool'] == 'delete_file'
    assert remove_result['success'] is True
    assert remove_result['tool'] == 'remove_skill'
    assert create_result['result'] == {
        'status': 'created',
        'message': 'Skill package change was written.',
    }
    assert patch_result['result']['status'] == 'patched'
    assert patch_result['result']['touched_files'] == ['SKILL.md']
    assert patch_result['result']['written_files'] == ['SKILL.md']
    assert patch_result['result']['deleted_files'] == []
    assert patch_result['result']['summary'] == 'patch skill body'
    assert file_create_result['result']['status'] == 'created'
    assert file_create_result['result']['written_files'] == ['scripts/check.py']
    assert file_create_result['result']['deleted_files'] == []
    assert delete_result['result']['status'] == 'deleted'
    assert delete_result['result']['written_files'] == []
    assert delete_result['result']['deleted_files'] == ['references/old.md']
    assert remove_result['result'] == {
        'status': 'removed',
        'message': 'Skill package change was written.',
    }
    assert ('create', 'drafts', 'new_skill', content) in store.calls
    assert ('remove', 'writing', 'existing') in store.calls
    replace_calls = [call for call in store.calls if call[0] == 'replace_files']
    assert replace_calls == [
        ('replace_files', 'writing', 'existing',
         {'SKILL.md': existing_content, 'references/old.md': 'old reference\n'},
         {
             'SKILL.md': existing_content.replace(
                 'Use this skill for tests.',
                 'Use this skill for focused tests.',
             ),
             'references/old.md': 'old reference\n',
         }),
        ('replace_files', 'writing', 'existing',
         {
             'SKILL.md': existing_content.replace(
                 'Use this skill for tests.',
                 'Use this skill for focused tests.',
             ),
             'references/old.md': 'old reference\n',
         },
         {
             'SKILL.md': existing_content.replace(
                 'Use this skill for tests.',
                 'Use this skill for focused tests.',
             ),
             'references/old.md': 'old reference\n',
             'scripts/check.py': 'print("ok")\n',
         }),
        ('replace_files', 'writing', 'existing',
         {
             'SKILL.md': existing_content.replace(
                 'Use this skill for tests.',
                 'Use this skill for focused tests.',
             ),
             'references/old.md': 'old reference\n',
             'scripts/check.py': 'print("ok")\n',
         },
         {
             'SKILL.md': existing_content.replace(
                 'Use this skill for tests.',
                 'Use this skill for focused tests.',
             ),
             'scripts/check.py': 'print("ok")\n',
         }),
    ]


def test_skill_editor_renames_package():
    existing_content = (
        '---\n'
        'name: existing\n'
        'category: writing\n'
        'description: Existing skill.\n'
        '---\n'
        'Use this skill for tests.\n'
    )
    store = FakeSkillStore({
        ('writing', 'existing'): {'SKILL.md': existing_content, 'references/doc.md': 'doc\n'},
    })

    result = skill_editor_mod.SkillManagementToolkit(store=store).rename_skill(
        'existing',
        category='writing',
        new_name='renamed',
        new_category='drafts',
    )

    assert result['success'] is True
    assert result['result']['status'] == 'renamed'
    assert result['result']['old'] == {'category': 'writing', 'name': 'existing'}
    assert result['result']['new'] == {'category': 'drafts', 'name': 'renamed'}
    rename_calls = [call for call in store.calls if call[0] == 'rename']
    assert rename_calls[0][1:5] == ('writing', 'existing', 'drafts', 'renamed')
    assert 'name: renamed' in rename_calls[0][5]
    assert 'category: drafts' in rename_calls[0][5]
