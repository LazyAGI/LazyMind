import json
from pathlib import Path

import pytest
from lazyllm.tools.agent import ToolExecutionError

import lazymind.chat.engine.tools.local_file.workspace as workspace_tools
from lazymind.chat.engine.subagent.runner import _build_subagent_tools
from lazymind.chat.engine.tools.local_file import resolver
from lazymind.chat.engine.tools.local_file.ingest import ingest_pdf_file
from lazymind.chat.engine.tools.local_file.store import (
    FileResourceStore,
    render_file_resource_catalog,
)
from lazymind.chat.engine.tools.local_file.window import (
    RESULT_BYTE_BUDGET,
    read_lines_window,
    split_logical_lines,
    utf8_size,
)


def _write_pdf(path: Path, payload: bytes = b'%PDF-1.4 demo') -> Path:
    path.write_bytes(payload)
    return path


def _set_scope(monkeypatch, tmp_path, *, files=None):
    monkeypatch.setattr(resolver.lazyllm, 'globals', {
        'agentic_config': {
            'user_id': 'user-1',
            'conversation_id': 'conversation-1',
            'files': [str(path) for path in (files or [])],
        },
    })
    monkeypatch.setattr(
        workspace_tools,
        'chat_agent_workspace',
        lambda *_args: str(tmp_path),
    )


def _ingest(monkeypatch, tmp_path, text='searchable token omega', name='paper.pdf'):
    store = FileResourceStore(str(tmp_path))
    src = _write_pdf(tmp_path / name, payload=f'%PDF {text}'.encode())
    monkeypatch.setattr(
        'lazymind.chat.engine.tools.local_file.ingest.parse_pdf_pages',
        lambda path: [(1, text)],
    )
    return ingest_pdf_file(str(src), display_name=name, store=store)


def test_ingest_catalog_hides_internal_parsed_path(monkeypatch, tmp_path):
    manifest = _ingest(
        monkeypatch,
        tmp_path,
        text='Alpha methods.\n\nBeta results.\nGamma conclusion.',
    )
    catalog = render_file_resource_catalog(
        FileResourceStore(str(tmp_path)),
        current_turn_seq=1,
    )

    assert manifest['parse_status'] == 'ready'
    assert manifest['file_id'] in catalog
    assert 'parsed.md' not in catalog
    assert 'grep' in catalog
    assert 'kb_tmp_search' in catalog
    assert 'read_file' in catalog


def test_unified_read_footer_uses_only_next_offset_or_eof():
    lines = [f'line-{index}' for index in range(1, 31)]
    first = read_lines_window(lines, offset=1, limit=10)
    rest = read_lines_window(lines, offset=11, limit=100)

    assert first['eof'] is False
    assert first['next_offset'] == 11
    assert first['footer'] == (
        'Showing lines 1-10 of 30.\nUse offset=11 to continue.'
    )
    assert rest['eof'] is True
    assert rest['footer'] == 'End of file.'
    assert rest['next_offset'] is None


def test_unified_tools_resolve_file_id_and_unique_name(monkeypatch, tmp_path):
    _set_scope(monkeypatch, tmp_path)
    manifest = _ingest(monkeypatch, tmp_path)

    searched = workspace_tools.grep(manifest['file_id'], 'omega')
    line = searched['matches'][0]['line']
    by_id = workspace_tools.read_file(manifest['file_id'], offset=line, limit=5)
    by_name = workspace_tools.read_file('paper.pdf')

    assert searched['target'] == manifest['file_id']
    assert 'omega' in by_id['text']
    assert 'omega' in by_name['text']
    assert by_name['kind'] == 'file_resource'


def test_unified_tools_resolve_workspace_and_text_attachment(monkeypatch, tmp_path):
    workspace_file = tmp_path / 'notes.md'
    workspace_file.write_text('workspace needle', encoding='utf-8')
    attachment = tmp_path / 'upload.txt'
    attachment.write_text('attachment needle', encoding='utf-8')
    _set_scope(monkeypatch, tmp_path, files=[attachment])

    workspace_read = workspace_tools.read_file('notes.md')
    attachment_read = workspace_tools.read_file('upload.txt')
    attachment_grep = workspace_tools.grep('upload.txt', 'needle')

    assert 'workspace needle' in workspace_read['text']
    assert attachment_read['kind'] == 'attachment_text'
    assert 'attachment needle' in attachment_read['text']
    assert attachment_grep['total'] == 1


