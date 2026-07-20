import io
import zipfile
from unittest.mock import patch

from lazymind.chat.engine.tools.infra.github_skill_installer import (
    GitHubSkillInstaller,
    GitHubSkillSource,
    PreparedSkillPackage,
)
from lazymind.chat.engine.tools.skill_editor import SkillManagementToolkit
from lazymind.common.skill_remote_store import SkillRemoteStore


class _Response:
    def __init__(self, *, json_data=None, content=b''):
        self.status_code = 200
        self.headers = {}
        self.reason = 'OK'
        self._json_data = json_data
        self._content = content

    def json(self):
        return self._json_data

    def iter_content(self, chunk_size):
        yield self._content

    def close(self):
        pass


class _GitHubSession:
    def __init__(self, archive):
        self._responses = [
            _Response(json_data={'default_branch': 'main'}),
            _Response(content=archive),
        ]

    def get(self, url, **kwargs):
        return self._responses.pop(0)


class _RemoteFS:
    def __init__(self):
        self.calls = []

    def exists(self, path):
        self.calls.append(('exists', path))
        return False

    def ls(self, path, detail=True):
        self.calls.append(('ls', path, detail))
        return []

    def mkdir(self, path, create_parents=True):
        self.calls.append(('mkdir', path, create_parents))

    def write_file(self, path, data, content_type='application/octet-stream'):
        self.calls.append(('write_file', path, data, content_type))

    def write(self, path, content, content_type='text/plain; charset=utf-8'):
        self.calls.append(('write', path, content, content_type))

    def trash(self, path):
        self.calls.append(('trash', path))


class _PreparedInstaller:
    def __init__(self):
        self.source = GitHubSkillSource(
            owner='owner',
            repo='example',
            ref='main',
            skill_path='',
            canonical_url='https://github.com/owner/example',
        )
        self.package = PreparedSkillPackage(
            source=self.source,
            name='example',
            description='Example skill.',
            files={'SKILL.md': b'---\nname: example\ndescription: Example skill.\n---\nUse it.\n'},
        )

    def prepare(self, github_url):
        return self.package

    def resolve_source(self, github_url):
        return self.source


class _PackageStore:
    def __init__(self, packages, *, skill_documents=None, read_errors=None):
        self.packages = list(packages)
        self.skill_documents = dict(skill_documents or {})
        self.read_errors = dict(read_errors or {})
        self.read_calls = []
        self.installed = []

    def package_exists(self, category, name):
        return False

    def list_packages(self):
        return list(self.packages)

    def read_skill_md(self, category, name):
        key = (category, name)
        self.read_calls.append(key)
        if key in self.read_errors:
            raise self.read_errors[key]
        return self.skill_documents[key]

    def install_package(self, category, name, files):
        self.installed.append((category, name, files))


def _skill_archive(category_line=''):
    output = io.BytesIO()
    with zipfile.ZipFile(output, 'w') as archive:
        archive.writestr(
            'example-main/SKILL.md',
            '---\nname: example\n'
            f'{category_line}'
            'description: Example skill.\n---\nUse this skill.\n',
        )
        archive.writestr('example-main/assets/logo.bin', b'logo')
    return output.getvalue()


def test_install_public_github_skill_to_remote_fs():
    remote_fs = _RemoteFS()
    store = SkillRemoteStore(fs=remote_fs)
    store.root = 'remote://skills'
    installer = GitHubSkillInstaller(session=_GitHubSession(_skill_archive()))

    result = SkillManagementToolkit(store=store, installer=installer).install_skill(
        'https://github.com/owner/example'
    )

    assert result['success'] is True
    assert result['result'] == {
        'status': 'installed',
        'skill_key': 'external/example',
        'github_url': 'https://github.com/owner/example',
        'enabled': False,
        'message': 'Skill installed. Go to Skill Management > My Skills to review and enable it.',
    }
    assert remote_fs.calls[:4] == [
        ('exists', 'remote://skills/external/example'),
        ('ls', 'remote://skills', True),
        ('exists', 'remote://skills/external/example'),
        ('mkdir', 'remote://skills/external/example', True),
    ]
    assert remote_fs.calls[4] == (
        'write_file',
        'remote://skills/external/example/assets/logo.bin',
        b'logo',
        'application/octet-stream',
    )
    assert remote_fs.calls[5][0:2] == (
        'write',
        'remote://skills/external/example/SKILL.md',
    )
    assert 'category:' not in remote_fs.calls[5][2]
    assert 'github_url: https://github.com/owner/example' in remote_fs.calls[5][2]


