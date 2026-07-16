import io
import zipfile

from lazymind.chat.engine.tools.infra.github_skill_installer import GitHubSkillInstaller
from lazymind.chat.engine.tools.infra.skill_remote_store import SkillRemoteStore
from lazymind.chat.engine.tools.skill_editor import SkillManagementToolkit


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


def _skill_archive():
    output = io.BytesIO()
    with zipfile.ZipFile(output, 'w') as archive:
        archive.writestr(
            'example-main/SKILL.md',
            '---\nname: example\ndescription: Example skill.\n---\nUse this skill.\n',
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
    assert remote_fs.calls[:3] == [
        ('exists', 'remote://skills/external/example'),
        ('ls', 'remote://skills', True),
        ('mkdir', 'remote://skills/external/example', True),
    ]
    assert remote_fs.calls[3] == (
        'write_file',
        'remote://skills/external/example/assets/logo.bin',
        b'logo',
        'application/octet-stream',
    )
    assert remote_fs.calls[4][0:2] == (
        'write',
        'remote://skills/external/example/SKILL.md',
    )
    assert 'category: external' in remote_fs.calls[4][2]
    assert 'github_url: https://github.com/owner/example' in remote_fs.calls[4][2]
