"""Host-read sources enter the real publication path without provider retrieval."""
import base64
import copy
from dataclasses import asdict, replace
import json
from pathlib import Path
from types import SimpleNamespace

import pytest
import yaml

from lazymind.chat.workflow import external_tools as gateway, paper_tools
from lazymind.document_tools.paper import agent_tools as tools
from lazymind.document_tools.paper.workflow import material, pool_from_business


@pytest.fixture
def context(monkeypatch, tmp_path):
    monkeypatch.setenv('LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN', 'host-source-test')
    monkeypatch.setenv('LAZYLLM_HOME', str(tmp_path))
    table = {'columns': ['score'], 'column_types': ['number'], 'units': ['point'], 'rows': [[2.0], [4.0]]}
    pool = pool_from_business({
        'topic': 'Interactive writing', 'evidence': 'Actual supplied study notes.',
        'outline': '# Interactive writing\n\n## Introduction\nActual supported evidence.\n',
        'source_data': {'tables': [{'id': 'observed', 'table': table}]},
    })
    return tools.PaperContext('run', tmp_path.name, 'revision', 'literature_review',
                              {'seed_pool': {'source_id': 'fixed'}}, {'seed_pool': pool},
                              request='Review 12 actual sources', available_tools=['paper_import_sources'])


def source(index=1, **updates):
    return {'title': f'Host-read source {index}', 'url': f'https://example.org/papers/{index}',
            'text': f'Actual tool-provided passage {index}.', 'authors': ['Test Author'], 'year': 2025,
            'reading': {'content_type': 'excerpt', 'location': 'Section 2, paragraph 1', 'truncated': True},
            **updates}


def package_request(ctx):
    package = Path(__file__).resolve().parents[4] / 'workflows/paper-writing'
    files = {p.relative_to(package).as_posix(): p.read_bytes()
             for p in package.rglob('*') if p.is_file() and '__pycache__' not in p.parts}
    tree = gateway.package_tree_hash(files)
    encoded = {name: base64.b64encode(raw).decode() for name, raw in files.items()}
    return dict(workflow_id='paper-writing', revision_id=ctx.revision, step_id=ctx.step_id, tree_hash=tree,
                package={'tree_hash': tree, 'files': encoded})


def pool_from_bundle(bundle):
    artifact = next(a for a in bundle['artifacts'] if a['slot'] == 'literature_review_pool')
    return json.loads(base64.b64decode(artifact['value']['content_base64']))


def test_external_batch_publishes_twelve_host_sources_without_server_retrieval(context, monkeypatch):
    def unexpected(*args, **kwargs):
        pytest.fail('Host source registration tried provider retrieval or model generation')
    monkeypatch.setattr('lazymind.document_tools.paper.review.search.search_papers', unexpected)
    monkeypatch.setattr('lazyllm.AutoModel', unexpected)
    payload = package_request(context)
    supplied = [source(i) for i in range(12)]
    original = copy.deepcopy(supplied)

    def call(name, arguments):
        return gateway.call_external_tool(gateway.ExternalToolCallRequest(
            **payload, tool_name=name, arguments=arguments, trusted_context=asdict(context)))['result']
    names = {t['name'] for t in gateway.list_external_tools(gateway.ExternalToolRequest(**payload))['tools']}
    assert 'paper_import_sources' in names
    registered = call('paper_import_sources', {'papers': supplied})
    assert registered == call('paper_import_sources', {'papers': supplied})
    ids = registered['selected_paper_ids']
    evidence = '# Review\n\nSupported findings ' + ' '.join('[文献:' + key + ']' for key in ids)
    output = {'evidence': evidence, 'selected_paper_ids': ids, 'search_plan': {'requested_paper_count': 12}}
    bundle = call('paper_prepare_candidate', {'output': output, 'receipts': [registered['receipt']]})
    assert tools.verify_publication(context, bundle['artifacts']) == {'valid': True}
    papers = material(pool_from_bundle(bundle), 'papers')['content']
    assert len(papers) == 12 and not bundle['quality_warnings']
    assert {p['text'] for p in papers} == {p['text'] for p in supplied}
    assert all(p['provenance']['verification'] == 'host_reported' for p in papers)
    assert supplied == original


