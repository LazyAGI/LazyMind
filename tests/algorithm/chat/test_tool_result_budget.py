import copy
import json
import re
from types import SimpleNamespace

import lazyllm
import pytest
from lazyllm.tools.tools.search import TavilySearch
from lazyllm.tools.agent.toolsManager import ToolManager

from lazymind.chat.lazyllm_tool_docs import ensure_lazyllm_tool_docs
from lazymind.chat.service.component import AgentEventFrameTranslator
from lazymind.chat.engine.tools.infra.tool_result_budget import bound_external_search_result, SEARCH_RESULT_CHARS
from lazymind.chat.engine.tools.infra.tool_result_citations import CitationResultMiddleware
from lazymind.chat.service.utils.citations import reset_citation_state


def size(value):
    return len(json.dumps(value, ensure_ascii=False))


def item(index=0, **extra):
    return {'title': f'Title {index}', 'url': f'https://example.test/{index}', 'source': 'tavily',
            'snippet': '中🙂' * 800, 'extra': {'doc_id': str(index), **extra},
            'ref': f'[[{index + 1}.1]]', 'citation_index': f'{index + 1}.1'}


def test_search_budget_counts_characters_not_bytes_and_preserves_input():
    raw = {'ok': True, 'value': [item(i, raw_content='文' * 2000) for i in range(8)]}
    original = copy.deepcopy(raw)
    result = bound_external_search_result(raw)
    assert size(result) <= SEARCH_RESULT_CHARS
    assert len(json.dumps(result, ensure_ascii=False).encode()) > SEARCH_RESULT_CHARS
    assert len(result['value']) == 8
    assert raw == original
    assert all(len(v['snippet']) == len(v['extra']['raw_content']) == 700 for v in result['value'])
    assert all(v['extra']['truncated'] for v in result['value'])


def test_remove_metadata_before_results_and_preserve_citations():
    raw = {'ok': True, 'value': [item(0, images=['x' * 20000])]}
    result = bound_external_search_result(raw)
    assert len(result['value']) == 1
    assert 'images' not in result['value'][0]['extra']
    assert result['truncated'] is True
    for key in ('url', 'ref', 'citation_index'):
        assert result['value'][0][key] == raw['value'][0][key]


def test_metadata_search_reports_returned_count_separate_from_total():
    raw = {'ok': True, 'value': {
        'items': [item(i) for i in range(200)], 'total_count': 9999,
        'next_cursor': 'provider-cursor', 'page': 1,
    }}
    result = bound_external_search_result(raw)
    value = result['value']
    assert size(result) <= SEARCH_RESULT_CHARS
    assert 0 < len(value['items']) < 200
    assert value['returned_count'] == result['returned_count'] == len(value['items'])
    assert value['total_count'] == 9999 and value['next_cursor'] == 'provider-cursor'
    assert value['truncated'] is True
    assert value['items'][-1]['extra']['doc_id'] == str(len(value['items']) - 1)


@pytest.mark.parametrize('field', ['url', 'next_cursor'])
def test_oversized_identity_is_never_clipped(field):
    raw = {'ok': True, 'value': [dict(item(), url='https://example.test/' + 'x' * 20000)]}
    if field == 'next_cursor':
        raw['value'] = {'items': [item()], 'next_cursor': 'x' * 20000}
    result = bound_external_search_result(raw)
    assert size(result) <= SEARCH_RESULT_CHARS
    assert result['value'] == [] or result['value']['items'] == []
    assert result['truncated'] and result['returned_count'] == 0


