import copy
import json
from types import SimpleNamespace

import httpx
import pytest

from lazyllm.tools.agent import ToolExecutionError, ToolManager
from lazymind.chat.engine.agent_runtime.compactors import is_oversized_tool_result
from lazymind.chat.service import academic_preparation as preparation


@pytest.fixture
def preparation_session(monkeypatch):
    requests = []
    sections = {
        'paper': {'paper_id': 'seed', 'title': 'Seed study',
                  'authors': ['Research author number ' + str(index) for index in range(592)]},
        'source_version': 'seed-version-1', 'content_hash': 'seed-hash',
        'pages': [{'page': 1, 'text': 'Abstract and contents'},
                  {'page': 3, 'text': 'Method: Query Router derives its design from Earlier System. '
                                      'Its window is 𝑛win tokens.'},
                  {'page': 4, 'text': 'Related work and experiments compare Earlier System.'}],
        'positions': [0, 1, 2],
        'next_cursor': 'seed-content-cursor',
    }
    references = {
        'paper': {'paper_id': 'seed'}, 'source_version': 'seed-version-1', 'view': 'references',
        'references': [{'reference_key': 'ref-1', 'raw_text': 'Earlier System. arXiv:2305.13245.',
                        'paper': {'paper_id': 'predecessor', 'arxiv_id': '2305.13245',
                                  'authors': sections['paper']['authors']}}],
        'next_cursor': 'seed-reference-cursor',
    }

    def request(method, url, **kwargs):
        requests.append((method, url, kwargs))
        data = kwargs['json']
        reference_view = data.get('view') == 'references' or data.get('cursor') == 'seed-reference-cursor'
        if reference_view:
            value = references
        else:
            selected = data.get('positions', list(range(len(sections['pages']))))
            if data.get('page_start'):
                selected = [index for index in selected
                            if data['page_start'] <= sections['pages'][index]['page'] <= data['page_end']]
            if data.get('cursor') == 'seed-content-cursor':
                selected = [2]
            limit = data.get('max_results', 2)
            value = {**sections, 'positions': selected[:limit],
                     'pages': [sections['pages'][index] for index in selected[:limit]],
                     'next_cursor': 'seed-content-cursor' if len(selected) > limit else ''}
        return SimpleNamespace(raise_for_status=lambda: None, json=lambda: {'data': value})

    class Client:
        base_url = 'http://core-fixture'
        transport = SimpleNamespace(request=request)

        def __init__(self, *args, **kwargs):
            pass

        def _headers(self):
            return {'X-User-Id': 'owner'}

    monkeypatch.setattr(preparation, 'WorkflowClient', Client)
    monkeypatch.setattr(preparation, 'inject_model_config', lambda _: None)
    monkeypatch.setattr(preparation, 'AutoModel', lambda **_: object())
    payload = {
        'llm_config': {}, 'depth': {'guidance': 'Standard research', 'preparation': {
            'rounds': 20, 'tool_calls': {'search_papers': 12, 'read_paper': 20}}},
        'user_id': 'owner', 'expansion_id': 'exp', 'revision': 3,
        'input': {'papers': [{'paper': sections['paper'], 'is_seed': True}]},
        'preparation': {'user_instruction': 'Trace design origins and comparisons through 2026-11-02'},
    }
    return payload, requests


def grounded_result():
    return {
        'summary': 'Read the seed methods, related work and experiment baselines.',
        'directions': [{'name': 'design origins and comparisons', 'question': 'What changed?',
                        'reason': 'Named design source and baseline in the seed.',
                        'difference': 'Method provenance and comparison', 'paper_ids': ['seed']}],
        'research_questions': [{'id': 'origin', 'direction': 'design origins and comparisons',
                                'question': 'Which mechanism did Query Router inherit?',
                                'category': 'predecessor'}],
        'named_clues': [{'name': 'Earlier System', 'role': 'Design source and experimental baseline',
                         'source_paper_id': 'seed', 'source_location': 'Sections 3–4, pp. 3–4',
                         'source_version': 'seed-version-1', 'identity': {'arxiv_id': '2305.13245'},
                         'source_excerpt': 'Query Router derives its design from Earlier System.', 'positions': [1],
                         'question_ids': ['origin'], 'paper_id': 'predecessor'}],
        'selected_directions': ['design origins and comparisons'],
    }


