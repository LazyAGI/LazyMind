from __future__ import annotations

import io
import json
import shutil
import zipfile
from pathlib import Path

import pytest

from lazyllm.tools.agent.skill_manager import SkillManager
from lazyllm.tools.sandbox.dummy_sandbox import DummySandbox
from lazymind.chat.engine.tools.infra.github_skill_installer import GitHubSkillInstaller
from lazymind.chat.engine.tools.skill_editor import SkillManagementToolkit
from lazymind.common.skill.remote_store import SkillRemoteStore


class _Response:
    status_code = 200
    headers = {}
    reason = 'OK'

    def __init__(self, *, json_data=None, content=b''):
        self._json_data = json_data
        self._content = content

    def json(self):
        return self._json_data

    def iter_content(self, chunk_size):
        del chunk_size
        yield self._content

    def close(self):
        pass


class _GitHubSession:
    def __init__(self, archive: bytes):
        self._responses = [
            _Response(json_data={'default_branch': 'main'}),
            _Response(content=archive),
        ]

    def get(self, url, **kwargs):
        del url, kwargs
        return self._responses.pop(0)


class _DiskRemoteFS:
    """Remote-URI filesystem backed by tmp_path for an offline integration test."""

    protocol = 'remote'
    _fs_protocol_key = 'remote'

    def __init__(self, root: Path):
        self.root = root
        self.materializations = []

    def _path(self, remote_path: str) -> Path:
        raw = str(remote_path).strip()
        if raw.startswith('remote://'):
            raw = raw[len('remote://'):]
        return self.root.joinpath(*[part for part in raw.replace('\\', '/').split('/') if part])

    def _uri(self, local_path: Path) -> str:
        return f'remote://{local_path.relative_to(self.root).as_posix()}'

    def exists(self, path: str, **kwargs) -> bool:
        del kwargs
        return self._path(path).exists()

    def mkdir(self, path: str, create_parents: bool = True, **kwargs) -> None:
        del kwargs
        self._path(path).mkdir(parents=create_parents, exist_ok=True)

    def ls(self, path: str, detail: bool = True, **kwargs):
        del kwargs
        directory = self._path(path)
        if not directory.is_dir():
            return []
        entries = []
        for child in sorted(directory.iterdir(), key=lambda item: item.name):
            if detail:
                entries.append({
                    'name': self._uri(child),
                    'type': 'directory' if child.is_dir() else 'file',
                    'size': 0 if child.is_dir() else child.stat().st_size,
                })
            else:
                entries.append(self._uri(child))
        return entries

    def open(self, path: str, mode: str = 'rb', **kwargs):
        return self._path(path).open(mode, **kwargs)

    def info(self, path: str, **kwargs):
        del kwargs
        local_path = self._path(path)
        return {
            'name': self._uri(local_path),
            'type': 'directory' if local_path.is_dir() else 'file',
            'size': 0 if local_path.is_dir() else local_path.stat().st_size,
        }

    def write_file(self, path: str, data: bytes, content_type: str = 'application/octet-stream') -> None:
        del content_type
        local_path = self._path(path)
        local_path.parent.mkdir(parents=True, exist_ok=True)
        local_path.write_bytes(data)

    def write(self, path: str, content: str, content_type: str = 'text/plain; charset=utf-8') -> None:
        del content_type
        local_path = self._path(path)
        local_path.parent.mkdir(parents=True, exist_ok=True)
        local_path.write_text(content, encoding='utf-8')

    def trash(self, path: str) -> None:
        shutil.rmtree(self._path(path))

    def materialize_dir(self, path: str, local_dir: str, **kwargs):
        del kwargs
        source = self._path(path)
        destination = Path(local_dir)
        shutil.copytree(source, destination, dirs_exist_ok=True)
        files = sorted(
            item.relative_to(source).as_posix()
            for item in source.rglob('*')
            if item.is_file()
        )
        self.materializations.append((path, local_dir, files))
        return {
            'source_path': path,
            'local_dir': local_dir,
            'materialized': True,
            'file_count': len(files),
            'files': files,
        }


def _skill_archive() -> bytes:
    output = io.BytesIO()
    with zipfile.ZipFile(output, 'w') as archive:
        archive.writestr(
            'example-main/SKILL.md',
            '---\n'
            'name: example\n'
            'description: Installed skill script integration fixture.\n'
            '---\n'
            'Run scripts/check.py and read references/message.txt.\n',
        )
        archive.writestr('example-main/references/message.txt', 'installed-reference\n')
        archive.writestr(
            'example-main/scripts/check.py',
            'import json\n'
            'import pathlib\n'
            'import sys\n'
            'root = pathlib.Path(__file__).resolve().parents[1]\n'
            'print(json.dumps({\n'
            '    "args": sys.argv[1:],\n'
            '    "reference": (root / "references" / "message.txt").read_text().strip(),\n'
            '}))\n',
        )
    return output.getvalue()


@pytest.fixture
def installed_skill(tmp_path):
    fs = _DiskRemoteFS(tmp_path / 'remote-store')
    store = SkillRemoteStore(fs=fs)
    store.root = 'remote://skills'
    installer = GitHubSkillInstaller(session=_GitHubSession(_skill_archive()))

    result = SkillManagementToolkit(store=store, installer=installer).install_skill(
        'https://github.com/owner/example',
    )

    return fs, result


def test_skill_install_stage_persists_complete_package(installed_skill):
    fs, result = installed_skill

    assert result['success'] is True
    assert result['result']['skill_key'] == 'external/example'
    package = fs._path('remote://skills/external/example')
    assert (package / 'SKILL.md').is_file()
    assert (package / 'references' / 'message.txt').read_text(encoding='utf-8') == 'installed-reference\n'
    assert (package / 'scripts' / 'check.py').is_file()


def test_skill_discovery_and_materialization_stages(installed_skill):
    fs, _ = installed_skill
    manager = SkillManager(
        dir='remote://skills',
        skills=['external/example'],
        fs=fs,
        sandbox=DummySandbox(),
    )

    skill = manager.get_skill('external/example')
    reference = manager.read_reference('external/example', 'references/message.txt')
    result = manager.run_script('external/example', 'scripts/check.py', args=['stage-check'])

    assert skill['status'] == 'ok'
    assert skill['path'] == 'remote://skills/external/example/SKILL.md'
    assert reference['content'] == 'installed-reference\n'
    assert fs.materializations[-1][0] == 'remote://skills/external/example'
    assert fs.materializations[-1][2] == [
        'SKILL.md',
        'references/message.txt',
        'scripts/check.py',
    ]
    assert result['status'] == 'ok'


def test_install_skill_then_execute_script_end_to_end(installed_skill):
    fs, install_result = installed_skill
    manager = SkillManager(
        dir='remote://skills',
        skills=['external/example'],
        fs=fs,
        sandbox=DummySandbox(timeout=10),
    )

    execution = manager.run_script(
        install_result['result']['skill_key'],
        'scripts/check.py',
        args=['--platform-matrix', 'ok'],
    )

    assert execution['status'] == 'ok'
    assert execution['exit_code'] == 0
    assert execution['stderr'] == ''
    assert json.loads(execution['stdout']) == {
        'args': ['--platform-matrix', 'ok'],
        'reference': 'installed-reference',
    }