def test_install_preserves_upstream_category_but_still_uses_external_path():
    remote_fs = _RemoteFS()
    store = SkillRemoteStore(fs=remote_fs)
    store.root = 'remote://skills'
    installer = GitHubSkillInstaller(
        session=_GitHubSession(_skill_archive('category: upstream-value\n'))
    )

    result = SkillManagementToolkit(store=store, installer=installer).install_skill(
        'https://github.com/owner/example'
    )

    assert result['success'] is True
    assert result['result']['skill_key'] == 'external/example'
    assert remote_fs.calls[5][1] == 'remote://skills/external/example/SKILL.md'
    assert 'category: upstream-value' in remote_fs.calls[5][2]


def test_install_ignores_unreadable_internal_package_during_source_deduplication():
    store = _PackageStore(
        [{'category': 'internal', 'name': 'grill-me'}],
        read_errors={('internal', 'grill-me'): RuntimeError('not found')},
    )

    result = SkillManagementToolkit(store=store, installer=_PreparedInstaller()).install_skill(
        'https://github.com/owner/example'
    )

    assert result['success'] is True
    assert store.read_calls == []
    assert store.installed[0][0:2] == ('external', 'example')


def test_install_skips_unreadable_external_package_during_source_deduplication():
    store = _PackageStore(
        [{'category': 'external', 'name': 'broken'}],
        read_errors={('external', 'broken'): RuntimeError('not found')},
    )

    with patch('lazymind.chat.engine.tools.skill_editor.lazyllm.LOG.warning') as warning:
        result = SkillManagementToolkit(store=store, installer=_PreparedInstaller()).install_skill(
            'https://github.com/owner/example'
        )

    assert result['success'] is True
    assert store.read_calls == [('external', 'broken')]
    assert store.installed[0][0:2] == ('external', 'example')
    warning.assert_called_once()
    assert 'skill=external/broken' in warning.call_args.args[0]


def test_install_still_rejects_duplicate_external_github_source():
    store = _PackageStore(
        [{'category': 'external', 'name': 'existing'}],
        skill_documents={
            ('external', 'existing'): (
                '---\nname: existing\ndescription: Existing skill.\n'
                'github_url: https://github.com/owner/example\n---\nUse it.\n'
            )
        },
    )

    result = SkillManagementToolkit(store=store, installer=_PreparedInstaller()).install_skill(
        'https://github.com/owner/example'
    )

    assert result['success'] is False
    assert result['error']['reason'] == (
        "GitHub source is already installed as 'external/existing'."
    )
    assert store.installed == []


def test_install_finds_duplicate_external_source_after_unreadable_package():
    store = _PackageStore(
        [
            {'category': 'external', 'name': 'broken'},
            {'category': 'external', 'name': 'existing'},
        ],
        skill_documents={
            ('external', 'existing'): (
                '---\nname: existing\ndescription: Existing skill.\n'
                'github_url: https://github.com/owner/example\n---\nUse it.\n'
            )
        },
        read_errors={('external', 'broken'): RuntimeError('not found')},
    )

    result = SkillManagementToolkit(store=store, installer=_PreparedInstaller()).install_skill(
        'https://github.com/owner/example'
    )

    assert result['success'] is False
    assert result['error']['reason'] == (
        "GitHub source is already installed as 'external/existing'."
    )
    assert store.read_calls == [('external', 'broken'), ('external', 'existing')]
    assert store.installed == []
