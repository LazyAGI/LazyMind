from __future__ import annotations

import os
import subprocess

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