@pytest.mark.parametrize('citations,collect_only', [(False, False), (True, False), (True, True)])
def test_middleware_bounds_search_without_affecting_content_or_other_tools(citations, collect_only):
    previous = lazyllm.globals.get('agentic_config')
    state = {}
    if citations:
        reset_citation_state(state)
    lazyllm.globals['agentic_config'] = {
        'citation_state': state, 'citation_mode': 'collect_only' if collect_only else '',
    }
    provider = TavilySearch(api_key='test')
    manager = SimpleNamespace(tools_info={
        'search': SimpleNamespace(_instance=provider, _method_name='search'),
        'read': SimpleNamespace(_instance=provider, _method_name='get_content'),
        'send_mail': SimpleNamespace(_instance=None, _method_name='send_mail'),
    })
    middleware = CitationResultMiddleware(manager)
    try:
        result = middleware._process_result({'function': {'name': 'search'}},
                                            {'ok': True, 'value': [item(i) for i in range(100)]},
                                            state, collect_only=collect_only)
        assert size(result) <= SEARCH_RESULT_CHARS
        if citations and not collect_only:
            assert result['value'][0]['ref']
        for name in ('read', 'send_mail'):
            raw = {'ok': True, 'value': {'content': '文' * 16384}}
            actual = middleware._process_result({'function': {'name': name}}, raw, state)
            assert actual['value']['content'] == raw['value']['content']
        failed = {'ok': False, 'error': 'provider unavailable'}
        assert middleware._process_result({'function': {'name': 'search'}}, failed, state) is failed
    finally:
        lazyllm.globals['agentic_config'] = previous or {}


@pytest.mark.parametrize('citations', [False, True])
def test_real_tool_manager_updates_records_and_event_results(monkeypatch, citations):
    previous = lazyllm.globals.get('agentic_config')
    state = {}
    if citations:
        reset_citation_state(state)
    lazyllm.globals['agentic_config'] = {'citation_state': state}
    provider = TavilySearch(api_key='test')
    raw = {'results': [{'title': f'Item {i}', 'url': f'https://example.test/{i}',
                        'content': '文' * 2000, 'raw_content': '原文' * 50000} for i in range(30)]}
    monkeypatch.setattr(provider, '_request', lambda *a, **k: SimpleNamespace(json=lambda: raw))
    try:
        ensure_lazyllm_tool_docs([provider])
        manager = CitationResultMiddleware(ToolManager([provider]))
        batch = manager.execute_with_records({'id': 'large-search', 'function': {
            'name': 'TavilySearch_search', 'arguments': {'query': 'test', 'include_raw_content': True}}})
        result = batch.results[0]
        assert result['ok'] is True
        assert size(result) <= SEARCH_RESULT_CHARS
        assert batch.records[0].result == result
        assert batch.stamped_results()[0] == result
        assert result['returned_count'] == len(result['value']) < 30
        assert ('ref' in result['value'][0]) is citations
        frames = AgentEventFrameTranslator(query='test').feed({'tag': 'tool_results', 'tool_results': [{
            'id': 'large-search', 'name': 'TavilySearch_search', 'result': batch.records[0].result,
        }]})
        text = ''.join(frame.get('text') or '' for frame in frames)
        displayed = json.loads(re.search(r'<tool_result>(.*?)</tool_result>', text, re.S).group(1))
        assert displayed['result'] == result
        assert size(displayed['result']) <= SEARCH_RESULT_CHARS
    finally:
        lazyllm.globals['agentic_config'] = previous or {}


@pytest.mark.parametrize('length', [700, 701, 20000])
@pytest.mark.parametrize('nested', [False, True])
def test_long_titles_preserve_results_and_source_identity(length, nested):
    first = item(0)
    first['snippet'] = 'short'
    (first['extra'] if nested else first)['title'] = '文' * length
    raw = {'ok': True, 'value': [first, item(1)]}
    original = copy.deepcopy(raw)
    result = bound_external_search_result(raw)
    assert len(result['value']) == 2
    assert size(result) <= SEARCH_RESULT_CHARS
    actual = result['value'][0]
    assert (actual['extra'] if nested else actual)['title'] == '文' * min(length, 700)
    for key in ('url', 'ref', 'citation_index'):
        assert actual[key] == first[key]
    assert actual['extra']['doc_id'] == first['extra']['doc_id']
    assert actual['extra'].get('truncated', False) == (length > 700)
    assert raw == original