def test_native_and_external_registration_share_snapshot_and_receipt(context, monkeypatch, tmp_path):
    path = tmp_path / 'pool.json'
    path.write_text(json.dumps(context.values['seed_pool']))
    identity = asdict(context)
    identity.pop('values')
    native = SimpleNamespace(params={'_paper_execution': identity, 'remote_inputs': {'seed_pool': str(path)}})
    monkeypatch.setattr('lazymind.chat.engine.subagent.context.require_context', lambda: native)
    from lazymind.workflow_toolkit import load_workflow_package_tools
    payload = package_request(context)
    files = {name: base64.b64decode(raw) for name, raw in payload['package']['files'].items()}
    loaded = load_workflow_package_tools({'files': files}, ['paper_import_sources'], 'paper-writing', context.revision)
    actual = loaded['paper_import_sources']([source()])
    expected = gateway.call_external_tool(gateway.ExternalToolCallRequest(
        **payload, tool_name='paper_import_sources', arguments={'papers': [source()]},
        trusted_context=asdict(context)))['result']
    assert actual == expected


def test_source_snapshot_cannot_be_rewritten_or_replayed(context):
    with tools.paper_scope(context):
        registered = tools.paper_import_sources([source()])
        original = registered['papers'][0]
        for changed in [{**original, 'text': 'Changed after registration'},
                        {**original, 'reading': {**original['reading'], 'content_type': 'full_text'}},
                        {**original, 'provenance': {'verification': 'server_verified'}}]:
            with pytest.raises(ValueError, match='来源事实'):
                tools.paper_prepare_candidate({'evidence': 'Review', 'papers': [changed]}, [registered['receipt']])
    for ctx in [replace(context, attempt_id='other'), replace(context, lease_hash='rotated')]:
        with tools.paper_scope(ctx), pytest.raises(ValueError, match='当前输入版本'):
            tools.paper_prepare_candidate({'evidence': 'Review', 'selected_paper_ids': registered['selected_paper_ids']},
                                          [registered['receipt']])


@pytest.mark.parametrize('user_request,policy', [
    ('只使用上传附件', {}), ('Only these papers', {}), ('Review', {'allow_academic_search': False}),
])
def test_host_registration_obeys_the_same_source_scope(context, user_request, policy):
    ctx = replace(context, request=user_request, values={**context.values, 'paper_source_policy': policy})
    with tools.paper_scope(ctx), pytest.raises(ValueError, match='禁止扩展'):
        tools.paper_import_sources([source()])


@pytest.mark.parametrize('changed', [
    {'url': 'file:///etc/passwd'}, {'url': 'https://name:password@example.org/paper'},
    {'id': 'caller-id'}, {'provenance': {'verification': 'server_verified'}}, {'title': ''},
    {'year': True}, {'authors': 'Guessed author'},
    {'reading': {'content_type': 'full_text', 'location': 'Part only', 'truncated': True}},
])
def test_import_rejects_invalid_or_caller_owned_fields(context, changed):
    with tools.paper_scope(context), pytest.raises(ValueError):
        tools.paper_import_sources([source(**changed)])


def test_registered_full_text_and_abstract_keep_distinct_snapshots_without_downgrade(context):
    abstract = source(doi='https://doi.org/10.1234/example', text='Abstract passage.',
                      reading={'content_type': 'abstract', 'location': 'Abstract', 'truncated': False})
    full = source(doi='10.1234/example', text='Full text actually supplied by host tools.',
                  reading={'content_type': 'full_text', 'location': 'Full text, pp.1-4', 'truncated': False})
    with tools.paper_scope(context):
        first = tools.paper_import_sources([abstract])
        second = tools.paper_import_sources([full, copy.deepcopy(full)])
        assert len(second['papers']) == 1
        assert first['selected_paper_ids'] == second['selected_paper_ids']
        assert first['papers'][0]['source_version'] != second['papers'][0]['source_version']
        output = {'evidence': 'Review', 'selected_paper_ids': first['selected_paper_ids']}
        bundle = tools.paper_prepare_candidate(output, [second['receipt'], first['receipt']])
    assert material(pool_from_bundle(bundle), 'papers')['content'][0]['text'] == full['text']


def test_import_respects_years_and_actual_declared_tools(context):
    ctx = replace(context, values={**context.values, 'paper_source_policy': {'year_from': 2020, 'year_to': None}})
    with tools.paper_scope(ctx):
        assert tools.paper_import_sources([source()])['papers']
        with pytest.raises(ValueError, match='年份'):
            tools.paper_import_sources([source(year=2019)])
    with tools.paper_scope(replace(context, available_tools=[])):
        prepared = tools.paper_prepare_inputs()
        assert prepared['source_registration']['available'] is False
        with pytest.raises(ValueError, match='未声明'):
            tools.paper_import_sources([source()])