def test_office_attachment_parse_is_cached_by_content(monkeypatch, tmp_path):
    attachment = tmp_path / 'report.docx'
    attachment.write_bytes(b'office-content')
    _set_scope(monkeypatch, tmp_path, files=[attachment])
    calls = []
    monkeypatch.setattr(
        resolver,
        'parse_attachment_content',
        lambda path, priority=0: calls.append(path) or 'cached office text',
    )

    first = workspace_tools.read_file('report.docx')
    second = workspace_tools.read_file('report.docx', offset=1)

    assert 'cached office text' in first['text']
    assert 'cached office text' in second['text']
    assert calls == [str(attachment)]
    assert list((tmp_path / 'attachment-text-cache').glob('*/parsed.txt'))


def test_duplicate_resource_name_is_rejected(monkeypatch, tmp_path):
    _set_scope(monkeypatch, tmp_path)
    store = FileResourceStore(str(tmp_path))
    first = _write_pdf(tmp_path / 'first.pdf', b'%PDF first')
    second = _write_pdf(tmp_path / 'second.pdf', b'%PDF second')
    monkeypatch.setattr(
        'lazymind.chat.engine.tools.local_file.ingest.parse_pdf_pages',
        lambda path: [(1, Path(path).stem)],
    )
    ingest_pdf_file(str(first), display_name='same.pdf', store=store)
    ingest_pdf_file(str(second), display_name='same.pdf', store=store)

    with pytest.raises(ToolExecutionError, match='ambiguous'):
        workspace_tools.read_file('same.pdf')


def test_duplicate_attachment_name_across_turns_is_rejected(monkeypatch, tmp_path):
    first = tmp_path / 'turn-1' / 'same.txt'
    second = tmp_path / 'turn-2' / 'same.txt'
    first.parent.mkdir()
    second.parent.mkdir()
    first.write_text('first', encoding='utf-8')
    second.write_text('second', encoding='utf-8')
    _set_scope(monkeypatch, tmp_path, files=[first, second])
    resolver.lazyllm.globals['agentic_config']['history_files_per_turn'] = {
        '1': [str(first)],
        '2': [str(second)],
    }

    with pytest.raises(ToolExecutionError, match='ambiguous'):
        workspace_tools.read_file('same.txt')


def test_long_physical_line_is_split_and_continuable(monkeypatch, tmp_path):
    _set_scope(monkeypatch, tmp_path)
    content = 'x' * 9001
    (tmp_path / 'long.txt').write_text(content, encoding='utf-8')

    lines = split_logical_lines(content)
    first = workspace_tools.read_file('long.txt', limit=1)
    second = workspace_tools.read_file(
        'long.txt',
        offset=first['next_offset'],
        limit=10,
    )

    assert [len(line) for line in lines] == [4000, 4000, 1001]
    assert first['next_offset'] == 2
    assert second['eof'] is True
    assert first['footer'].endswith('Use offset=2 to continue.')


def test_window_budget_is_utf8_aware_and_continuable():
    for unit in ('x', '中', '😀'):
        content = unit * 20_000
        lines = split_logical_lines(content)
        offset = 1
        visited = []
        while offset <= len(lines):
            window = read_lines_window(lines, offset=offset, limit=4000)
            assert utf8_size(window['text']) < 16 * 1024
            visited.extend(lines[offset - 1:window['end_line']])
            if window['eof']:
                break
            assert window['next_offset'] == window['end_line'] + 1
            offset = window['next_offset']
        assert ''.join(visited) == content


