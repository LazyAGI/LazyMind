from io import BytesIO, StringIO

import pytest
import requests
from lazyllm.tools.agent import ToolExecutionError
from lazyllm.tools.agent.skill_manager import SkillManager

from lazymind.chat.engine.tools.file_resources import remote_skill, tools as workspace

ROOT = 'remote://skills/external/excel'


class FakeRemoteFS:
    files = {ROOT + '/SKILL.md': b'# Excel', ROOT + '/references/guide.md': b'first\nneedle\nlast'}

    def ls(self, path, detail=True):
        if path == ROOT:
            return [{'name': ROOT + '/SKILL.md', 'type': 'file'},
                    {'name': ROOT + '/references', 'type': 'directory'}]
        if path == ROOT + '/references':
            return [{'name': ROOT + '/references/guide.md', 'type': 'file'}]
        raise FileNotFoundError(path)

    def info(self, path):
        return {'type': 'file' if path in self.files else 'directory'}

    def open(self, path, mode='rb', **kwargs):
        if path not in self.files:
            raise FileNotFoundError(path)
        return BytesIO(self.files[path]) if 'b' in mode else StringIO(self.files[path].decode())

    def read_limited(self, path, max_bytes):
        with self.open(path, 'rb') as stream:
            return stream.read(max_bytes + 1)


@pytest.fixture(autouse=True)
def remote_fs(monkeypatch):
    monkeypatch.setattr(remote_skill, 'RemoteFS', FakeRemoteFS)


def test_list_remote_never_uses_local_workspace(monkeypatch):
    def fail(*args, **kwargs):
        pytest.fail('remote URI reached local workspace resolver')
    monkeypatch.setattr(workspace, '_resolve_text_target_for_tool', fail)
    result = workspace.list_skill_files(ROOT, recursive=True)
    assert result['path'] == ROOT
    assert result['entries'] == ['SKILL.md', 'references', 'references/guide.md']
    assert not result['truncated']
    workspace.read_file_resource(ROOT + '/SKILL.md')
    workspace.search_file_resource(ROOT, 'needle')


def test_remote_reference_window_and_grep_targets():
    target = ROOT + '/references/guide.md'
    result = workspace.read_file_resource(target, offset=2, limit=1)
    assert result['target'] == target
    assert result['offset'] == 2
    assert 'needle' in result['text']
    assert not result['eof']
    hits = workspace.search_file_resource(ROOT, 'needle')
    assert hits['matches'] == [{'target': target, 'line': 2, 'text': 'needle'}]


def test_existing_read_reference_uses_skill_relative_path():
    manager = SkillManager(dir='', fs=FakeRemoteFS())
    manager._skills_index = {'external/excel': {'name': 'external/excel', 'path': ROOT}}
    result = manager.read_reference('external/excel', 'references/guide.md')
    assert result['path'] == ROOT + '/references/guide.md'
    assert result['content'] == 'first\nneedle\nlast'


def test_remote_listing_limit_does_not_skip_grep_files(monkeypatch):
    names = [ROOT + f'/file-{n}.md' for n in range(205)]
    monkeypatch.setattr(FakeRemoteFS, 'ls', lambda *a, **k: [{'name': n, 'type': 'file'} for n in names])
    monkeypatch.setattr(FakeRemoteFS, 'files', {name: b'needle' if n == 10 else b'none'
                                             for n, name in enumerate(names)})
    result = workspace.search_file_resource(ROOT, 'needle')
    assert result['truncated']
    assert result['matches'][0]['target'] == names[10]


@pytest.mark.parametrize('target', [
    'remote:/skills/external/excel', 'remote://memory/private',
    ROOT + '/../private', ROOT + '/%2e%2e/private', ROOT + '?token=x',
    ROOT + '/references\\secret',
])
def test_invalid_remote_paths_never_fall_back_to_local(target):
    with pytest.raises(ToolExecutionError, match='invalid_skill_uri'):
        workspace.list_skill_files(target)


def test_missing_remote_reference_is_reported():
    with pytest.raises(ToolExecutionError, match='remote_resource_not_found'):
        workspace.read_file_resource(ROOT + '/missing.md')


@pytest.mark.parametrize('status,code', [(404, 'remote_resource_not_found'),
                                       (403, 'remote_resource_access_denied'),
                                       (503, 'skill_remote_mount_unavailable')])
