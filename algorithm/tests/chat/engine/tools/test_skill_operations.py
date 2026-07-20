from __future__ import annotations

import pytest

from lazymind.chat.engine.tools.infra.skill_operations import (
    create_skill_file,
    delete_skill_file,
    edit_skill_file,
    fuzzy_find_and_replace,
)
from lazymind.chat.engine.tools.skill_editor import SkillManagementToolkit
from lazymind.common.skill_paths import normalize_skill_package_path, relative_to_package
from lazymind.common.skill_remote_store import SkillRemoteStore


@pytest.mark.parametrize('path', ['/x', '../x', 'refs/../x', 'refs//x', r'refs\x', ''])
def test_normalize_skill_package_path_rejects_unsafe_paths(path):
    with pytest.raises(ValueError):
        normalize_skill_package_path(path)


@pytest.mark.parametrize(
    ('package_dir', 'path'),
    [
        ('remote://skills/writing/example', 'remote://skills/writing/example/references/doc.md'),
        ('skills/writing/example', 'skills/writing/example/references/doc.md'),
    ],
)
def test_relative_to_package_preserves_nested_paths(package_dir, path):
    assert relative_to_package(package_dir, path) == 'references/doc.md'


def test_skill_file_operations_create_and_delete_supporting_files():
    existing_content = (
        '---\n'
        'name: existing\n'
        'category: writing\n'
        'description: Existing skill.\n'
        '---\n'
        'Use this skill for tests.\n'
    )
    current_files = {'SKILL.md': existing_content, 'references/old.md': 'old\n'}

    create_result = create_skill_file(
        current_files,
        'existing',
        'scripts/check.py',
        'print("ok")\n',
    )
    delete_result = delete_skill_file(current_files, 'existing', 'references/old.md')

    assert create_result['status'] == 'created'
    assert create_result['touched_files'] == ['scripts/check.py']
    assert create_result['files'] == {
        'SKILL.md': existing_content,
        'references/old.md': 'old\n',
        'scripts/check.py': 'print("ok")\n',
    }
    assert delete_result['status'] == 'deleted'
    assert delete_result['touched_files'] == ['references/old.md']
    assert delete_result['files'] == {'SKILL.md': existing_content}


def test_edit_skill_file_rejects_skill_identity_change():
    existing_content = (
        '---\n'
        'name: existing\n'
        'category: writing\n'
        'description: Existing skill.\n'
        '---\n'
        'Use this skill for tests.\n'
    )
    renamed_content = existing_content.replace('name: existing', 'name: renamed')

    with pytest.raises(ValueError, match='SKILL.md frontmatter name cannot be changed'):
        edit_skill_file({'SKILL.md': existing_content}, 'existing', 'SKILL.md', renamed_content)


def test_fuzzy_find_and_replace_exact_match():
    edited, count, strategy, error = fuzzy_find_and_replace('alpha beta\n', 'beta', 'gamma')

    assert error is None
    assert count == 1
    assert strategy == 'exact'
    assert edited == 'alpha gamma\n'


def test_fuzzy_find_and_replace_requires_unique_match_unless_replace_all():
    unchanged, count, strategy, error = fuzzy_find_and_replace('old old', 'old', 'new')
    edited, replace_all_count, replace_all_strategy, replace_all_error = fuzzy_find_and_replace(
        'old old',
        'old',
        'new',
        replace_all=True,
    )

    assert unchanged == 'old old'
    assert count == 0
    assert strategy is None
    assert error == 'Found 2 matches for old_text. Provide more context to make it unique, or use replace_all=True.'
    assert edited == 'new new'
    assert replace_all_count == 2
    assert replace_all_strategy == 'exact'
    assert replace_all_error is None


def test_skill_remote_store_rejects_mismatched_document_before_remote_fs_write():
    class RecordingFS:
        def __init__(self):
            self.calls = []

        def exists(self, path):
            self.calls.append(('exists', path))
            return False

        def mkdir(self, path, create_parents=True):
            self.calls.append(('mkdir', path))

        def write(self, path, content):
            self.calls.append(('write', path))

    fs = RecordingFS()
    store = SkillRemoteStore(fs=fs)
    content = '---\nname: different\ndescription: Example.\n---\nBody.\n'

    with pytest.raises(ValueError, match='must match expected name'):
        store.create('internal', 'expected', content)

    assert fs.calls == []


def test_create_skill_uses_internal_storage_even_when_document_category_differs():
    class RecordingStore:
        def __init__(self):
            self.calls = []

        def create(self, category, name, content):
            self.calls.append((category, name, content))

    content = (
        '---\n'
        'name: example\n'
        'description: Example.\n'
        'category: external\n'
        '---\n'
        'Body.\n'
    )
    store = RecordingStore()
    toolkit = SkillManagementToolkit(store=store, installer=object())

    result = toolkit.create_skill('example', content=content)

    assert result['success'] is True
    assert store.calls == [('internal', 'example', content)]


def test_create_skill_rejects_invalid_document_before_store_write():
    class RecordingStore:
        def __init__(self):
            self.calls = []

        def create(self, category, name, content):
            self.calls.append((category, name, content))

    store = RecordingStore()
    toolkit = SkillManagementToolkit(store=store, installer=object())

    result = toolkit.create_skill(
        'example',
        content='---\nname: example\ndescription: 123\n---\nBody.\n',
    )

    assert result['success'] is False
    assert "field 'description' must be a string" in result['error']['reason']
    assert store.calls == []
