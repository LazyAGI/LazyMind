import io

import pytest

from lazymind.chat.engine.tools.infra.skill_remote_store import SkillRemoteStore


class _RecordingFS:
    def __init__(self):
        self.calls = []

    def mkdir(self, path, create_parents=True):
        self.calls.append(('mkdir', path, create_parents))

    def write_file(self, path, data, content_type='application/octet-stream'):
        self.calls.append(('write_file', path, data, content_type))

    def write(self, path, content, content_type='text/plain; charset=utf-8'):
        self.calls.append(('write', path, content, content_type))

    def trash(self, path):
        self.calls.append(('trash', path))

    def exists(self, path):
        self.calls.append(('exists', path))
        return path.endswith('/existing')

    def ls(self, path, detail=True):
        self.calls.append(('ls', path, detail))
        if path == 'remote://skills':
            return [{'name': 'remote://skills/external', 'type': 'directory'}]
        if path == 'remote://skills/external':
            return [{'name': 'remote://skills/external/existing', 'type': 'directory'}]
        return []

    def open(self, path, mode='r', **kwargs):
        self.calls.append(('open', path, mode, kwargs))
        return io.StringIO('---\nname: existing\ngithub_url: https://github.com/o/r/tree/main/s\n---\n')


class _FailingFS(_RecordingFS):
    def write_file(self, path, data, content_type='application/octet-stream'):
        super().write_file(path, data, content_type)
        raise RuntimeError('backend down')


class _FailingCleanupFS(_FailingFS):
    def trash(self, path):
        super().trash(path)
        raise RuntimeError('trash unavailable')


def test_install_package_writes_supporting_bytes_before_skill_md():
    fs = _RecordingFS()
    store = SkillRemoteStore(fs=fs)

    result = store.install_package('external', 'example', {
        'SKILL.md': b'---\nname: example\n---\nBody\n',
        'assets/logo.png': b'\x89PNG',
        'references/guide.md': b'# Guide\n',
    })

    assert result['path'] == 'remote://skills/external/example'
    assert fs.calls == [
        ('mkdir', 'remote://skills/external/example', True),
        (
            'write_file',
            'remote://skills/external/example/assets/logo.png',
            b'\x89PNG',
            'image/png',
        ),
        (
            'write_file',
            'remote://skills/external/example/references/guide.md',
            b'# Guide\n',
            'text/markdown',
        ),
        (
            'write',
            'remote://skills/external/example/SKILL.md',
            '---\nname: example\n---\nBody\n',
            'text/markdown; charset=utf-8',
        ),
    ]


def test_install_package_trashes_new_package_after_remote_write_failure():
    fs = _FailingFS()
    store = SkillRemoteStore(fs=fs)

    with pytest.raises(RuntimeError, match='backend down'):
        store.install_package('external', 'example', {
            'SKILL.md': b'---\nname: example\n---\nBody\n',
            'assets/logo.bin': b'content',
        })

    assert fs.calls[-1] == ('trash', 'remote://skills/external/example')


def test_install_package_preserves_write_and_cleanup_failures():
    store = SkillRemoteStore(fs=_FailingCleanupFS())

    with pytest.raises(RuntimeError) as exc_info:
        store.install_package('external', 'example', {
            'SKILL.md': b'---\nname: example\n---\nBody\n',
            'assets/logo.bin': b'content',
        })

    assert 'backend down' in str(exc_info.value)
    assert 'cleanup also failed: trash unavailable' in str(exc_info.value)


def test_install_package_validates_before_creating_remote_package():
    fs = _RecordingFS()
    store = SkillRemoteStore(fs=fs)

    with pytest.raises(ValueError, match='must contain SKILL.md'):
        store.install_package('external', 'example', {'assets/logo.bin': b'content'})

    assert fs.calls == []


def test_store_lists_package_keys_and_reads_only_requested_skill_md():
    fs = _RecordingFS()
    store = SkillRemoteStore(fs=fs)

    assert store.package_exists('external', 'existing') is True
    assert store.list_packages() == [{'category': 'external', 'name': 'existing'}]
    content = store.read_skill_md('external', 'existing')

    assert 'github_url: https://github.com/o/r/tree/main/s' in content
    assert [call[0] for call in fs.calls].count('open') == 1
    assert fs.calls[-1][1] == 'remote://skills/external/existing/SKILL.md'
