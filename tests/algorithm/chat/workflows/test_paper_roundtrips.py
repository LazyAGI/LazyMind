"""Bounded batching keeps the same paper evidence and authorization boundaries."""
import base64
from dataclasses import asdict, replace
import json
from types import SimpleNamespace

import pytest
import yaml

from lazymind.document_tools.paper import agent_tools as tools
from lazymind.document_tools.paper.workflow import pool_from_business


@pytest.fixture
def context(monkeypatch, tmp_path):
    monkeypatch.setenv('LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN', 'paper-batch-test')
    monkeypatch.setenv('LAZYLLM_HOME', str(tmp_path))
    pool = pool_from_business({
        'topic': 'Bound observations', 'evidence': 'Observed values only.',
        'manuscript': '# Draft\n\nCited [文献:p1].',
        'papers': [{'id': 'p1', 'title': 'Actual source', 'text': 'Actual text. ' * 1000,
                    'citation': 'Actual source.'}],
        'source_data': {'tables': [{'id': 'observed', 'table': {
            'columns': ['value'], 'column_types': ['number'], 'units': ['s'], 'rows': [[1], [2]]}}]},
    })
    return tools.PaperContext('session', 'attempt', 'revision', 'write_outline',
                              {'seed_pool': {'source_revision': '1'}}, {'seed_pool': pool})


def test_preparation_inlines_exact_schemas_within_native_budget(context):
    ctx = replace(context, step_id='experiment')
    with tools.paper_scope(ctx):
        full = tools._prepared_inputs(ctx)
        prepared = tools.paper_prepare_inputs()
    assert prepared['result_meta_schema'] == full['result_meta_schema']
    assert prepared['existing_data_candidate_example'] == full['existing_data_candidate_example']
    assert len(tools.canonical(prepared)) < 10000


def test_material_batch_matches_single_reads_and_builds_pool_once(context, monkeypatch):
    identifiers = ['input:topic', 'contract:candidate_example', 'p1']
    with tools.paper_scope(context):
        single = [tools.paper_read_material(key, limit=100) for key in identifiers]
        calls = []
        original = tools.base_pool

        def measured(ctx):
            calls.append(ctx)
            return original(ctx)
        monkeypatch.setattr(tools, 'base_pool', measured)
        batch = tools.paper_read_material([
            {'material_id': key, 'limit': 100} for key in identifiers], limit=300)
    assert len(calls) == 1
    assert [{k: v for k, v in page.items() if k != 'material_id'} for page in batch['materials']] == single
    assert batch['total_characters_returned'] == sum(len(page['text']) for page in single)


def test_batch_budget_offsets_and_reviewer_visibility(context):
    with tools.paper_scope(context):
        result = tools.paper_read_material(['p1', 'input:topic'], limit=10)
        assert result['total_characters_returned'] == 10
        assert result['materials'][1]['text'] == ''
        assert result['materials'][1]['next_offset'] == 0
        resumed = tools.paper_read_material([{'material_id': 'p1', 'offset': 10, 'limit': 10}], limit=10)
        assert resumed['materials'][0]['text'] == tools.paper_read_material('p1', offset=10, limit=10)['text']
        with pytest.raises(ValueError):
            tools.paper_read_material([{'material_id': 'p1', 'owner': 'other'}])
    with tools.paper_scope(replace(context, step_id='peer_review')):
        with pytest.raises(ValueError, match='绑定'):
            tools.paper_read_material(['manuscript', 'input:source_data'])


def test_source_batch_preserves_receipts_and_rejects_unknown_or_expired_ids(context):
    with tools.paper_scope(context):
        papers = [{'id': 'r1', 'source': 'crossref', 'text': 'Actual abstract.', 'title': 'Source'},
                  {'id': 'r2', 'source': 'crossref', 'title': 'Unavailable source'}]
        receipt = tools.receipt_reference(context, 'papers', papers)
        result = tools.paper_read_paper(receipt, ['r1', 'r2'])
        assert result['papers'] == [papers[0]]
        assert result['failed'][0]['paper_id'] == 'r2'
        assert tools.unseal(context, result['receipt'], 'papers') == [papers[0]]
        with pytest.raises(ValueError, match='不属于'):
            tools.paper_read_paper(receipt, ['r1', 'unknown'])
    with tools.paper_scope(replace(context, lease_hash='reclaimed')):
        with pytest.raises(ValueError):
            tools.paper_read_paper(receipt, ['r1'])