def test_read_file_result_stays_below_spill_threshold(monkeypatch, tmp_path):
    _set_scope(monkeypatch, tmp_path)
    tool_spills = tmp_path / 'tool_spills'
    tool_spills.mkdir()
    for index, unit in enumerate(('x', '中', '😀'), start=1):
        relative = f'tool_spills/{index}.txt'
        (tmp_path / relative).write_text(unit * 20_000, encoding='utf-8')
        result = workspace_tools.read_file(relative)
        assert utf8_size(str(result)) < 16 * 1024
        assert utf8_size(result['text']) <= RESULT_BYTE_BUDGET + 128


def test_grep_zero_matches_has_explicit_footer(monkeypatch, tmp_path):
    _set_scope(monkeypatch, tmp_path)
    (tmp_path / 'notes.txt').write_text('alpha', encoding='utf-8')

    result = workspace_tools.grep('notes.txt', 'missing')

    assert result['total'] == 0
    assert result['footer'] == 'No matches.'


def test_subagent_always_has_unified_read_tools():
    names = {tool.__name__ for tool in _build_subagent_tools([])}

    assert {'grep', 'read_file'} <= names


def test_main_agent_always_registers_unified_read_tools():
    from lazymind.chat.service.chat_service import _build_chat_artifact_tools
    from lazymind.chat.service.component.tool_registry import DEFAULT_TOOLS

    names = {tool.__name__ for tool in _build_chat_artifact_tools()}
    optional = {cfg.name for cfg in DEFAULT_TOOLS}

    assert {'grep', 'read_file', 'write_file', 'list_dir', 'save_chat_artifact'} <= names
    assert {'grep', 'read_file'}.isdisjoint(optional)


def test_migrated_tools_use_single_toolmanager_envelope(monkeypatch, tmp_path):
    import lazyllm
    from lazyllm.tools import ToolManager
    from lazyllm.tools.agent.base import LazyLLMAgentBase
    from lazymind.chat.engine.subagent import tools as subagent_tools
    from lazymind.chat.engine.tools import kb

    attachment = tmp_path / 'notes.txt'
    attachment.write_text('attachment needle', encoding='utf-8')
    lazyllm.globals['agentic_config'] = lazyllm.globals.get('agentic_config') or {}
    monkeypatch.setitem(lazyllm.globals, 'agentic_config', {
        'user_id': 'user-1',
        'conversation_id': 'conversation-1',
        'files': [str(attachment)],
        'history_files_per_turn': {'1': [str(attachment)]},
    })
    monkeypatch.setattr(
        workspace_tools,
        'chat_agent_workspace',
        lambda *_args: str(tmp_path),
    )
    monkeypatch.setattr(
        'lazymind.chat.engine.tools.local_file.store.workspace_for_request',
        lambda *_args, **_kwargs: str(tmp_path),
    )
    manager = ToolManager([
        kb.kb_tmp_search,
        workspace_tools.read_file,
        workspace_tools.grep,
        subagent_tools.read_user_attachment,
        subagent_tools.find_user_attachment,
    ])
    calls = [
        ('kb_tmp_search', {'grep_patterns': ['needle']}),
        ('read_file', {'target': 'notes.txt'}),
        ('grep', {'target': 'notes.txt', 'pattern': 'needle'}),
        ('read_user_attachment', {'filename': 'notes.txt'}),
        ('find_user_attachment', {'filename': 'notes.txt'}),
    ]

    for index, (name, arguments) in enumerate(calls, start=1):
        tool_call = {
            'id': f'call-{index}',
            'type': 'function',
            'function': {
                'name': name,
                'arguments': json.dumps(arguments),
            },
        }
        result = manager([tool_call])[0]

        assert set(result) == {'ok', 'value'}
        assert result['ok'] is True
        assert isinstance(result['value'], dict)
        assert {'success', 'tool', 'result', 'error'}.isdisjoint(result['value'])

        event_item = LazyLLMAgentBase._normalize_tool_results(
            [tool_call],
            [result],
        )[0]
        assert set(event_item) == {'id', 'name', 'arguments', 'result'}
        assert event_item['id'] == tool_call['id']
        assert event_item['name'] == name
        assert event_item['result'] is result

    failure_call = {
        'id': 'call-failure',
        'type': 'function',
        'function': {'name': 'kb_tmp_search', 'arguments': '{}'},
    }
    failure = manager([failure_call])[0]

    assert set(failure) == {'ok', 'value'}
    assert failure['ok'] is False
    assert failure['value'] == 'at least one of semantic_query or grep_patterns is required'
    failure_event = LazyLLMAgentBase._normalize_tool_results(
        [failure_call],
        [failure],
    )[0]
    assert failure_event == {
        'id': 'call-failure',
        'name': 'kb_tmp_search',
        'arguments': '{}',
        'result': failure,
    }


