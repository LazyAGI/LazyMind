from __future__ import annotations

import os
import subprocess
import time

import pytest
from lazyllm.tools.agent import ToolExecutionError
import lazymind.chat.engine.tools.local_fs as local_fs_mod
from lazymind.chat.engine.tools.local_fs import LocalFileToolkit


def _set_local_fs_sources(monkeypatch, sources):
    monkeypatch.setattr(local_fs_mod.lazyllm, 'globals', {
        'agentic_config': {'local_fs_sources': sources},
    })


def _source(source_id, paths, extensions):
    return {
        'source_id': source_id,
        'paths': [str(path) for path in paths],
        'file_extensions': extensions,
    }


def test_local_fs_key_source_is_empty_without_config(monkeypatch):
    monkeypatch.setattr(local_fs_mod.lazyllm, 'globals', {})

    assert LocalFileToolkit().__key_source__() == []


def test_local_fs_ls_lists_roots_and_filters_directory_files(monkeypatch, tmp_path):
    source_a = tmp_path / 'source-a'
    source_b = tmp_path / 'source-b'
    source_a.mkdir()
    source_b.mkdir()
    (source_a / 'allowed.pdf').write_text('pdf', encoding='utf-8')
    (source_a / 'hidden.txt').write_text('txt', encoding='utf-8')
    (source_a / 'nested').mkdir()
    _set_local_fs_sources(monkeypatch, [
        _source('source-a', [source_a], ['pdf']),
        _source('source-b', [source_b], ['csv']),
    ])

    roots = LocalFileToolkit().ls()
    listing = LocalFileToolkit().ls(str(source_a))

    assert [entry['source_id'] for entry in roots['entries']] == ['source-a', 'source-b']
    assert [entry['name'] for entry in listing['entries']] == ['allowed.pdf', 'nested']


def test_local_fs_read_checks_source_extension(monkeypatch, tmp_path):
    allowed = tmp_path / 'allowed'
    allowed.mkdir()
    visible = allowed / 'sample.pdf'
    hidden = allowed / 'sample.txt'
    visible.write_text('a\nb\nc\n', encoding='utf-8')
    hidden.write_text('secret', encoding='utf-8')
    _set_local_fs_sources(monkeypatch, [_source('source-a', [allowed], ['pdf'])])

    ok = LocalFileToolkit().read(str(visible), start_line=1, max_lines=1)
    with pytest.raises(ToolExecutionError):
        LocalFileToolkit().read(str(hidden))

    assert ok['content'] == 'b\n'
    assert ok['source_id'] == 'source-a'


def test_local_fs_string_replace_updates_one_exact_match_atomically(monkeypatch, tmp_path):
    allowed = tmp_path / 'allowed'
    allowed.mkdir()
    target = allowed / 'notes.md'
    target.write_bytes(b'heading\r\nold value\r\ntail\r\n')
    target.chmod(0o640)
    _set_local_fs_sources(monkeypatch, [_source('source-a', [allowed], ['md'])])

    result = LocalFileToolkit().string_replace(
        str(target),
        'heading\r\nold value',
        'heading\r\nnew value',
    )

    assert result['replacements'] == 1
    assert result['source_id'] == 'source-a'
    assert target.read_bytes() == b'heading\r\nnew value\r\ntail\r\n'
    assert os.stat(target).st_mode & 0o777 == 0o640


def test_local_fs_string_replace_requires_expected_match_count(monkeypatch, tmp_path):
    allowed = tmp_path / 'allowed'
    allowed.mkdir()
    target = allowed / 'notes.txt'
    original = 'same\nsame\n'
    target.write_text(original, encoding='utf-8')
    _set_local_fs_sources(monkeypatch, [_source('source-a', [allowed], ['txt'])])

    with pytest.raises(ToolExecutionError, match='found at least 2'):
        LocalFileToolkit().string_replace(str(target), 'same', 'changed')

    assert target.read_text(encoding='utf-8') == original

    replaced = LocalFileToolkit().string_replace(
        str(target), 'same', 'changed', expected_replacements=2,
    )
    assert replaced['replacements'] == 2
    assert target.read_text(encoding='utf-8') == 'changed\nchanged\n'