def test_search_pagination_uses_same_scoped_snapshot(context, monkeypatch):
    from lazymind.document_tools.paper.review import materials, search
    calls = []

    def retrieve(query, **kwargs):
        calls.append(query)
        return {'sources': [], 'papers': [
            {'id': 'new1', 'title': 'First real source', 'source': 'crossref', 'abstract': 'First abstract.'},
            {'id': 'new2', 'title': 'Second real source', 'source': 'crossref', 'abstract': 'Second abstract.'}]}
    monkeypatch.setattr(materials, '_api_keys', lambda: {})
    monkeypatch.setattr(search, 'search_papers', retrieve)
    ctx = replace(context, step_id='literature_review')
    with tools.paper_scope(ctx):
        first = tools.paper_search('observations', count=1)
        second = tools.paper_search('observations', count=1, page=2, search_receipt=first['search_receipt'])
        assert first['receipt'] == first['search_receipt']
        assert tools.resolve_receipt(first['receipt'])['payload']['kind'] == 'papers'
        read = tools.paper_read_paper(first['search_receipt'], 'new1')
        assert read['paper']['id'] == 'new1'
        assert second['papers'][0]['id'] == 'new2'
        assert calls == ['observations']
        with pytest.raises(ValueError, match='翻页'):
            tools.paper_search('changed query', count=1, page=2, search_receipt=first['search_receipt'])
    with tools.paper_scope(replace(ctx, lease_hash='new lease')):
        with pytest.raises(ValueError):
            tools.paper_search('observations', count=1, page=2, search_receipt=first['search_receipt'])


def test_native_and_external_batch_keep_identical_bytes(context, monkeypatch, tmp_path):
    from lazymind.chat.workflow import external_tools as gateway
    from lazymind.chat.workflow.paper_tools import PAPER_BINDING
    path = tmp_path / 'pool.json'
    path.write_text(json.dumps(context.values['seed_pool']))
    identity = asdict(context)
    identity.pop('values')
    native = SimpleNamespace(params={'_paper_execution': identity, 'remote_inputs': {'seed_pool': str(path)}})
    monkeypatch.setattr('lazymind.chat.engine.subagent.context.require_context', lambda: native)
    arguments = {'material_id': ['input:topic', 'contract:candidate_example', 'p1'], 'limit': 4000}
    actual = tools.paper_read_material(**arguments)
    config = {'runtime': {'execution': {'version': '1', 'steps': {'write_outline': {
        'output_validator': 'paper-agent-v1', 'tools': [{'name': 'paper_read_material', 'source': 'package'}]}}}},
        'tool_scripts': [{'path': 'scripts/agent_tools.py', 'functions': ['paper_read_material']}]}
    files = {'workflow.yaml': yaml.safe_dump(config).encode(), 'scripts/agent_tools.py': PAPER_BINDING.encode()}
    tree = gateway.package_tree_hash(files)
    remote = gateway.call_external_tool(gateway.ExternalToolCallRequest(
        workflow_id='owned-resource', revision_id=context.revision, tree_hash=tree, step_id='write_outline',
        package={'tree_hash': tree, 'files': {k: base64.b64encode(v).decode() for k, v in files.items()}},
        tool_name='paper_read_material', arguments=arguments, trusted_context=asdict(context)))['result']
    assert actual == remote


@pytest.mark.parametrize('legacy_search_receipt', [False, True])
def test_literature_search_receipt_reads_and_publishes(context, monkeypatch, legacy_search_receipt):
    from lazymind.document_tools.paper.review import materials, search
    result = {'sources': [], 'papers': [
        {'id': 'new1', 'title': 'Actual source', 'source': 'crossref', 'abstract': 'Actual abstract.'}]}
    monkeypatch.setattr(materials, '_api_keys', lambda: {})
    monkeypatch.setattr(search, 'search_papers', lambda *args, **kwargs: result)
    ctx = replace(context, step_id='literature_review')
    with tools.paper_scope(ctx):
        searched = tools.paper_search('observations', count=1)
        receipt = searched['search_receipt']
        if legacy_search_receipt:
            receipt = tools.receipt_reference(ctx, 'search', {'identity': {}, 'result': result})
        assert tools.paper_read_paper(receipt, 'new1')['paper']['text'] == 'Actual abstract.'
        candidate = tools.paper_prepare_candidate({
            'evidence': '# Review\n\nObserved evidence [文献:new1].',
            'selected_paper_ids': ['new1'], 'search_plan': {'requested_paper_count': 1},
            'paper_source_policy': {'allow_academic_search': True}}, receipts=[receipt])
        assert tools.verify_publication(ctx, candidate['artifacts']) == {'valid': True}
        with pytest.raises(ValueError, match='真实绑定'):
            tools.paper_prepare_candidate({'evidence': 'Invented', 'selected_paper_ids': ['fake']}, [receipt])
    with tools.paper_scope(replace(ctx, lease_hash='reclaimed')):
        with pytest.raises(ValueError):
            tools.paper_read_paper(receipt, 'new1')
        with pytest.raises(ValueError):
            tools.paper_prepare_candidate({'evidence': 'Replay', 'selected_paper_ids': ['new1']}, [receipt])