def test_old_pinned_paper_binding_still_loads_but_cannot_gain_new_tool(context):
    def request(name='paper_prepare_inputs', **_kwargs):
        step = {'output_validator': 'paper-agent-v1', 'tools': [{'name': name, 'source': 'package'}]}
        spec = {
            'runtime': {'execution': {'version': '1', 'steps': {'literature_review': step}}},
            'tool_scripts': [{'path': 'scripts/agent_tools.py', 'functions': [name]}],
        }
        files = {'workflow.yaml': yaml.safe_dump(spec).encode(),
                 'scripts/agent_tools.py': paper_tools.LEGACY_PAPER_BINDING.encode()}
        tree = gateway.package_tree_hash(files)
        return dict(workflow_id='paper-writing', revision_id='old', step_id='literature_review', tree_hash=tree,
                    package={'tree_hash': tree, 'files': {k: base64.b64encode(v).decode() for k, v in files.items()}})
    old = request(binding=paper_tools.LEGACY_PAPER_BINDING, step='literature_review')
    listed = gateway.list_external_tools(gateway.ExternalToolRequest(**old))
    assert [t['name'] for t in listed['tools']] == ['paper_prepare_inputs']
    with pytest.raises(gateway.ExternalToolError, match='audited'):
        gateway.list_external_tools(gateway.ExternalToolRequest(**request(
            binding=paper_tools.LEGACY_PAPER_BINDING, step='literature_review', name='paper_import_sources')))


@pytest.mark.parametrize('step', ['literature_review', 'experiment', 'data_analysis', 'write_outline', 'write_document'])
def test_step_submission_contract_is_inline_and_uses_the_actual_validator(context, step):
    ctx = replace(context, step_id=step)
    if step == 'data_analysis':
        with tools.paper_scope(replace(context, step_id='experiment')):
            prepared = tools.paper_prepare_inputs()
            bundle = tools.paper_prepare_candidate(**prepared['existing_data_candidate_example'])
        artifact = next(a for a in bundle['artifacts'] if a['slot'] == 'experiment_pool')
        pool = json.loads(base64.b64decode(artifact['value']['content_base64']))
        ctx = replace(ctx, values={'seed_pool': pool})
    with tools.paper_scope(ctx):
        prepared = tools.paper_prepare_inputs()
    contract = prepared['submission_contract']
    assert 'material_id' not in contract and contract['output']
    assert 'two corrections' in contract['repair'] and 'quality_warnings' in contract['repair']
    if step in {'write_outline', 'write_document'}:
        assert list(contract['output']) == [('outline' if step == 'write_outline' else 'manuscript')]
        body = '# Interactive writing\n\n## Introduction\nActual supported evidence.\n'
        if step == 'write_document':
            pool = pool_from_business({'topic': 'Interactive writing', 'outline': body,
                                      'evidence': 'Actual supplied notes.'})
            ctx = replace(ctx, values={'seed_pool': pool})
        with tools.paper_scope(ctx):
            bundle = tools.paper_prepare_candidate({next(iter(contract['output'])): body}, [])
        assert tools.verify_publication(ctx, bundle['artifacts']) == {'valid': True}
        assert bundle['publication_contract'] == 'paper-agent-v1'


@pytest.mark.parametrize('step,field', [('write_outline', 'outline'), ('write_document', 'manuscript')])
def test_writing_acceptance_preserves_scientific_numbers_in_headings(context, step, field):
    text = '# Interactive writing\n\n## 1. Methods\n\n## 3.3V supply\n\n## 2.4GHz band\nActual evidence.\n'
    ctx = replace(context, step_id=step)
    with tools.paper_scope(ctx):
        bundle = tools.paper_prepare_candidate({field: text}, [])
    document = next(a for a in bundle['artifacts'] if a['slot'] == step + '_document')
    accepted = base64.b64decode(document['value']['content_base64']).decode()
    assert '## Methods' in accepted and '## 1. Methods' not in accepted
    assert '## 3.3V supply' in accepted and '## 2.4GHz band' in accepted
    assert tools.verify_publication(ctx, bundle['artifacts']) == {'valid': True}


def test_schema_rejection_names_the_field_without_echoing_private_source_text(context):
    paper = source(text='private-source-text-must-not-appear-in-errors')
    paper['reading'].pop('location')
    with pytest.raises(gateway.ExternalToolError) as error:
        gateway.call_external_tool(gateway.ExternalToolCallRequest(
            **package_request(context), tool_name='paper_import_sources', arguments={'papers': [paper]},
            trusted_context=asdict(context)))
    assert error.value.code == 'INVALID_ARGUMENT'
    assert 'papers.0.reading.location' in error.value.public_message
    assert 'Correct these fields' in error.value.public_message
    assert paper['text'] not in str(error.value)