@pytest.mark.parametrize('status', [422, 503])
def test_preparation_exposes_correctable_core_errors_without_hiding_service_failure(
        monkeypatch, preparation_session, status):
    payload, _ = preparation_session
    transport = preparation.WorkflowClient.transport
    original_request = transport.request

    def request(method, url, **kwargs):
        if kwargs['json'].get('paper_id') == 'unregistered':
            return httpx.Response(status, request=httpx.Request(method, url), json={
                'code': 'INVALID_SELECTION', 'message': 'paper must be registered in this expansion',
            })
        return original_request(method, url, **kwargs)

    monkeypatch.setattr(transport, 'request', request)

    def execute(self, llm, plan):
        read, finish = plan.tools[1:]
        if status == 422:
            with pytest.raises(ToolExecutionError, match='INVALID_SELECTION: paper must be registered'):
                read('unregistered')
        else:
            with pytest.raises(httpx.HTTPStatusError):
                read('unregistered')
        read('seed')
        finish(**grounded_result())

    monkeypatch.setattr(preparation.AgentExecutor, 'run', execute)
    result = preparation.run_preparation(payload)
    assert result['directions'] == grounded_result()['directions']
    assert result['metrics']['tool_calls'] == 1


def test_preparation_reads_seed_sections_and_references_then_publishes_grounded_clues(
        monkeypatch, preparation_session):
    payload, requests = preparation_session
    expected = grounded_result()
    reference_clue = copy.deepcopy(expected['named_clues'][0])
    reference_clue.pop('positions')
    reference_clue.update(source_excerpt='Earlier System. arXiv:2305.13245.', reference_key='ref-1')
    expected['named_clues'].append(reference_clue)

    def execute(self, llm, plan):
        tools = ToolManager(plan.tools)

        def invoke(name, arguments):
            tool = next(tool for tool in tools.all_tools if tool.name == name)
            return tool(arguments)

        first = invoke('read_paper', {'paper_id': 'seed', 'page_start': 1, 'page_end': 4})
        assert first['source_version'] == 'seed-version-1'
        assert first['positions'] == [0, 1] and len(first['pages']) == 2
        assert first['pages'][1]['text'] == (
            'Method: Query Router derives its design from Earlier System. Its window is 𝑛win tokens.')
        assert not is_oversized_tool_result(first)
        next_pages = invoke('read_paper', {'cursor': first['next_cursor']})
        assert next_pages['positions'] == [2] and 'experiments' in next_pages['pages'][0]['text']
        invoke('read_paper', {'paper_id': 'seed', 'keywords': ['Method', 'Related work', 'Experiments']})
        invoke('read_paper', {'paper_id': 'seed', 'positions': [1, 2]})
        refs = invoke('read_paper', {'paper_id': 'seed', 'view': 'references',
                                     'keywords': ['Earlier System'], 'max_results': 2})
        assert refs['references'][0]['paper']['paper_id'] == 'predecessor'
        assert refs['references'][0]['raw_text'] == 'Earlier System. arXiv:2305.13245.'
        assert not is_oversized_tool_result(refs)
        invoke('read_paper', {'paper_id': 'seed', 'view': 'references',
                              'reference_keys': ['ref-1'], 'max_results': 1})
        invoke('read_paper', {'cursor': refs['next_cursor']})
        assert invoke('finish_preparation', expected) == {'status': 'saved'}

    monkeypatch.setattr(preparation.AgentExecutor, 'run', execute)
    result = preparation.run_preparation(payload)
    assert result['research_questions'] == expected['research_questions']
    assert result['named_clues'] == expected['named_clues']
    assert result['directions'] == expected['directions']
    assert result['selected_directions'] == expected['selected_directions']
    assert result['metrics']['tool_calls'] == 7
    assert all(kwargs['json']['expansion_id'] == 'exp' and kwargs['json']['preparation_revision'] == 3
               and kwargs['headers'] == {'X-User-Id': 'owner'} for _, _, kwargs in requests)
    assert requests[1][2]['json'] == {
        'cursor': 'seed-content-cursor', 'expansion_id': 'exp', 'preparation_revision': 3,
    }
    assert requests[3][2]['json']['positions'] == [1, 2]
    assert requests[0][2]['json']['max_results'] == 2
    assert requests[-1][1] == 'http://core-fixture/academic/paper-contents:read'
    assert requests[4][2]['json']['keywords'] == ['Earlier System']
    assert requests[4][2]['json']['max_results'] == 2
    assert requests[5][2]['json']['reference_keys'] == ['ref-1']
    assert requests[-1][2]['json']['cursor'] == 'seed-reference-cursor'