def test_local_fs_string_replace_rejects_non_text_content_and_disallowed_files(monkeypatch, tmp_path):
    allowed = tmp_path / 'allowed'
    allowed.mkdir()
    binary = allowed / 'binary.txt'
    hidden = allowed / 'hidden.log'
    binary.write_bytes(b'before\x00after')
    hidden.write_text('before', encoding='utf-8')
    _set_local_fs_sources(monkeypatch, [_source('source-a', [allowed], ['txt'])])

    with pytest.raises(ToolExecutionError):
        LocalFileToolkit().string_replace(str(binary), 'before', 'changed')
    with pytest.raises(ToolExecutionError):
        LocalFileToolkit().string_replace(str(hidden), 'before', 'changed')

    assert binary.read_bytes() == b'before\x00after'
    assert hidden.read_text(encoding='utf-8') == 'before'


def test_local_fs_rejects_symlink_escape(monkeypatch, tmp_path):
    allowed = tmp_path / 'allowed'
    outside = tmp_path / 'outside'
    allowed.mkdir()
    outside.mkdir()
    secret = outside / 'secret.pdf'
    secret.write_text('secret', encoding='utf-8')
    link = allowed / 'link.pdf'
    link.symlink_to(secret)
    _set_local_fs_sources(monkeypatch, [_source('source-a', [allowed], ['pdf'])])

    with pytest.raises(ToolExecutionError):
        LocalFileToolkit().read(str(link))
    listing = LocalFileToolkit().ls(str(allowed))

    assert listing['entries'] == []


def test_local_fs_glob_and_grep_search_multiple_sources_with_extensions(monkeypatch, tmp_path):
    source_a = tmp_path / 'source-a'
    source_b = tmp_path / 'source-b'
    source_a.mkdir()
    source_b.mkdir()
    (source_a / 'a.pdf').write_text('needle in pdf', encoding='utf-8')
    (source_a / 'a.txt').write_text('needle in txt', encoding='utf-8')
    (source_b / 'b.csv').write_text('needle in csv', encoding='utf-8')
    _set_local_fs_sources(monkeypatch, [
        _source('source-a', [source_a], ['pdf']),
        _source('source-b', [source_b], ['csv']),
    ])
    monkeypatch.setattr(LocalFileToolkit, '_has_rg', staticmethod(lambda: False))

    globbed = LocalFileToolkit().glob('*')
    grepped = LocalFileToolkit().grep('needle')

    assert [path.split('/')[-1] for path in globbed['matches']] == ['a.pdf', 'b.csv']
    assert [(entry['source_id'], entry['file'].split('/')[-1]) for entry in grepped['matches']] == [
        ('source-a', 'a.pdf'),
        ('source-b', 'b.csv'),
    ]


def test_local_fs_rg_includes_hidden_and_no_ignore_flags(monkeypatch, tmp_path):
    source = tmp_path / 'source'
    source.mkdir()
    visible = source / '.hidden.pdf'
    visible.write_text('needle', encoding='utf-8')
    _set_local_fs_sources(monkeypatch, [_source('source-a', [source], ['pdf'])])

    calls = []

    def fake_run_rg(args, cwd):
        calls.append(args)
        if '--files' in args:
            return subprocess.CompletedProcess(args, 0, stdout='.hidden.pdf\n', stderr='')
        return subprocess.CompletedProcess(
            args,
            0,
            stdout=(
                '{"type":"match","data":{"path":{"text":".hidden.pdf"},'
                '"line_number":1,"lines":{"text":"needle\\n"}}}\n'
            ),
            stderr='',
        )

    monkeypatch.setattr(LocalFileToolkit, '_has_rg', staticmethod(lambda: True))
    monkeypatch.setattr(LocalFileToolkit, '_run_rg', staticmethod(fake_run_rg))

    assert LocalFileToolkit().glob('*.pdf')['match_count'] == 1
    assert LocalFileToolkit().grep('needle')['match_count'] == 1
    assert all('--no-ignore' in args and '--hidden' in args for args in calls)


def _set_bound_workspace(monkeypatch, root, workspace_id='workspace-1'):
    monkeypatch.setattr(local_fs_mod.lazyllm, 'globals', {
        'agentic_config': {
            'user_id': 'user-1',
            'conversation_id': 'conversation-1',
            '_workspace_execution': {'history_id': 'history-1', 'run_id': 'run-1'},
            'workspace_context': {
                'workspace_id': workspace_id,
                'permission_mode': 'allow_all',
                'permission_version': 1,
            },
            'local_fs_sources': [_source(f'local-workspace:{workspace_id}', [root], ['txt', 'md'])],
        },
    })


def _prepared_workspace_call(toolkit, method, arguments, versions=None, identity=None):
    return toolkit.execute_workspace_call(
        method, arguments, call_id='prepared-call', tool_name='LocalFileToolkit_' + method,
        identity=identity or {'history_id': 'history-1', 'run_id': 'run-1'},
        cancel_check=None, versions=versions if versions is not None else {},
    )