def test_remote_http_error_classification(monkeypatch, status, code):
    def fail(*args, **kwargs):
        response = requests.Response()
        response.status_code = status
        try:
            raise requests.HTTPError(response=response)
        except requests.HTTPError as exc:
            raise RuntimeError('internal detail') from exc
    monkeypatch.setattr(FakeRemoteFS, 'ls', fail)
    with pytest.raises(ToolExecutionError, match=code):
        workspace.list_skill_files(ROOT)


def test_attachment_only_tools_do_not_gain_remote_access():
    with pytest.raises(ToolExecutionError, match='attachment-only'):
        workspace._read_file(ROOT + '/SKILL.md', resources_only=True)
    with pytest.raises(ToolExecutionError, match='attachment-only'):
        workspace._grep(ROOT, 'needle', resources_only=True)


@pytest.mark.parametrize('target', ['/tmp', '.', 'file:///tmp'])
def test_remote_listing_does_not_grant_host_file_access(target):
    with pytest.raises(ToolExecutionError, match='invalid_skill_uri'):
        workspace.list_skill_files(target)


def test_listing_cannot_escape_requested_remote_directory(monkeypatch):
    monkeypatch.setattr(FakeRemoteFS, 'ls', lambda *a, **k: [{'name': 'remote://skills/other/secret'}])
    with pytest.raises(ToolExecutionError, match='escaped'):
        workspace.list_skill_files(ROOT)


def test_recursive_depth_limit_reports_incomplete_listing():
    result = workspace.list_skill_files(ROOT, recursive=True, max_depth=0)
    assert result['truncated']
    assert 'references/guide.md' not in result['entries']
    assert not workspace.list_skill_files(ROOT, recursive=False, max_depth=0)['truncated']
    assert not workspace.list_skill_files(ROOT, recursive=True, max_depth=1)['truncated']


@pytest.mark.parametrize('count,truncated', [(199, False), (200, False), (201, True)])
def test_listing_limit_only_marks_actual_omissions(monkeypatch, count, truncated):
    entries = [{'name': ROOT + f'/file-{i}.md', 'type': 'file'} for i in range(count)]
    # A repeated entry must not be mistaken for an omitted unique file.
    monkeypatch.setattr(FakeRemoteFS, 'ls', lambda *a, **k: entries + entries[:1])
    result = workspace.list_skill_files(ROOT)
    assert len(result['entries']) == min(count, 200)
    assert result['truncated'] is truncated


def test_directory_search_continues_after_oversized_file(monkeypatch):
    large = ROOT + '/large.bin'
    guide = ROOT + '/guide.md'
    monkeypatch.setattr(FakeRemoteFS, 'files', {large: b'x' * (20 * 1024 * 1024 + 1), guide: b'needle'})
    monkeypatch.setattr(FakeRemoteFS, 'ls', lambda *a, **k: [
        {'name': large, 'type': 'file'}, {'name': guide, 'type': 'file'},
    ])
    result = workspace.search_file_resource(ROOT, 'needle')
    assert result['matches'] == [{'target': guide, 'line': 1, 'text': 'needle'}]
    assert result['skipped_files'] == [{'target': large, 'reason': 'remote_resource_too_large'}]
    assert result['truncated']
    assert result['footer'] == 'Search truncated.'
    with pytest.raises(ToolExecutionError, match='remote_resource_too_large'):
        workspace.search_file_resource(large, 'needle')
    with pytest.raises(ToolExecutionError, match='remote_resource_too_large'):
        workspace.read_file_resource(large)


def test_directory_search_does_not_swallow_permission_errors(monkeypatch):
    def denied(*args, **kwargs):
        raise ToolExecutionError('remote_resource_access_denied')
    monkeypatch.setattr(FakeRemoteFS, 'open', denied)
    with pytest.raises(ToolExecutionError, match='remote_resource_access_denied'):
        workspace.search_file_resource(ROOT, 'needle')


@pytest.mark.parametrize('operation', ['list', 'read', 'search'])
def test_remote_filesystem_permission_error_classification(monkeypatch, operation):
    def denied(*args, **kwargs):
        raise PermissionError('private filesystem detail')
    monkeypatch.setattr(FakeRemoteFS, 'ls' if operation == 'list' else 'open', denied)
    with pytest.raises(ToolExecutionError, match='^remote_resource_access_denied$'):
        if operation == 'list':
            workspace.list_skill_files(ROOT)
        elif operation == 'read':
            workspace.read_file_resource(ROOT + '/SKILL.md')
        else:
            workspace.search_file_resource(ROOT, 'needle')