def test_preparation_rejects_broken_clue_links_and_mixed_cursor_before_saving(monkeypatch, preparation_session):
    payload, requests = preparation_session

    def execute(self, llm, plan):
        read, finish = plan.tools[1:]
        with pytest.raises(ToolExecutionError, match='only its cursor'):
            read('seed', cursor='seed-content-cursor')
        with pytest.raises(ToolExecutionError, match='view must be'):
            read('seed', view='invented')
        assert not requests
        read('seed')
        broken = copy.deepcopy(grounded_result())
        broken['directions'][0]['paper_ids'] = ['not-returned']
        with pytest.raises(ToolExecutionError, match='unregistered paper_ids.*not-returned'):
            finish(**broken)
        broken = copy.deepcopy(grounded_result())
        broken['named_clues'][0]['question_ids'] = ['missing']
        with pytest.raises(ToolExecutionError, match='valid research question_ids') as exc:
            finish(**broken)
        assert 'named_clues[0]' in str(exc.value) and 'origin' in str(exc.value)
        broken = copy.deepcopy(grounded_result())
        broken['research_questions'][0]['category'] = 'keyword'
        with pytest.raises(ToolExecutionError, match='predecessor/successor/peer'):
            finish(**broken)
        finish(**grounded_result())

    monkeypatch.setattr(preparation.AgentExecutor, 'run', execute)
    result = preparation.run_preparation(payload)
    assert result['named_clues'][0]['question_ids'] == ['origin']
    assert result['metrics']['tool_calls'] == 1


@pytest.mark.parametrize(('changes', 'error'), [
    ({'positions': []}, 'real positions/reference_key anchors'),
    ({'source_paper_id': 'unread-source'}, 'source paper/version was not returned'),
    ({'source_version': 'different-version'}, 'source paper/version was not returned'),
    ({'positions': [99]}, 'anchors were not returned'),
    ({'positions': [0]}, 'source_excerpt is not in'),
    ({'source_excerpt': 'This user-requested comparison should exist in the seed.'}, 'source_excerpt is not in'),
    ({'name': 'A different mechanism not named by the source'}, 'original name present in source_excerpt'),
    ({'positions': [], 'reference_key': 'unread-reference'}, 'anchors were not returned'),
])
def test_preparation_requires_actually_read_excerpt_anchors_and_allows_correction(
        monkeypatch, preparation_session, changes, error):
    payload, _ = preparation_session

    def execute(self, llm, plan):
        read, finish = plan.tools[1:]
        read('seed')
        invalid = grounded_result()
        invalid['named_clues'][0].update(changes)
        with pytest.raises(ToolExecutionError, match=error) as exc:
            finish(**invalid)
        if 'source_excerpt' in changes:
            assert 'copy an exact excerpt' in str(exc.value) and 'Earlier System' in str(exc.value)
        assert finish(**grounded_result()) == {'status': 'saved'}

    monkeypatch.setattr(preparation.AgentExecutor, 'run', execute)
    result = preparation.run_preparation(payload)
    assert result['named_clues'] == grounded_result()['named_clues']