def test_bound_workspace_read_delegates_to_core_without_local_fallback(monkeypatch, tmp_path):
    root = tmp_path / 'workspace'
    root.mkdir()
    target = root / 'notes.txt'
    target.write_text('local-secret', encoding='utf-8')
    _set_bound_workspace(monkeypatch, root)
    calls = []

    def fake_post(path, payload):
        calls.append((path, payload))
        if path.endswith('workspace-operations:prepare'):
            return {'response': {'code': 0, 'data': {
                'operation_id': 'operation-1', 'decision': 'allowed', 'status': 'allowed',
                'version': 'v0', 'path': 'notes.txt',
            }}}
        return {'response': {'code': 0, 'data': {
            'operation_id': 'operation-1', 'content': 'core-content', 'status': 'completed',
            'version': 'v1', 'path': 'notes.txt',
        }}}

    monkeypatch.setattr(local_fs_mod, 'post_core_api', fake_post, raising=False)

    monkeypatch.setattr(local_fs_mod.os.path, 'realpath', lambda *_: pytest.fail('workspace resolved in Python'))
    result = _prepared_workspace_call(LocalFileToolkit(), 'read', {'filepath': str(target)})

    assert result['content'] == 'core-content'
    assert len(calls) == 2
    assert calls[0][0].endswith('workspace-operations:prepare')
    assert calls[1][0].endswith('workspace-operations/operation-1:execute')
    assert calls[0][1]['operation'] == 'read'
    assert calls[0][1]['call_id'].split('/', 1)[1] == 'prepared-call:1'
    assert abs(int(calls[0][1]['call_id'].split('/', 1)[0]) - int(time.time() * 1000)) < 1000
    assert calls[0][1]['call_id'] == calls[1][1]['call_id']
    assert calls[0][1]['path'] == 'notes.txt'


def test_bound_workspace_exposes_create_append_and_delete_operations():
    assert {'create', 'overwrite', 'append', 'delete', 'mkdir'} <= set(LocalFileToolkit.__public_apis__)


def test_bound_workspace_pending_approval_does_not_touch_local_file(monkeypatch, tmp_path):
    root = tmp_path / 'workspace'
    root.mkdir()
    target = root / 'notes.txt'
    target.write_text('local-secret', encoding='utf-8')
    _set_bound_workspace(monkeypatch, root)

    requests = []
    def fake_post(path, payload):
        requests.append((path, payload))
        assert path.endswith('workspace-operations:prepare')
        return {'response': {'code': 0, 'data': {
            'operation_id': 'pending-1', 'decision': 'pending', 'status': 'pending',
            'expires_at': int(time.time() * 1000) - 1, 'path': 'notes.txt',
        }}}

    monkeypatch.setattr(local_fs_mod, 'post_core_api', fake_post)
    with pytest.raises(ToolExecutionError, match='expired'):
        _prepared_workspace_call(LocalFileToolkit(), 'append',
                                 {'filepath': str(target), 'content': '\nnew', 'expected_version': 'observed'})
    assert len(requests) == 1
    assert target.read_text(encoding='utf-8') == 'local-secret'


def test_bound_workspace_mutation_requests_use_core_operations(monkeypatch, tmp_path):
    root = tmp_path / 'workspace'
    root.mkdir()
    _set_bound_workspace(monkeypatch, root)
    calls = []

    def fake_post(path, payload):
        calls.append((path, payload.copy()))
        if path.endswith(':prepare'):
            return {'response': {'code': 0, 'data': {
                'operation_id': f"op-{len(calls)}", 'decision': 'allowed', 'status': 'allowed', 'path': payload['path'],
            }}}
        return {'response': {'code': 0, 'data': {
            'operation_id': path.split('/')[-1].split(':')[0], 'path': payload['path'],
            'version': f"v-{len(calls)}", 'content': payload.get('content', ''), 'status': 'completed',
        }}}

    monkeypatch.setattr(local_fs_mod, 'post_core_api', fake_post, raising=False)
    toolkit = LocalFileToolkit()
    versions = {}
    _prepared_workspace_call(toolkit, 'create', {'filepath': 'new.txt', 'content': 'one'}, versions)
    _prepared_workspace_call(toolkit, 'append', {'filepath': 'new.txt', 'content': 'two'}, versions)
    _prepared_workspace_call(toolkit, 'delete', {'filepath': 'new.txt'}, versions)

    prepare_payloads = [payload for path, payload in calls if path.endswith(':prepare')]
    assert [payload['operation'] for payload in prepare_payloads] == [
        'create', 'append', 'delete',
    ]
    assert [payload.get('expected_version') for payload in prepare_payloads] == [None, 'v-2', 'v-4']
    assert versions == {}
    assert calls[0][1]['call_id'] == calls[1][1]['call_id']