def test_find_by_display_name_requires_a_unique_match(monkeypatch, tmp_path):
    _set_scope(monkeypatch, tmp_path)
    store = FileResourceStore(str(tmp_path))
    first = _write_pdf(tmp_path / 'first.pdf', b'%PDF first')
    second = _write_pdf(tmp_path / 'second.pdf', b'%PDF second')
    monkeypatch.setattr(
        'lazymind.chat.engine.tools.local_file.ingest.parse_pdf_pages',
        lambda path: [(1, Path(path).stem)],
    )
    ingest_pdf_file(str(first), display_name='same.pdf', store=store)
    ingest_pdf_file(str(second), display_name='same.pdf', store=store)

    assert store.find_by_display_name('same.pdf') is None
    unique = ingest_pdf_file(
        str(_write_pdf(tmp_path / 'unique.pdf', b'%PDF unique')),
        display_name='unique.pdf',
        store=store,
    )
    assert store.find_by_display_name('unique.pdf')['file_id'] == unique['file_id']


def test_read_user_attachment_honors_turn(monkeypatch, tmp_path):
    from lazymind.chat.engine.subagent import tools as subagent_tools

    first = tmp_path / 'turn-1' / 'same.txt'
    second = tmp_path / 'turn-2' / 'same.txt'
    first.parent.mkdir()
    second.parent.mkdir()
    first.write_text('first-turn-body', encoding='utf-8')
    second.write_text('second-turn-body', encoding='utf-8')
    cfg = {
        'user_id': 'user-1',
        'conversation_id': 'conversation-1',
        'files': [str(second)],
        'history_files_per_turn': {
            '1': [str(first)],
            '2': [str(second)],
        },
    }
    monkeypatch.setattr(resolver.lazyllm, 'globals', {'agentic_config': cfg})
    monkeypatch.setattr(workspace_tools.lazyllm, 'globals', {'agentic_config': cfg})
    monkeypatch.setattr(
        workspace_tools,
        'chat_agent_workspace',
        lambda *_args: str(tmp_path),
    )

    selected = subagent_tools.read_user_attachment('same.txt', turn=1)

    assert selected['status'] == 'ok'
    assert 'first-turn-body' in selected['content']
    assert 'second-turn-body' not in selected['content']


def test_concurrent_index_upserts_keep_both_entries(tmp_path):
    import threading

    store = FileResourceStore(str(tmp_path))
    barrier = threading.Barrier(2)
    errors = []

    def upsert(name):
        try:
            barrier.wait(timeout=5)
            store.write_manifest(store.empty_manifest(
                file_id=f'fr_{name}',
                display_name=f'{name}.pdf',
                source='upload',
                source_url=None,
                source_path=str(tmp_path / f'{name}.pdf'),
                original_path=str(tmp_path / f'{name}.pdf'),
                content_sha256=name,
                bytes_count=1,
                turn_seq=1,
                parse_status='ready',
            ))
        except Exception as exc:
            errors.append(exc)

    threads = [
        threading.Thread(target=upsert, args=('aaaaaa',)),
        threading.Thread(target=upsert, args=('bbbbbb',)),
    ]
    for thread in threads:
        thread.start()
    for thread in threads:
        thread.join()

    ids = {item['file_id'] for item in store.load_index()}
    assert errors == []
    assert ids == {'fr_aaaaaa', 'fr_bbbbbb'}


