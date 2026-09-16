import json
from types import SimpleNamespace
from pathlib import Path

import pytest
from lazyllm.tools.agent.toolsManager import ToolManager
from lazyllm.tools.tools.search import TavilySearch
from lazyllm.tools.tools.search.base import _make_result
from lazymind.chat.engine.tools.infra.tool_result_citations import CitationResultMiddleware
from lazymind.chat.engine.tools.infra import tool_result_budget as budget
from lazymind.chat.service.utils.streaming import sse_line
from lazymind.chat.engine.tools.local_file.window import load_text_lines, read_lines_window


def response(value):
    return SimpleNamespace(json=lambda: value, raise_for_status=lambda: None)


def test_provider_to_model_and_ui_projection_keeps_original(tmp_path, monkeypatch):
    text = ('正文🙂\\"\n' * 20000)
    tool = TavilySearch(api_key='test')
    monkeypatch.setattr(tool, '_request', lambda *a, **k: response({
        'results': [{'title': 'Paper', 'url': 'https://example.test', 'content': 'summary', 'raw_content': text}],
        'answer': text,
    }))
    from lazymind.chat.lazyllm_tool_docs import ensure_lazyllm_tool_docs
    ensure_lazyllm_tool_docs([tool])
    manager = budget.ToolResultBudgetMiddleware(CitationResultMiddleware(ToolManager([tool])), str(tmp_path))
    batch = manager.execute_with_records([{
        'id': 'c1', 'function': {'name': 'TavilySearch_search',
                                 'arguments': {'query': 'test', 'include_raw_content': True}},
    }])
    result = batch.results[0]
    assert result['ok']
    assert budget.encoded_size(result) <= budget.RESULT_BYTES
    assert batch.records[0].result == result
    items = result['value']
    assert items[0]['extra']['raw_content'] == text[:700]
    target = items[0]['extra']['content_snapshot']['target']
    assert (tmp_path / target).read_text() == text
    assert not str(tmp_path) in json.dumps(result)
    full = items[0]['extra']['result_read']['full_result']['target']
    assert json.loads((tmp_path / full).read_text())['value'][0]['extra']['raw_content'] == text


def test_huge_search_metadata_and_batch_are_bounded(tmp_path):
    items = [_make_result('题' * 3000, 'https://example.test/' + str(i), '摘要' * 600,
                          'sciverse', authors=['作' * 5000] * 10) for i in range(200)]
    result = budget.bound_tool_result(
        'SciverseSearch_meta_search', {'ok': True, 'value': {'items': items, 'total_count': 10000}},
        workspace=str(tmp_path), search=True,
    )
    assert budget.encoded_size(result) <= budget.RESULT_BYTES
    value = result['value']
    assert value['returned_count'] == len(value['items']) < 200
    assert value['total_count'] == 10000
    original = json.loads((tmp_path / value['extra']['result_read']['full_result']['target']).read_text())
    assert len(original['value']['items']) == 200


def test_write_failure_does_not_repeat_successful_action_or_expose_body(monkeypatch):
    monkeypatch.setattr(budget, '_snapshot', lambda *a: (_ for _ in ()).throw(OSError('secret path')))
    result = budget.bound_tool_result('send_mail', {'ok': True, 'value': {
        'status': 'sent', '_agent_control': {'stop': True}, 'body': 'x' * 100000}})
    assert result['ok'] is True
    assert result['value']['status'] == 'sent'
    assert result['value']['_agent_control'] == {'stop': True}
    assert 'snapshot_error' in result['value']['result_read']
    assert 'full_result' not in result['value']['result_read']
    assert budget.encoded_size(result) <= budget.RESULT_BYTES


@pytest.mark.parametrize('ok', [True, False])
def test_generic_preserves_failure_and_ids(tmp_path, ok):
    value = {'ok': ok, 'value': {
        'status': 'done' if ok else 'failed', 'request_id': 'receipt-1', 'content': '🙂' * 20000,
    }}
    result = budget.bound_tool_result('other_tool', value, workspace=str(tmp_path))
    assert result['ok'] == ok
    assert result['value']['request_id'] == 'receipt-1'
    assert budget.encoded_size(result) <= budget.RESULT_BYTES