def test_preparation_cursor_accepts_only_known_matching_paper_and_explains_conflicts(
        monkeypatch, preparation_session):
    payload, requests = preparation_session

    def execute(self, llm, plan):
        read, finish = plan.tools[1:]
        first = read('seed', max_results=5)
        assert requests[-1][2]['json']['max_results'] == 2
        cursor = first['next_cursor']
        with pytest.raises(ToolExecutionError, match='omit these conflicting fields: paper_id'):
            read('another-paper', cursor=cursor)
        with pytest.raises(ToolExecutionError, match='omit these conflicting fields: keywords, max_results'):
            read('seed', cursor=cursor, keywords=['Methods'], max_results=2)
        assert len(requests) == 1
        assert read('seed', cursor=cursor, positions=[])['positions'] == [2]
        assert requests[-1][2]['json'] == {'cursor': cursor, 'expansion_id': 'exp', 'preparation_revision': 3}
        read('seed', view='references', max_results=20)
        assert requests[-1][2]['json']['max_results'] == 5
        finish(**grounded_result())

    monkeypatch.setattr(preparation.AgentExecutor, 'run', execute)
    preparation.run_preparation(payload)


def test_preparation_reports_all_invalid_clues_in_one_repair_with_the_existing_guard(
        monkeypatch, preparation_session):
    from lazymind.chat.engine.agent_runtime.tool_call_guard import (
        DEFAULT_ARGUMENT_REPAIR_LIMIT, ToolExecutionMiddleware,
    )

    payload, _ = preparation_session
    corrected = grounded_result()
    corrected['named_clues'] = [copy.deepcopy(corrected['named_clues'][0]) for _ in range(17)]
    invalid = copy.deepcopy(corrected)
    invalid['named_clues'][0].update(question_ids=['unlinked'], source_version='unread-version')
    invalid['named_clues'][12]['source_excerpt'] = 'Earlier System has an invented first detail.'
    invalid['named_clues'][15]['source_excerpt'] = 'Earlier System has an invented second detail.'
    invalid['named_clues'][16]['name'] = 'Earlier System / an invented alias'

    def execute(self, llm, plan):
        middleware = ToolExecutionMiddleware(
            ToolManager(plan.tools), argument_limits={'finish_preparation': DEFAULT_ARGUMENT_REPAIR_LIMIT})

        def invoke(name, arguments, call_id):
            return middleware.execute_with_records([{
                'id': call_id, 'type': 'function',
                'function': {'name': name, 'arguments': json.dumps(arguments, ensure_ascii=False)},
            }]).results[0]

        invoke('read_paper', {'paper_id': 'seed'}, 'read')
        first = invoke('finish_preparation', invalid, 'invalid')
        feedback = json.dumps(first, ensure_ascii=False)
        assert all('named_clues[' + str(index) + ']' in feedback for index in (0, 12, 15, 16))
        assert 'valid research question_ids' in feedback and 'source paper/version' in feedback
        assert 'origin' in feedback and 'copy an exact excerpt' in feedback
        assert '𝑛win' in feedback and '\\ud835' not in feedback
        assert not is_oversized_tool_result(first)
        saved = invoke('finish_preparation', corrected, 'corrected')
        assert saved == {'ok': True, 'value': {'status': 'saved'}}
        assert not middleware.argument_failure

    monkeypatch.setattr(preparation.AgentExecutor, 'run', execute)
    result = preparation.run_preparation(payload)
    assert result['named_clues'] == corrected['named_clues']


def test_preparation_accepts_continuous_cross_block_excerpt_but_rejects_skipped_text(
        monkeypatch, preparation_session):
    payload, _ = preparation_session

    def execute(self, llm, plan):
        read, finish = plan.tools[1:]
        read('seed')
        read('seed', positions=[1, 2])
        skipped = grounded_result()
        skipped['named_clues'][0].update(
            positions=[0, 2],
            source_excerpt='Abstract and contents Related work and experiments compare Earlier System.')
        with pytest.raises(ToolExecutionError, match='source_excerpt is not in'):
            finish(**skipped)
        contiguous = grounded_result()
        contiguous['named_clues'][0].update(
            positions=[2, 1],
            source_excerpt='Its window is 𝑛win tokens. Related work and experiments compare Earlier System.')
        assert finish(**contiguous) == {'status': 'saved'}

    monkeypatch.setattr(preparation.AgentExecutor, 'run', execute)
    result = preparation.run_preparation(payload)
    assert result['named_clues'][0]['positions'] == [2, 1]