def test_workspace_mutation_without_observation_never_reads_latest(monkeypatch, tmp_path):
    _set_bound_workspace(monkeypatch, tmp_path / 'only-on-core')
    monkeypatch.setattr(local_fs_mod, 'post_core_api', lambda *_: pytest.fail('unobserved mutation contacted Core'))
    with pytest.raises(ToolExecutionError, match='read the file'):
        _prepared_workspace_call(LocalFileToolkit(), 'append', {'filepath': 'notes.txt', 'content': '+'})


def test_workspace_mixed_discovery_keeps_ordinary_sources_and_core_filter(monkeypatch, tmp_path):
    workspace = tmp_path / 'only-on-core'
    ordinary = tmp_path / 'ordinary'
    ordinary.mkdir()
    (ordinary / 'local.txt').write_text('needle ordinary', encoding='utf-8')
    _set_bound_workspace(monkeypatch, workspace)
    local_fs_mod.lazyllm.globals['agentic_config']['local_fs_sources'].append(_source('ordinary', [ordinary], ['txt']))
    calls = []
    def post(path, payload):
        calls.append((path, payload.copy()))
        if path.endswith(':prepare'):
            return {'operation_id': 'op', 'decision': 'allowed', 'status': 'allowed'}
        return {'operation_id': 'op', 'status': 'completed', 'data': {
            'matches': [{'file': 'core.txt', 'path': 'core.txt', 'line': 1, 'content': 'needle core'}],
            'skipped': [{'path': '.env', 'reason': 'approval_required'}],
        }}
    monkeypatch.setattr(local_fs_mod, 'post_core_api', post)
    monkeypatch.setattr(LocalFileToolkit, '_has_rg', staticmethod(lambda: False))
    result = _prepared_workspace_call(LocalFileToolkit(), 'grep', {'pattern': 'needle', 'glob': '*.txt'})
    assert result['match_count'] == 2
    assert {match['source_id'] for match in result['matches']} == {'local-workspace:workspace-1', 'ordinary'}
    assert calls[0][1]['glob'] == '*.txt'
    assert result['skipped'] == [{'path': '.env', 'reason': 'approval_required'}]
    assert not workspace.exists()


@pytest.mark.parametrize('method, arguments', [('ls', {'path': '/only-on-core'}), ('glob', {'pattern': '*.txt'}),
                                                ('grep', {'pattern': 'needle'}), ('info', {'path': '/only-on-core'})])
def test_workspace_discovery_never_reads_python_files(monkeypatch, method, arguments):
    _set_bound_workspace(monkeypatch, '/only-on-core')
    def post(path, payload):
        if path.endswith(':prepare'):
            return {'operation_id': 'op', 'decision': 'allowed', 'status': 'allowed'}
        return {'operation_id': 'op', 'status': 'completed', 'data': {'entries': [], 'matches': [], 'path': '.'}}
    monkeypatch.setattr(local_fs_mod, 'post_core_api', post)
    with monkeypatch.context() as io:
        for name in ('realpath', 'isfile', 'isdir'):
            io.setattr(local_fs_mod.os.path, name, lambda *_: pytest.fail('workspace Python file access'))
        io.setattr(local_fs_mod.os, 'stat', lambda *_: pytest.fail('workspace Python stat'))
        result = _prepared_workspace_call(LocalFileToolkit(), method, arguments)
    assert isinstance(result, dict)


def test_mixed_ordinary_source_alias_does_not_read_workspace(monkeypatch, tmp_path):
    workspace, ordinary = tmp_path / 'workspace', tmp_path / 'ordinary'
    workspace.mkdir()
    ordinary.mkdir()
    (workspace / 'secret.txt').write_text('needle secret', encoding='utf-8')
    (ordinary / 'alias.txt').symlink_to(workspace / 'secret.txt')
    _set_bound_workspace(monkeypatch, workspace)
    local_fs_mod.lazyllm.globals['agentic_config']['local_fs_sources'].append(_source('ordinary', [ordinary], ['txt']))
    def post(path, payload):
        if path.endswith(':prepare'):
            return {'operation_id': 'op', 'decision': 'allowed', 'status': 'allowed'}
        return {'operation_id': 'op', 'status': 'completed', 'data': {'matches': []}}
    monkeypatch.setattr(local_fs_mod, 'post_core_api', post)
    monkeypatch.setattr(LocalFileToolkit, '_has_rg', staticmethod(lambda: False))
    result = _prepared_workspace_call(LocalFileToolkit(), 'grep', {'pattern': 'needle'})
    assert result['matches'] == []