def test_reupload_ready_pdf_refreshes_turn_seq(monkeypatch, tmp_path):
    store = FileResourceStore(str(tmp_path))
    src = _write_pdf(tmp_path / 'paper.pdf', b'%PDF same-bytes')
    monkeypatch.setattr(
        'lazymind.chat.engine.tools.local_file.ingest.parse_pdf_pages',
        lambda path: [(1, 'body')],
    )
    first = ingest_pdf_file(str(src), display_name='paper.pdf', turn_seq=1, store=store)
    second = ingest_pdf_file(str(src), display_name='paper.pdf', turn_seq=3, store=store)
    catalog = render_file_resource_catalog(store, current_turn_seq=3)

    assert first['file_id'] == second['file_id']
    assert second['turn_seq'] == 3
    assert second['turn_seqs'] == [1, 3]
    assert '[CURRENT]' in catalog
    assert 'Turn 1,3' in catalog


def test_concurrent_same_pdf_ingest_shares_file_id(monkeypatch, tmp_path):
    import threading

    store = FileResourceStore(str(tmp_path))
    src = _write_pdf(tmp_path / 'paper.pdf', b'%PDF concurrent')
    started = threading.Event()
    release = threading.Event()

    def slow_parse(_path):
        started.set()
        assert release.wait(timeout=5)
        return [(1, 'shared body')]

    monkeypatch.setattr(
        'lazymind.chat.engine.tools.local_file.ingest.parse_pdf_pages',
        slow_parse,
    )
    results = [None, None]
    errors = []

    def run(index):
        try:
            results[index] = ingest_pdf_file(
                str(src), display_name='paper.pdf', turn_seq=index + 1, store=store,
            )
        except Exception as exc:
            errors.append(exc)

    first = threading.Thread(target=run, args=(0,))
    second = threading.Thread(target=run, args=(1,))
    first.start()
    assert started.wait(timeout=5)
    second.start()
    release.set()
    first.join(timeout=10)
    second.join(timeout=10)

    assert errors == []
    assert results[0]['file_id'] == results[1]['file_id']
    assert results[0]['parse_status'] == 'ready'
    assert results[1]['parse_status'] == 'ready'
    assert sorted(store.load_manifest(results[0]['file_id'])['turn_seqs']) == [1, 2]
    assert len(store.load_index()) == 1


def test_expired_lease_takeover_does_not_clobber_ready_with_failed(monkeypatch, tmp_path):
    import threading
    import time

    store = FileResourceStore(str(tmp_path))
    src = _write_pdf(tmp_path / 'paper.pdf', b'%PDF lease')
    started = threading.Event()
    release = threading.Event()
    calls = {'n': 0}

    def parse(_path):
        calls['n'] += 1
        if calls['n'] == 1:
            started.set()
            assert release.wait(timeout=5)
            raise RuntimeError('late fail')
        return [(1, 'takeover body')]

    monkeypatch.setattr(
        'lazymind.chat.engine.tools.local_file.ingest._LEASE_SECONDS',
        0.2,
    )
    monkeypatch.setattr(
        'lazymind.chat.engine.tools.local_file.ingest.parse_pdf_pages',
        parse,
    )
    results = [None, None]
    errors = []

    def run(index):
        try:
            results[index] = ingest_pdf_file(
                str(src), display_name='paper.pdf', turn_seq=index + 1, store=store,
            )
        except Exception as exc:
            errors.append(exc)

    first = threading.Thread(target=run, args=(0,))
    second = threading.Thread(target=run, args=(1,))
    first.start()
    assert started.wait(timeout=5)
    second.start()
    time.sleep(0.35)
    release.set()
    first.join(timeout=10)
    second.join(timeout=10)

    loaded = store.load_manifest(results[0]['file_id'] or results[1]['file_id'])
    assert errors == []
    assert results[0]['parse_status'] == 'ready'
    assert results[1]['parse_status'] == 'ready'
    assert loaded['parse_status'] == 'ready'
    assert loaded.get('parse_error') is None
    assert 'takeover body' in (tmp_path / 'file-resources' / loaded['file_id'] / 'parsed.md').read_text()
