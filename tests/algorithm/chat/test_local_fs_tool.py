from __future__ import annotations

import subprocess

import lazymind.chat.engine.tools.local_fs as local_fs_mod
from lazymind.chat.engine.tools.local_fs import LocalFSToolGroup


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

    assert LocalFSToolGroup().__key_source__() == []


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

    roots = LocalFSToolGroup().ls()
    listing = LocalFSToolGroup().ls(str(source_a))

    assert roots['success'] is True
    assert [entry['source_id'] for entry in roots['result']['entries']] == ['source-a', 'source-b']
    assert listing['success'] is True
    assert [entry['name'] for entry in listing['result']['entries']] == ['allowed.pdf', 'nested']


def test_local_fs_read_checks_source_extension(monkeypatch, tmp_path):
    allowed = tmp_path / 'allowed'
    allowed.mkdir()
    visible = allowed / 'sample.pdf'
    hidden = allowed / 'sample.txt'
    visible.write_text('a\nb\nc\n', encoding='utf-8')
    hidden.write_text('secret', encoding='utf-8')
    _set_local_fs_sources(monkeypatch, [_source('source-a', [allowed], ['pdf'])])

    ok = LocalFSToolGroup().read(str(visible), start_line=1, max_lines=1)
    denied = LocalFSToolGroup().read(str(hidden))

    assert ok['success'] is True
    assert ok['result']['content'] == 'b\n'
    assert ok['result']['source_id'] == 'source-a'
    assert denied['success'] is False
    assert denied['error']['type'] == 'PermissionError'


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

    result = LocalFSToolGroup().read(str(link))
    listing = LocalFSToolGroup().ls(str(allowed))

    assert result['success'] is False
    assert result['tool'] == 'read'
    assert result['error']['type'] == 'PermissionError'
    assert listing['success'] is True
    assert listing['result']['entries'] == []


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
    monkeypatch.setattr(LocalFSToolGroup, '_has_rg', staticmethod(lambda: False))

    globbed = LocalFSToolGroup().glob('*')
    grepped = LocalFSToolGroup().grep('needle')

    assert globbed['success'] is True
    assert [path.split('/')[-1] for path in globbed['result']['matches']] == ['a.pdf', 'b.csv']
    assert grepped['success'] is True
    assert [(entry['source_id'], entry['file'].split('/')[-1]) for entry in grepped['result']['matches']] == [
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

    monkeypatch.setattr(LocalFSToolGroup, '_has_rg', staticmethod(lambda: True))
    monkeypatch.setattr(LocalFSToolGroup, '_run_rg', staticmethod(fake_run_rg))

    assert LocalFSToolGroup().glob('*.pdf')['success'] is True
    assert LocalFSToolGroup().grep('needle')['success'] is True
    assert all('--no-ignore' in args and '--hidden' in args for args in calls)
