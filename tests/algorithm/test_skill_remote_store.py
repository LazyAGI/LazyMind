import pytest

from lazymind.common.skill_remote_store import SkillRemoteStore
from lazymind.common.skill_storage_key import (
    parse_skill_storage_key,
    require_skill_storage_category,
)


class _RemoteFS:
    def __init__(self, *, existing=(), listings=None):
        self.calls = []
        self.existing = set(existing)
        self.listings = dict(listings or {})

    def exists(self, path):
        self.calls.append(('exists', path))
        return path in self.existing

    def ls(self, path, detail=True):
        self.calls.append(('ls', path, detail))
        return list(self.listings.get(path, []))

    def mkdir(self, path, create_parents=True):
        self.calls.append(('mkdir', path, create_parents))
        self.existing.add(path)

    def write(self, path, content, content_type='text/plain; charset=utf-8'):
        self.calls.append(('write', path, content, content_type))

    def write_file(self, path, data, content_type='application/octet-stream'):
        self.calls.append(('write_file', path, data, content_type))

    def move(self, source, target, recursive=False):
        self.calls.append(('move', source, target, recursive))
        self.existing.discard(source)
        self.existing.add(target)

    def trash(self, path):
        self.calls.append(('trash', path))
        self.existing.discard(path)


def _skill_document(name):
    return f'---\nname: {name}\ndescription: Example skill.\n---\nUse it.\n'


def _store(fs):
    store = SkillRemoteStore(fs=fs)
    store.root = 'remote://skills'
    return store


def test_skill_storage_category_validation():
    assert require_skill_storage_category(' internal ') == 'internal'
    assert require_skill_storage_category('external') == 'external'
    assert parse_skill_storage_key('internal/example') == ('internal', 'example')

    with pytest.raises(ValueError, match='internal.*external'):
        require_skill_storage_category('research')
    with pytest.raises(ValueError, match='category/name'):
        parse_skill_storage_key('example')
    with pytest.raises(ValueError, match='internal.*external'):
        parse_skill_storage_key('research/example')
    with pytest.raises(ValueError, match='invalid name'):
        parse_skill_storage_key('external/invalid name')


def test_store_rejects_unsupported_storage_category_before_accessing_fs():
    fs = _RemoteFS()
    store = _store(fs)

    with pytest.raises(ValueError, match='internal.*external'):
        store.create('research', 'example', 'content')

    assert fs.calls == []


def test_list_packages_only_returns_valid_storage_categories_and_names():
    fs = _RemoteFS(listings={
        'remote://skills': [
            {'name': 'remote://skills/internal', 'type': 'directory'},
            {'name': 'remote://skills/research', 'type': 'directory'},
            {'name': 'remote://skills/external', 'type': 'directory'},
        ],
        'remote://skills/internal': [
            {'name': 'remote://skills/internal/generated', 'type': 'directory'},
            {'name': 'remote://skills/internal/invalid name', 'type': 'directory'},
        ],
        'remote://skills/external': [
            {'name': 'remote://skills/external/downloaded', 'type': 'directory'},
        ],
    })
    store = _store(fs)

    assert store.list_packages() == [
        {'category': 'external', 'name': 'downloaded'},
        {'category': 'internal', 'name': 'generated'},
    ]
    assert not any(call[0] == 'ls' and call[1] == 'remote://skills/research' for call in fs.calls)


def test_create_rejects_same_key_but_allows_same_name_in_other_category():
    fs = _RemoteFS(existing={'remote://skills/external/example'})
    store = _store(fs)
    content = _skill_document('example')

    result = store.create('internal', 'example', content)

    assert result['category'] == 'internal'
    assert (
        'write',
        'remote://skills/internal/example/SKILL.md',
        content,
        'text/plain; charset=utf-8',
    ) in fs.calls

    fs.calls.clear()
    with pytest.raises(FileExistsError, match='internal/example already exists'):
        store.create('internal', 'example', content)
    assert fs.calls == [('exists', 'remote://skills/internal/example')]


def test_install_package_rejects_existing_key_before_writing():
    fs = _RemoteFS(existing={'remote://skills/external/example'})
    store = _store(fs)

    with pytest.raises(FileExistsError, match='external/example already exists'):
        store.install_package('external', 'example', {
            'SKILL.md': _skill_document('example').encode(),
            'assets/logo.bin': b'logo',
        })

    assert fs.calls == [('exists', 'remote://skills/external/example')]


def test_rename_stays_in_category_and_rejects_existing_target():
    source = 'remote://skills/internal/source'
    target = 'remote://skills/internal/target'
    fs = _RemoteFS(existing={source, target})
    store = _store(fs)

    with pytest.raises(ValueError, match='cannot be moved across storage categories'):
        store.rename(
            'internal',
            'source',
            'external',
            'target',
            skill_content=_skill_document('target'),
        )
    assert fs.calls == []

    with pytest.raises(FileExistsError, match='internal/target already exists'):
        store.rename(
            'internal',
            'source',
            'internal',
            'target',
            skill_content=_skill_document('target'),
        )
    assert fs.calls == [('exists', source), ('exists', target)]


def test_replace_and_remove_require_existing_package():
    fs = _RemoteFS()
    store = _store(fs)

    with pytest.raises(FileNotFoundError, match='internal/missing does not exist'):
        store.replace_files('internal', 'missing', {}, {'SKILL.md': 'content'})
    with pytest.raises(FileNotFoundError, match='external/missing does not exist'):
        store.remove('external', 'missing')

    assert fs.calls == [
        ('exists', 'remote://skills/internal/missing'),
        ('exists', 'remote://skills/external/missing'),
    ]


def test_remove_allows_existing_skill_in_arbitrary_safe_category():
    path = 'remote://skills/research3/web-research'
    fs = _RemoteFS(existing={path})
    store = _store(fs)

    result = store.remove('research3', 'web-research')

    assert result == {
        'persisted': 'remote_fs',
        'deleted': True,
        'path': path,
        'name': 'web-research',
        'category': 'research3',
        'action': 'remove',
    }
    assert fs.calls == [('exists', path), ('trash', path)]
