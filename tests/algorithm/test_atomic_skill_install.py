import base64
import io
import zipfile

import pytest
from lazyllm.tools.agent import ToolExecutionError

from lazymind.chat.engine.tools.skill_editor import SkillManagementToolkit
from lazymind.common.skill.remote_store import SkillRemoteStore
from lazymind.common.integrations.remote_fs import RemoteFS


class AtomicRemoteFS:
    def __init__(self, *, status='installed', error=None):
        self.calls = []
        self.status = status
        self.error = error

    def install_skill(self, **kwargs):
        self.calls.append(kwargs)
        if self.error:
            raise self.error
        return {'status': self.status, 'skill_id': 'skill-1', 'skill_key': 'external/example',
                'source_url': kwargs.get('url', ''), 'enabled': False}


@pytest.mark.parametrize('source_url', [
    'https://github.com/owner/repo/tree/main/skills/example',
    'https://skills.sh/owner/repo/example',
    'https://skillsmp.com/skills/example',
    'https://skillhub.cn/skills/owner/example',
    'https://clawhub.ai/owner/example',
])
def test_chat_install_uses_atomic_shared_url_importer(source_url):
    fs = AtomicRemoteFS()
    result = SkillManagementToolkit(store=SkillRemoteStore(fs=fs)).install_skill(source_url)
    assert fs.calls == [{'url': source_url}]
    assert result['status'] == 'installed'
    assert result['skill_key'] == 'external/example'
    assert result['enabled'] is False


def test_chat_retry_reports_existing_skill_without_changing_it():
    fs = AtomicRemoteFS(status='already_installed')
    result = SkillManagementToolkit(store=SkillRemoteStore(fs=fs)).install_skill('https://github.com/owner/repo')
    assert result['status'] == 'already_installed'
    assert result['skill_key'] == 'external/example'
    assert len(fs.calls) == 1


def test_failed_chat_install_does_not_trash_an_existing_package():
    fs = AtomicRemoteFS(error=RuntimeError('skill already exists'))
    with pytest.raises(ToolExecutionError, match='skill already exists'):
        SkillManagementToolkit(store=SkillRemoteStore(fs=fs)).install_skill('https://github.com/owner/repo')
    assert fs.calls == [{'url': 'https://github.com/owner/repo'}]


def test_local_package_install_sends_one_complete_atomic_archive():
    fs = AtomicRemoteFS()
    store = SkillRemoteStore(fs=fs)
    files = {'SKILL.md': b'---\nname: example\ndescription: Example skill.\n---\nBody.\n',
             'assets/logo.png': b'\x89PNG\x00\xff'}
    result = store.install_package('external', 'example', files)
    assert result['skill_key'] == 'external/example'
    assert len(fs.calls) == 1
    assert fs.calls[0]['expected_name'] == 'example'
    with zipfile.ZipFile(io.BytesIO(fs.calls[0]['package_zip'])) as archive:
        assert {name: archive.read(name) for name in archive.namelist()} == files


def test_atomic_remote_fs_request_has_long_import_timeout(monkeypatch):
    calls = []
    fs = RemoteFS(base_url='https://core.example/api/core')
    monkeypatch.setattr(fs, '_request', lambda *args, **kwargs: calls.append((args, kwargs)) or {'status': 'installed'})
    fs.install_skill(url='https://skills.sh/owner/repo/example')
    assert calls[0][0] == ('POST', 'skills:install')
    assert calls[0][1]['json'] == {'url': 'https://skills.sh/owner/repo/example'}
    assert calls[0][1]['timeout'] >= 300
