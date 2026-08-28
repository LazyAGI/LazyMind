from types import SimpleNamespace

import pytest
from lazyllm.tools.agent import ToolExecutionError

from lazymind.chat.engine.tools import kb
from lazymind.chat.engine.tools.local_file.store import FileResourceStore


def _set_uploads(monkeypatch, tmp_path, files):
    monkeypatch.setattr(kb, '_tmp_agentic_config', lambda: {
        'user_id': 'user-1',
        'conversation_id': 'conversation-1',
        'files': [str(path) for path in files],
        'history_files_per_turn': {'1': [str(path) for path in files]},
    })
    monkeypatch.setattr(
        'lazymind.chat.engine.tools.local_file.store.workspace_for_request',
        lambda *_args, **_kwargs: str(tmp_path),
    )
    monkeypatch.setattr(
        'lazymind.chat.engine.tools.local_file.workspace.chat_agent_workspace',
        lambda *_args, **_kwargs: str(tmp_path),
    )


def test_kb_tmp_search_requires_a_query():
    with pytest.raises(ToolExecutionError, match='semantic_query'):
        kb.kb_tmp_search()


def test_grep_channel_on_uploaded_text(monkeypatch, tmp_path):
    notes = tmp_path / 'notes.md'
    notes.write_text('alpha header\nbeta body\nomega clause\n', encoding='utf-8')
    skipped = tmp_path / 'script.py'
    skipped.write_text('omega = 1\n', encoding='utf-8')
    data = tmp_path / 'data.json'
    data.write_text('{"omega": true}\n', encoding='utf-8')
    _set_uploads(monkeypatch, tmp_path, [notes, skipped, data])

    payload = kb.kb_tmp_search(grep_patterns=['omega'])

    assert payload['total'] == 1
    assert payload['hits'][0]['target'] == 'notes.md'
    assert payload['hits'][0]['line'] == 3
    assert payload['hits'][0]['channels'] == ['grep']
    reasons = {item['target']: item['reason'] for item in payload['skipped']}
    assert reasons['script.py'] == 'not_in_whitelist'
    assert reasons['data.json'] == 'not_in_whitelist'


def test_web_file_resources_are_not_in_corpus(monkeypatch, tmp_path):
    notes = tmp_path / 'notes.md'
    notes.write_text('keep this omega line\n', encoding='utf-8')
    _set_uploads(monkeypatch, tmp_path, [notes])
    store = FileResourceStore(str(tmp_path))
    manifest = store.empty_manifest(
        file_id='fr_web01',
        display_name='fetched.pdf',
        source='web',
        source_url='https://example.com/fetched.pdf',
        source_path=str(tmp_path / 'fetched.pdf'),
        original_path=str(tmp_path / 'fetched.pdf'),
        content_sha256='abc',
        bytes_count=12,
        turn_seq=1,
        parse_status='ready',
    )
    parsed = tmp_path / 'file-resources' / 'fr_web01' / 'parsed.md'
    parsed.parent.mkdir(parents=True, exist_ok=True)
    parsed.write_text('omega from the public web\n', encoding='utf-8')
    manifest['parsed_path'] = str(parsed)
    store.write_manifest(manifest)

    result = kb.kb_tmp_search(grep_patterns=['omega'])
    targets = {hit['target'] for hit in result['hits']}
    corpus = {item['target'] for item in result['corpus']}
    assert targets == {'notes.md'}
    assert 'fetched.pdf' not in corpus


def test_grep_hits_rank_before_semantic(monkeypatch, tmp_path):
    notes = tmp_path / 'notes.md'
    notes.write_text('semantic only line\n\ngrep omega line\n', encoding='utf-8')
    _set_uploads(monkeypatch, tmp_path, [notes])

    def fake_retrieve(files, query, **_kwargs):
        return [SimpleNamespace(
            text='semantic only line',
            global_metadata={'file_path': str(notes)},
            metadata={},
        )]

    monkeypatch.setattr(
        'lazymind.chat.engine.tools.algo.search_temp.retrieve_temp_nodes',
        fake_retrieve,
    )
    monkeypatch.setattr(
        'lazymind.chat.engine.tools.algo.search_temp.embed_available',
        lambda: False,
    )
    monkeypatch.setattr(
        kb,
        'get_vocab_manager',
        lambda _user_id: (lambda query: query),
    )

    result = kb.kb_tmp_search(
        semantic_query='meaning',
        grep_patterns=['omega'],
    )
    hits = result['hits']
    assert hits[0]['line'] == 3
    assert 'grep' in hits[0]['channels']
    assert hits[1]['line'] == 1
    assert hits[1]['channels'] == ['bm25_chinese']


def test_parse_timeout_skips_file(monkeypatch, tmp_path):
    notes = tmp_path / 'slow.pdf'
    notes.write_bytes(b'%PDF slow')
    _set_uploads(monkeypatch, tmp_path, [notes])

    def boom(*_args, **_kwargs):
        raise TimeoutError('parse exceeded 60s')

    monkeypatch.setattr(kb, '_tmp_run_with_timeout', boom)
    result = kb.kb_tmp_search(grep_patterns=['omega'])
    skipped = result['skipped']
    assert any(item['reason'] == 'parse_timeout' for item in skipped)
    assert result['hits'] == []


def test_timeout_worker_inherits_agentic_config():
    import lazyllm

    sid = 'search-temp-timeout-test'
    lazyllm.globals._init_sid(sid=sid)
    lazyllm.globals['agentic_config'] = {
        'conversation_id': 'conversation-1',
        'user_id': 'user-1',
    }

    def read_cid():
        cfg = lazyllm.globals.get('agentic_config') or {}
        return cfg.get('conversation_id')

    try:
        assert kb._tmp_run_with_timeout(read_cid, 5) == 'conversation-1'
    finally:
        lazyllm.globals.clear()


def test_pdf_prepare_passes_store_from_request_thread(monkeypatch, tmp_path):
    pdf = tmp_path / 'paper.pdf'
    pdf.write_bytes(b'%PDF-1.4')
    parsed = tmp_path / 'parsed.md'
    parsed.write_text('omega from paper\n', encoding='utf-8')
    _set_uploads(monkeypatch, tmp_path, [pdf])
    captured = {}

    def fake_ingest(_src, **kwargs):
        captured['store'] = kwargs.get('store')
        return {
            'parse_status': 'ready',
            'parsed_path': str(parsed),
            'file_id': 'fr_paper',
        }

    monkeypatch.setattr(
        'lazymind.chat.engine.tools.local_file.ingest.ingest_pdf_file',
        fake_ingest,
    )
    result = kb.kb_tmp_search(grep_patterns=['omega'])
    assert captured['store'] is not None
    assert result['corpus'][0]['target'] == 'paper.pdf'
    assert result['total'] == 1