def test_search_read_limit_and_snapshot_text(tmp_path, monkeypatch):
    provider = TavilySearch(api_key='test')
    text = ('abcdef\r\n🙂\n\"\\' * 5000) + 'final\n'
    monkeypatch.setattr(provider, '_fetch_content_text', lambda item: text)
    raw = provider.get_content({'url': 'https://example.test'}, limit=100000)
    assert len(raw['content']) == 700
    assert raw['extra']['content_read']['limit'] == 700
    result = budget.bound_tool_result(
        'TavilySearch_get_content', {'ok': True, 'value': raw}, workspace=str(tmp_path), search=True,
    )
    path = tmp_path / result['value']['extra']['content_snapshot']['target']
    assert path.read_bytes().decode() == text
    lines = load_text_lines(str(path))
    offset, returned = 1, []
    while True:
        page = read_lines_window(lines, offset=offset, limit=4000)
        returned.append(page['content'])
        assert budget.encoded_size(page) <= 10 * 1024
        if page['eof']:
            break
        offset = page['next_offset']
    assert ''.join(returned) == text


def test_encoded_stream_frames_preserve_text_exactly():
    text = ('中文字🙂\\"\n' * 30000)
    encoded = sse_line({'code': 200, 'data': {'text': text, 'think': '', 'sources': []}})
    frames = [line for line in encoded.splitlines() if line]
    assert all(len((line + '\n\n').encode()) <= 64 * 1024 for line in frames)
    assert ''.join(json.loads(line)['data'].get('text', '') for line in frames) == text


@pytest.mark.parametrize('trusted', [False, True])
def test_snapshot_reference_is_scoped_to_current_conversation(tmp_path, monkeypatch, trusted):
    from lazymind.chat.engine.tools.local_file import workspace as files
    from lazyllm.tools.agent import ToolExecutionError
    root = tmp_path / 'chat-artifacts'
    first, second = root / 'first', root / 'second'
    first.mkdir(parents=True)
    second.mkdir()
    monkeypatch.setattr(files, 'chat_agent_workspace', lambda user, conv: str(root / conv))
    monkeypatch.setattr(files, '_cfg', {'trusted_local_mode': trusted, 'agentic_workspace': str(tmp_path)})
    result = budget.bound_tool_result('tool', {'content': 'x' * 50000}, workspace=str(first))
    target = result['result_read']['full_result']['target']
    _, resolved = files._resolve_workspace_path(target, 'user', 'first')
    assert resolved == str(first / target)
    with pytest.raises(ToolExecutionError):
        files._resolve_workspace_path(str(first / target), 'user', 'second')
    _, other = files._resolve_workspace_path(target, 'user', 'second')
    assert not Path(other).exists()


def test_old_tagged_history_uses_explicit_workspace_before_request_globals(tmp_path):
    from lazymind.chat.service.component.history import normalize_history_for_agent
    call = {'id': 'old-call', 'name': 'tool', 'arguments': {}}
    result = {'id': 'old-call', 'name': 'tool', 'result': {'ok': True, 'value': '旧内容' * 20000}}
    history = [{'role': 'assistant', 'content': (
        '<tool_call>' + json.dumps(call) + '</tool_call>'
        '<tool_result>' + json.dumps(result) + '</tool_result>'
    )}]
    original = history[0]['content']
    messages = normalize_history_for_agent(history, workspace=str(tmp_path))
    value = json.loads(messages[1]['content'])
    assert budget.encoded_size(value) <= budget.RESULT_BYTES
    target = value['value']['result_read']['full_result']['target']
    assert json.loads((tmp_path / target).read_text()) == result['result']
    assert history[0]['content'] == original


def test_huge_link_and_images_keep_search_list_and_valid_snapshot(tmp_path):
    item = _make_result('Title', 'https://example.test/' + 'x' * 100000, 'text', 'provider',
                        images=['https://image.test/' + 'x' * 3000] * 1000)
    result = budget.bound_tool_result('search', {'ok': True, 'value': [item]},
                                      workspace=str(tmp_path), search=True)
    assert result['ok'] is True
    assert isinstance(result['value'], list)
    assert budget.encoded_size(result) <= budget.RESULT_BYTES
    preview = result['value'][0]
    assert 'url' not in preview  # Never expose a clipped, invalid URL.
    target = preview['extra']['result_read']['full_result']['target']
    assert len(json.loads((tmp_path / target).read_text())['value'][0]['extra']['images']) == 1000


def test_batch_events_split_results_and_preserve_call_ids(tmp_path, monkeypatch):
    from lazymind.chat.service.component import AgentEventFrameTranslator
    from lazymind.chat.engine.tools.local_file import store
    monkeypatch.setattr(store, 'workspace_for_request', lambda: str(tmp_path))
    translator = AgentEventFrameTranslator(query='test')
    frames = translator.feed({'tag': 'tool_results', 'tool_results': [
        {'id': f'call-{i}', 'name': 'tool', 'result': {'ok': True, 'value': '🙂' * 100000}}
        for i in range(10)
    ]})
    assert len(frames) == 10
    for i, frame in enumerate(frames):
        assert f'call-{i}' in frame['text']
        assert len(sse_line({'code': 200, 'data': frame}).encode()) <= 64 * 1024