def test_native_and_external_parallel_search_read_and_publish(context, monkeypatch, tmp_path):
    from lazymind.document_tools.paper.review import materials, search
    from lazymind.chat.workflow import external_tools as gateway
    from lazymind.chat.workflow.paper_tools import PAPER_BINDING
    calls = []

    def fetch(source, query, *args, **kwargs):
        calls.append((source, query))
        return [search._paper(source, query, abstract='Actual retrieved evidence.')]

    monkeypatch.setattr(materials, '_api_keys', lambda: {})
    monkeypatch.setattr(search, '_fetch', fetch)
    ctx = replace(context, step_id='literature_review')
    path = tmp_path / 'pool.json'
    path.write_text(json.dumps(ctx.values['seed_pool']))
    identity = asdict(ctx)
    identity.pop('values')
    native = SimpleNamespace(params={'_paper_execution': identity, 'remote_inputs': {'seed_pool': str(path)}})
    monkeypatch.setattr('lazymind.chat.engine.subagent.context.require_context', lambda: native)
    names = ['paper_search', 'paper_read_paper', 'paper_prepare_candidate']
    config = {'runtime': {'execution': {'version': '1', 'steps': {'literature_review': {
        'output_validator': 'paper-agent-v1', 'tools': [{'name': name, 'source': 'package'} for name in names]}}}},
        'tool_scripts': [{'path': 'scripts/agent_tools.py', 'functions': names}]}
    files = {'workflow.yaml': yaml.safe_dump(config).encode(), 'scripts/agent_tools.py': PAPER_BINDING.encode()}
    tree = gateway.package_tree_hash(files)

    def remote(name, arguments):
        return gateway.call_external_tool(gateway.ExternalToolCallRequest(
            workflow_id='owned-resource', revision_id=ctx.revision, tree_hash=tree, step_id=ctx.step_id,
            package={'tree_hash': tree, 'files': {k: base64.b64encode(v).decode() for k, v in files.items()}},
            tool_name=name, arguments=arguments, trusted_context=asdict(ctx)))['result']

    arguments = {'query': ['writing assistance', 'document revision'], 'count': 1}
    actual = tools.paper_search(**arguments)
    external = remote('paper_search', arguments)
    assert actual == external
    assert len(calls) == 8
    paged = remote('paper_search', {**arguments, 'page': 2, 'search_receipt': external['search_receipt']})
    assert paged['papers'][0]['id'] != actual['papers'][0]['id'] and len(calls) == 8
    paper_id = actual['papers'][0]['id']
    read_args = external['read_requests'][paper_id]
    assert tools.paper_read_paper(**read_args) == remote('paper_read_paper', read_args)
    output = {'evidence': f'Actual evidence [文献:{paper_id}].', 'selected_paper_ids': [paper_id]}
    for candidate in (tools.paper_prepare_candidate(output, [actual['receipt']]),
                      remote('paper_prepare_candidate', {'output': output, 'receipts': [external['receipt']]})):
        assert tools.verify_publication(ctx, candidate['artifacts']) == {'valid': True}
    with tools.paper_scope(replace(ctx, lease_hash='reclaimed')):
        with pytest.raises(ValueError):
            tools.paper_read_paper(**read_args)
        with pytest.raises(ValueError):
            tools.paper_search(**arguments, page=2, search_receipt=external['search_receipt'])


@pytest.mark.parametrize('query,kwargs', [
    ([], {}), (['same', 'same'], {}), (['a', 'b', 'c', 'd'], {}),
    (['valid', 'this query has far too many English words'], {}),
    (['valid', 'other'], {'arxiv_terms': ['custom terms']}),
])
def test_invalid_batch_never_starts_retrieval(context, monkeypatch, query, kwargs):
    from lazymind.document_tools.paper.review import search
    monkeypatch.setattr(search, 'search_papers', lambda *a, **k: pytest.fail('Invalid search was dispatched'))
    with tools.paper_scope(replace(context, step_id='literature_review')):
        with pytest.raises(ValueError):
            tools.paper_search(query, **kwargs)


def test_attachment_only_policy_blocks_whole_batch(context, monkeypatch):
    from lazymind.document_tools.paper.review import search
    monkeypatch.setattr(search, 'search_papers', lambda *a, **k: pytest.fail('Forbidden retrieval was dispatched'))
    ctx = replace(context, step_id='literature_review',
                  values={**context.values, 'paper_source_policy': {'allow_academic_search': False}})
    with tools.paper_scope(ctx):
        with pytest.raises(ValueError, match='禁止扩展'):
            tools.paper_search(['writing assistance', 'document revision'])