def test_completed_read_receipt_is_not_reported_as_empty_file(monkeypatch, tmp_path):
    _set_bound_workspace(monkeypatch, tmp_path / 'only-on-core')
    def post(path, payload):
        return {'operation_id': 'op', 'decision': 'allowed',
                'status': 'allowed' if path.endswith(':prepare') else 'completed',
                'receipt': not path.endswith(':prepare'), 'version': 'v1'}
    monkeypatch.setattr(local_fs_mod, 'post_core_api', post)
    with pytest.raises(ToolExecutionError, match='not replayed'):
        _prepared_workspace_call(LocalFileToolkit(), 'read', {'filepath': 'notes.txt'})


def test_fresh_empty_read_with_allowed_decision_is_not_a_receipt(monkeypatch, tmp_path):
    _set_bound_workspace(monkeypatch, tmp_path / 'only-on-core')
    def post(path, payload):
        return {'operation_id': 'op', 'decision': 'allowed',
                'status': 'allowed' if path.endswith(':prepare') else 'completed', 'version': 'v1'}
    monkeypatch.setattr(local_fs_mod, 'post_core_api', post)
    result = _prepared_workspace_call(LocalFileToolkit(), 'read', {'filepath': 'empty.txt'})
    assert result['content'] == '' and result['version'] == 'v1'


def test_workspace_context_uses_private_core_snapshot(monkeypatch):
    monkeypatch.setattr(local_fs_mod.lazyllm, 'globals', {'agentic_config': {
        'user_id': 'u', 'conversation_id': 'c',
        '_core_workspace_context': {'workspace_id': 'private', 'permission_mode': 'always_ask'},
        'workspace_context': {'workspace_id': 'public', 'permission_mode': 'allow_all'},
    }})
    assert LocalFileToolkit._workspace_context()['workspace_id'] == 'private'
    assert LocalFileToolkit._workspace_context()['permission_mode'] == 'always_ask'


@pytest.mark.parametrize('count', [0, -1, 101, True])
def test_workspace_replacement_count_rejected_before_core(monkeypatch, tmp_path, count):
    _set_bound_workspace(monkeypatch, tmp_path / 'only-on-core')
    monkeypatch.setattr(local_fs_mod, 'post_core_api', lambda *_: pytest.fail('invalid count consumed an operation'))
    with pytest.raises(ToolExecutionError, match='between 1 and 100'):
        _prepared_workspace_call(LocalFileToolkit(), 'string_replace', {
            'filepath': 'notes.txt', 'old_string': 'old', 'new_string': 'new',
            'expected_replacements': count, 'expected_version': 'observed',
        })


def test_mixed_parent_grep_prunes_controlled_subtree_before_open(monkeypatch, tmp_path):
    import builtins
    ordinary, workspace = tmp_path / 'ordinary', tmp_path / 'ordinary' / 'workspace'
    workspace.mkdir(parents=True)
    secret = workspace / '.env'
    secret.write_text('needle secret', encoding='utf-8')
    (ordinary / 'visible.txt').write_text('needle public', encoding='utf-8')
    _set_bound_workspace(monkeypatch, workspace)
    local_fs_mod.lazyllm.globals['agentic_config']['local_fs_sources'].append(_source('ordinary', [ordinary], ['txt', 'env']))
    def post(path, payload):
        if path.endswith(':prepare'):
            return {'operation_id': 'op', 'decision': 'allowed', 'status': 'allowed'}
        return {'operation_id': 'op', 'status': 'completed', 'data': {'matches': []}}
    monkeypatch.setattr(local_fs_mod, 'post_core_api', post)
    monkeypatch.setattr(LocalFileToolkit, '_has_rg', staticmethod(lambda: True))
    monkeypatch.setattr(LocalFileToolkit, '_run_rg', staticmethod(lambda *_: pytest.fail('rg searched controlled subtree')))
    original = builtins.open
    opened = []
    def guarded(path, *args, **kwargs):
        opened.append(str(path))
        assert str(workspace) not in str(path)
        return original(path, *args, **kwargs)
    monkeypatch.setattr(builtins, 'open', guarded)
    result = _prepared_workspace_call(LocalFileToolkit(), 'grep', {'pattern': 'needle'})
    assert [match['content'] for match in result['matches']] == ['needle public']
    assert str(secret) not in opened
