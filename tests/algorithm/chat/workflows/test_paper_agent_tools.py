"""Behavioral coverage for the shared model-free paper Agent boundary."""
import base64
import copy
from dataclasses import replace
from types import SimpleNamespace

import pytest

from lazymind.document_tools.paper import agent_tools as tools
from lazymind.document_tools.paper.workflow import material, pool_from_business


@pytest.fixture
def paper_context(monkeypatch, tmp_path):
    monkeypatch.setenv('LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN', 'isolated-paper-test-key')
    monkeypatch.setenv('LAZYLLM_HOME', str(tmp_path / 'paper-runtime'))
    pool = pool_from_business({'topic': 'Document state', 'evidence': 'Observed document revision histories.'})
    pool['future_extension'] = {'keep': [1, 2]}
    return tools.PaperContext('run', tmp_path.name, 'revision', 'write_outline',
                              {'seed_pool': {'source_id': 'pool-v1'}}, {'seed_pool': pool},
                              request='Write an English outline', launch_request='Write an English paper')


def outline():
    return {'outline': '# Document State\n\n## Introduction\nResearch problem.\n\n## Methods\nObserved revisions.\n'}


def pool_from_bundle(bundle, step='write_outline'):
    item = next(item for item in bundle['artifacts'] if item['slot'] == step + '_pool')
    import json
    return json.loads(base64.b64decode(item['value']['content_base64']))


def test_outline_prepares_versions_and_lineage_without_a_model(paper_context, monkeypatch):
    def no_model(*args, **kwargs):
        raise AssertionError('paper primitive attempted model generation')
    monkeypatch.setattr('lazyllm.AutoModel', no_model)
    with tools.paper_scope(paper_context):
        prepared = tools.paper_prepare_inputs()
        result = tools.paper_prepare_candidate(outline(), receipts=[])
    assert prepared['expected_output_fields'] == ['outline']
    accepted = pool_from_bundle(result)
    assert accepted['future_extension'] == {'keep': [1, 2]}
    assert accepted['sequence'] == paper_context.values['seed_pool']['sequence'] + 1
    assert accepted['history'][-1]['step'] == 'write_outline'
    assert material(accepted, 'outline')['content'] == outline()['outline']
    assert material(paper_context.values['seed_pool'], 'outline') is None
    assert tools.verify_publication(paper_context, result['artifacts']) == {'valid': True}


def test_signed_bundle_cannot_be_tampered_or_replayed(paper_context):
    with tools.paper_scope(paper_context):
        result = tools.paper_prepare_candidate(outline(), receipts=[])
    changed = copy.deepcopy(result['artifacts'])
    document = next(item for item in changed if item['slot'] == 'write_outline_document')
    document['value']['content_base64'] = base64.b64encode(b'# Replaced after validation').decode()
    with pytest.raises(ValueError, match='内容不一致'):
        tools.verify_publication(paper_context, changed)
    with pytest.raises(ValueError, match='当前输入版本'):
        tools.verify_publication(replace(paper_context, attempt_id='new-attempt'), result['artifacts'])
    with pytest.raises(ValueError, match='当前输入版本'):
        tools.verify_publication(replace(paper_context, bindings={'seed_pool': {'source_id': 'pool-v2'}}),
                                 result['artifacts'])


def test_raw_markdown_is_not_a_pool_but_material_tables_are_exact(paper_context):
    raw = '# User study\n\n| method | time (s) |\n|---|---|\n| A | 2 |\n| B | 3 |\n'
    with tools.paper_scope(replace(paper_context, values={'seed_pool': raw})):
        with pytest.raises(ValueError, match='paper-material-pool.v2'):
            tools.paper_prepare_inputs()
    ctx = replace(paper_context, values={'paper_topic': 'Document state',
                                         'research_materials': [{'filename': 'observations.md', 'text': raw}]})
    first, second = tools.base_pool(ctx), tools.base_pool(ctx)
    assert first == second
    data = material(first, 'source_data')['content']['tables'][0]['table']
    assert data['rows'] == [['A', 2], ['B', 3]]
    assert data['units'] == [None, 's']


def test_native_and_external_adapters_share_candidate_bytes(paper_context, monkeypatch, tmp_path):
    import json
    source = tmp_path / 'pool.json'
    source.write_text(json.dumps(paper_context.values['seed_pool']))
    identity = {key: getattr(paper_context, key) for key in
                ('session_id', 'attempt_id', 'revision', 'step_id', 'bindings', 'request', 'launch_request')}
    native = SimpleNamespace(params={'_paper_execution': identity, 'remote_inputs': {'seed_pool': str(source)}},
                             workspace_path=str(tmp_path))
    # Import before replacing the context accessor; imports must not capture a test stub.
    from lazymind.chat.engine.subagent import tools as native_tools
    monkeypatch.setattr('lazymind.chat.engine.subagent.context.require_context', lambda: native)
    saved = []
    monkeypatch.setattr(native_tools, 'save_artifacts',
                        lambda artifacts: saved.extend(artifacts) or {'status': 'ok'})
    with tools.paper_scope(paper_context):
        external = tools.paper_prepare_candidate(outline(), receipts=[])
    tools.paper_submit_candidate(outline(), receipts=[])
    actual = []
    for item in saved:
        value = item['value']
        if item['content_type'] == 'file':
            from pathlib import Path
            value = {'name': Path(value).name,
                     'content_base64': base64.b64encode(Path(value).read_bytes()).decode()}
        actual.append({'slot': item['key'], 'seq': 1, 'content_type': item['content_type'], 'value': value})
    assert tools.verify_publication(paper_context, actual) == {'valid': True}
    assert tools._manifest(actual[:-1]) == tools._manifest(external['artifacts'][:-1])


@pytest.mark.parametrize('user_request,policy', [
    ('只使用上传附件', {}), ('Only these papers', {}), ('Review literature', {'allow_academic_search': False}),
])
def test_source_only_scope_cannot_expand(paper_context, user_request, policy):
    ctx = replace(paper_context, step_id='literature_review', request=user_request,
                  values={**paper_context.values, 'paper_source_policy': policy})
    with tools.paper_scope(ctx):
        with pytest.raises(ValueError, match='禁止扩展'):
            tools.paper_search('document revision')


def test_fabricated_or_changed_sources_are_rejected(paper_context):
    paper = {'id': 'p1', 'title': 'Actual title', 'text': 'Actual source', 'citation': 'Actual title.'}
    pool = pool_from_business({'topic': 'Topic', 'papers': [paper]})
    ctx = replace(paper_context, step_id='literature_review', values={'seed_pool': pool})
    with tools.paper_scope(ctx):
        for changed in ({**paper, 'id': 'fake'}, {**paper, 'text': 'Invented result'}):
            with pytest.raises(ValueError, match='来源事实|真实检索回执'):
                tools.paper_prepare_candidate({'evidence': 'Review [p1]', 'papers': [changed]}, receipts=[])


def test_experiment_cannot_self_authorize(paper_context):
    ctx = replace(paper_context, step_id='experiment')
    with tools.paper_scope(ctx):
        with pytest.raises(ValueError, match='未明确授权'):
            tools.paper_run_experiment('def run(config, data): return []', {})


def test_read_material_pages_are_real_and_cannot_escape_bindings(paper_context):
    pool = pool_from_business({'topic': 'Topic', 'papers': [
        {'id': 'p1', 'title': 'Paper', 'text': 'abcdefghij'}]})
    with tools.paper_scope(replace(paper_context, values={'seed_pool': pool})):
        first = tools.paper_read_material('p1', limit=4)
        assert first['text'] == 'abcd' and first['next_offset'] == 4
        assert tools.paper_read_material('p1', offset=4, limit=6)['text'] == 'efghij'
        with pytest.raises(ValueError, match='绑定'):
            tools.paper_read_material('/etc/passwd')


@pytest.mark.parametrize('dataset', [
    {'source': 'input', 'title': 'observations.csv', 'text': 'id,value\n1,2'},
    {'title': 'observations.csv', 'text': 'Raw observations', 'source_tables': [{'id': 'raw'}]},
])
def test_reviewer_cannot_read_hidden_pool_or_data_attachments(paper_context, dataset):
    import json
    pool = pool_from_business({
        'topic': 'Review', 'manuscript': '# Paper\n\nCited [文献:p1]. Data observations.csv.',
        'papers': [{'id': 'p1', 'title': 'Cited study', 'text': 'Actual reference.', 'citation': 'Cited study.'},
                   {'id': 'dataset', **dataset}, {'id': 'unused', 'title': 'Unused', 'text': 'Hidden paper.'}],
        'evidence': 'Hidden upstream interpretation',
        'source_data': {'tables': [{'id': 'raw', 'table': {
            'columns': ['value'], 'column_types': ['number'], 'units': [None], 'rows': [[2]]}}]},
    })
    original = copy.deepcopy(pool)
    ctx = replace(paper_context, step_id='peer_review', values={'seed_pool': pool})
    with tools.paper_scope(ctx):
        prepared = tools.paper_prepare_inputs()
        assert {entry['material_id'] for entry in prepared['material_catalog']} == {'manuscript', 'papers'}
        assert [paper['id'] for paper in prepared['input']['papers']] == ['p1']
        assert json.loads(tools.paper_read_material('papers')['text']) == prepared['input']['papers']
        assert tools.paper_read_material('p1')['text'] == 'Actual reference.'
        assert tools.paper_read_material('manuscript')['text'] == prepared['input']['manuscript']
        for hidden in ('dataset', 'unused', 'source_data', 'evidence'):
            with pytest.raises(ValueError, match='绑定'):
                tools.paper_read_material(hidden)
    assert pool == original
    with tools.paper_scope(replace(ctx, step_id='write_outline')):
        assert tools.paper_read_material('source_data')['text']
        assert tools.paper_read_material('dataset')['text'] == dataset['text']


def test_external_worker_cannot_submit_or_forge_a_publication(paper_context):
    with tools.paper_scope(paper_context):
        with pytest.raises(ValueError, match='不能保存'):
            tools.paper_submit_candidate(outline(), receipts=[])
    with pytest.raises(ValueError, match='唯一'):
        tools.verify_publication(paper_context, [])


def test_finish_and_native_json_envelopes(paper_context):
    ctx = replace(paper_context, step_id='finish')
    with tools.paper_scope(ctx):
        result = tools.paper_prepare_candidate({}, receipts=[])
    result['artifacts'][-1]['value'] = {'data': result['artifacts'][-1]['value']}
    assert tools.verify_publication(ctx, result['artifacts']) == {'valid': True}


@pytest.mark.parametrize('step', [
    'research_topic', 'literature_review', 'refine_topic', 'experiment', 'data_analysis',
    'write_outline', 'write_document', 'peer_review', 'revise_document', 'reading_reflection', 'finish',
])
def test_all_step_inputs_are_model_free_and_versioned(paper_context, monkeypatch, prepared_analysis_pool, step):
    import json
    from pathlib import Path
    def no_model(*args, **kwargs):
        raise AssertionError('A paper operation must not ask Runtime to generate content')
    monkeypatch.setattr('lazyllm.AutoModel', no_model)
    if step == 'finish':
        ctx = replace(paper_context, step_id=step)
    else:
        source = json.loads((Path(__file__).resolve().parents[4]
                             / 'tests/fixtures/paper-writing' / (step + '.json')).read_text())['input']
        source.pop('mode', None)
        pool = prepared_analysis_pool(source) if step == 'data_analysis' else pool_from_business(source)
        ctx = replace(paper_context, step_id=step, values={'seed_pool': pool},
                      request=source.get('request', ''))
    with tools.paper_scope(ctx):
        prepared = tools.paper_prepare_inputs()
    assert prepared['step'] == step
    assert prepared['inputs_digest'] == tools.digest(ctx.scope)
    assert prepared['launch_request'] == ctx.launch_request
    if step == 'data_analysis':
        import json
        with tools.paper_scope(ctx):
            code = json.loads(tools.paper_read_material(prepared['code_contract']['material_id'], limit=64000)['text'])
            plot = json.loads(tools.paper_read_material(
                prepared['plot_code_contract']['material_id'], limit=64000)['text'])
        assert 'results' in code['return_schema']['properties']
        assert plot['required_function'] == 'draw(data, statistics, spec)'
        assert 'never default' in plot['finding_metadata']


@pytest.mark.parametrize('step,field', [
    ('research_topic', 'question'), ('refine_topic', 'question'),
    ('reading_reflection', 'reflection'), ('write_document', 'manuscript'),
])
def test_generated_steps_share_business_validation_and_history(paper_context, step, field):
    papers = [{'id': 'p1', 'title': 'Source', 'text': 'Actual text', 'citation': 'Source.'}]
    pool = pool_from_business({'topic': 'Study', 'papers': papers, 'evidence': 'Known evidence',
                              'outline': '# Study\n\n## Methods\nDiscuss source.'})
    ctx = replace(paper_context, step_id=step, values={'seed_pool': pool})
    output = {field: '# Study\n\n## Methods\nDiscuss actual evidence [文献:p1].'}
    with tools.paper_scope(ctx):
        result = tools.paper_prepare_candidate(output, receipts=[])
    stored = pool_from_bundle(result, step)
    assert material(stored, field)['content']
    assert stored['history'][-1]['step'] == step
    assert tools.verify_publication(ctx, result['artifacts']) == {'valid': True}


def test_outline_and_preparation_gates_cannot_be_invented(paper_context):
    for step, message in [('write_document', '已确认的大纲'), ('data_analysis', 'F3')]:
        with tools.paper_scope(replace(paper_context, step_id=step)):
            with pytest.raises(ValueError, match=message):
                tools.paper_prepare_inputs()


def test_launch_source_limits_survive_followup_and_old_lease_is_rejected(paper_context):
    ctx = replace(paper_context, step_id='literature_review', launch_request='Only the attachments',
                  request='Continue the review')
    with tools.paper_scope(ctx):
        with pytest.raises(ValueError, match='禁止扩展'):
            tools.paper_search('document revisions')
    with tools.paper_scope(paper_context):
        bundle = tools.paper_prepare_candidate(outline(), receipts=[])
    with pytest.raises(ValueError, match='当前输入版本'):
        tools.verify_publication(replace(paper_context, lease_hash='new-lease'), bundle['artifacts'])


def test_revision_patches_use_actual_source_ranges_and_preserve_unchanged_text():
    source = '# Paper\n\n## Methods\nActual data.\n\n## Limitations\nSmall sample.\n'
    plan = {'scope': 'span', 'instructions': [{'modify_type': 'update', 'target_scope': 'fragment',
                             'content_ref': {'heading_path': ['Paper', 'Limitations']},
                             'locator_text': 'Small sample.', 'instruction': 'Clarify scope.'}]}
    patches = {'replacements': [{'old_string': 'Small sample.', 'new_string': 'Small sample; no generalization.'}]}
    inputs = {'manuscript': source}
    assert tools.apply_revision(inputs, None, None) == source
    updated = tools.apply_revision(inputs, plan, patches)
    assert updated == source.replace('Small sample.', 'Small sample; no generalization.')
    patches['replacements'][0] = {'old_string': 'Actual data.', 'new_string': 'Invented data.'}
    with pytest.raises(ValueError, match='scope'):
        tools.apply_revision(inputs, plan, patches)


def test_review_and_unchanged_revision_build_fresh_hashes(paper_context):
    from lazymind.document_tools.paper.review.schema import text_digest
    manuscript = '# Paper\n\n## Methods\nLimited observations.\n'
    pool = pool_from_business({'topic': 'Paper', 'manuscript': manuscript})
    candidate = {'review_result': {'summary': 'Limited supplied evidence.', 'issues': [],
                                  'limitations': ['No independent experiment was performed.']},
                 'literature_readings': {'readings': []}}
    for step in ('peer_review', 'revise_document'):
        ctx = replace(paper_context, step_id=step, values={'seed_pool': pool})
        with tools.paper_scope(ctx):
            result = tools.paper_prepare_candidate(candidate, receipts=[])
        stored = pool_from_bundle(result, step)
        assert material(stored, 'review_data')['content']['manuscript_sha256'] == text_digest(manuscript)
        assert material(stored, 'manuscript')['content'] == manuscript
        assert tools.verify_publication(ctx, result['artifacts']) == {'valid': True}


def test_real_native_publication_uses_same_receipt(paper_context, monkeypatch, tmp_path):
    import json
    import lazyllm
    from pathlib import Path
    from lazymind.chat.engine.subagent.context import SubAgentContext
    source = tmp_path / 'source.json'
    source.write_text(json.dumps(paper_context.values['seed_pool']))
    identity = {key: getattr(paper_context, key) for key in
                ('session_id', 'attempt_id', 'revision', 'step_id', 'bindings', 'request', 'launch_request')}
    events = []
    slots = ['write_outline_pool', 'write_outline_document', 'write_outline_receipt']
    native = SubAgentContext(task_id='paper-native-test', conversation_id='paper-native-test',
                             agent_type='workflow_step', objective='Write outline',
                             params={'_paper_execution': identity, 'remote_inputs': {'seed_pool': str(source)},
                                     'output_slot_types': dict(zip(slots, ['file', 'file', 'json']))},
                             workspace_path=str(tmp_path), input_slots=['seed_pool'], output_slots=slots,
                             db=None, emit=events.append)
    previous = lazyllm.globals.get('subagent_ctx')
    lazyllm.globals['subagent_ctx'] = native
    try:
        result = tools.paper_submit_candidate(outline(), receipts=[])
        assert result['_agent_control']['stop'] is True
        actual = []
        for event in events:
            if event['type'] != 'artifact':
                continue
            value = event['value']
            if event['content_type'] == 'file':
                value = {'name': value['filename'],
                         'content_base64': base64.b64encode(Path(value['path']).read_bytes()).decode()}
            actual.append({**event, 'value': value})
        assert tools.verify_publication(paper_context, actual) == {'valid': True}
    finally:
        lazyllm.globals['subagent_ctx'] = previous


def test_computation_replay_and_status_do_not_execute_twice(paper_context, tmp_path, monkeypatch):
    monkeypatch.setenv('LAZYLLM_HOME', str(tmp_path))
    calls = []

    @tools.execute_once
    def computation(value):
        calls.append(value)
        return {'actual': value}

    with tools.paper_scope(paper_context):
        first = computation(3)
        assert computation(3) == first
        status = tools.paper_execution_status(first['operation_id'])
        assert status['result'] == first and status['state'] == 'completed'
        assert tools.paper_cancel_execution(first['operation_id'])['cancel_requested'] is False
    assert calls == [3]
    with tools.paper_scope(replace(paper_context, attempt_id='another')):
        with pytest.raises(ValueError, match='不属于'):
            tools.paper_execution_status(first['operation_id'])


def test_real_external_registry_uses_same_candidate_function(paper_context):
    from dataclasses import asdict
    from pathlib import Path
    from lazymind.chat.workflow.external_tools import (
        ExternalToolRequest, ExternalToolCallRequest, list_external_tools, call_external_tool, package_tree_hash,
    )
    package = Path(__file__).resolve().parents[4] / 'workflows/paper-writing'
    contents = {path.relative_to(package).as_posix(): path.read_bytes()
                for path in package.rglob('*') if path.is_file() and '__pycache__' not in path.parts}
    tree = package_tree_hash(contents)
    request = dict(workflow_id='paper-writing', revision_id=paper_context.revision, tree_hash=tree,
                   step_id='write_outline', package={'tree_hash': tree, 'files': {
                       name: base64.b64encode(raw).decode() for name, raw in contents.items()}})
    names = {item['name'] for item in list_external_tools(ExternalToolRequest(**request))['tools']}
    assert 'paper_prepare_candidate' in names
    assert 'paper_submit_candidate' not in names and 'run_paper_step' not in names
    result = call_external_tool(ExternalToolCallRequest(**request, tool_name='paper_prepare_candidate',
                                trusted_context=asdict(paper_context), arguments={'output': outline(), 'receipts': []}))
    assert tools.verify_publication(paper_context, result['result']['artifacts']) == {'valid': True}


def test_literature_publication_preserves_real_source_identity(paper_context):
    source = {'id': 'ref-real', 'title': 'Actual source', 'text': 'A real supplied passage.',
              'citation': 'Author. Actual source. 2025.', 'reading': {'content_type': 'provided_text', 'truncated': False}}
    ctx = replace(paper_context, step_id='literature_review',
                  values={'seed_pool': pool_from_business({'topic': 'Study', 'papers': [source]})})
    with tools.paper_scope(ctx):
        result = tools.paper_prepare_candidate({'evidence': '# Review\n\nSupported scope [文献:ref-real].',
                                                'papers': [source]}, receipts=[])
    pool = pool_from_bundle(result, 'literature_review')
    assert material(pool, 'papers')['content'] == [source]
    assert '#paper-ref-ref-real' in material(pool, 'evidence')['content']
    assert tools.verify_publication(ctx, result['artifacts']) == {'valid': True}


def test_existing_data_preparation_cannot_change_measurements(paper_context):
    table = {'columns': ['score'], 'column_types': ['number'], 'units': ['s'], 'rows': [[2.0], [3.0]]}
    ctx = replace(paper_context, step_id='experiment', values={'seed_pool': pool_from_business({
        'topic': 'Study', 'data': table})})
    output = {'experiment': {'executed': False, 'plan': 'Use supplied observations.',
                             'results': table, 'report': 'No new experiment or audit was performed.'},
              'code_files': [], 'result_meta': {
                  'execution_status': 'not_run', 'data_origin': 'user_supplied', 'method': 'empirical',
                  'reason': 'Supplied observations.', 'assumptions': [], 'metrics': [], 'seed': None,
                  'column_types': {'score': 'number'}, 'input_table_ids': ['data']}}
    with tools.paper_scope(ctx):
        bundle = tools.paper_prepare_candidate(output, receipts=[])
        changed = copy.deepcopy(output)
        changed['experiment']['results']['rows'][0][0] = 99.0
        with pytest.raises(ValueError, match='原始数值'):
            tools.paper_prepare_candidate(changed, receipts=[])
    pool = pool_from_bundle(bundle, 'experiment')
    assert material(pool, 'experiment')['content']['results'] == table
    assert pool['history'][-1]['node'] == 'F3'


def test_no_knowledge_selection_never_queries_live_defaults(paper_context, monkeypatch):
    import httpx
    monkeypatch.setattr(httpx, 'post', lambda *a, **k: pytest.fail('unselected knowledge queried'))
    with tools.paper_scope(paper_context):
        assert tools.paper_knowledge_documents()['status'] == 'no_selected_knowledge'


def test_json_attachment_tables_preserve_cells_and_reject_conflicting_ids(paper_context):
    table = {'columns': ['condition', 'value'], 'column_types': ['string', 'number'],
             'units': [None, 'ms'], 'rows': [['A', 2.5], ['B', None]]}
    ctx = replace(paper_context, values={'paper_topic': 'Study', 'research_materials': [table]})
    pool = tools.base_pool(ctx)
    assert material(pool, 'source_data')['content']['tables'][0]['table'] == table
    first = {'id': 'observed', 'description': 'Actual', 'table': table}
    changed = copy.deepcopy(first)
    changed['table']['rows'][0][1] = 100.0
    ctx = replace(ctx, values={**ctx.values, 'research_materials': [
        {'tables': [first]}, {'tables': [changed]}]})
    with pytest.raises(ValueError, match='ID 冲突'):
        tools.base_pool(ctx)


def test_native_tool_schemas_and_spill_reader_keep_restricted_surface(paper_context):
    from lazyllm.tools.agent.toolsManager import ToolManager
    manager = ToolManager([tools.paper_execution_status, tools.paper_search, tools.paper_prepare_candidate])
    assert manager.tools_info['paper_execution_status'].validate_parameters({'operation_id': 'a' * 64})
    assert manager.tools_info['paper_search'].validate_parameters({'query': 'revision consistency'})
    assert manager.tools_info['paper_prepare_candidate'].validate_parameters({'output': outline(), 'receipts': []})
    with pytest.raises(ValueError, match='tool_spills'):
        tools.paper_read_result('/etc/passwd')
    with tools.paper_scope(paper_context):
        with pytest.raises(ValueError, match='tool_spills'):
            tools.paper_read_result('workspace://tool_spills/anything.txt')


@pytest.mark.parametrize('words', [4105, 5766])
def test_manuscript_quality_checks_warn_without_blocking_publication(paper_context, words):
    pool = pool_from_business({'topic': 'Study', 'evidence': 'Known evidence',
                              'constraints': {'paper_language': 'en', 'word_target': 5000,
                                              'required_sections': ['conclusion']},
                              'outline': '# Study\n\n## Methods\nDescribe observations.'})
    ctx = replace(paper_context, step_id='write_document', values={'seed_pool': pool})
    manuscript = '# Study\n\n### Observations\n' + 'evidence ' * words
    with tools.paper_scope(ctx):
        result = tools.paper_prepare_candidate({'manuscript': manuscript}, receipts=[])
    assert result['status'] == 'validated'
    assert any('篇幅' in warning for warning in result['quality_warnings'])
    assert any('大纲' in warning for warning in result['quality_warnings'])
    assert any('章节' in warning for warning in result['quality_warnings'])
    stored = pool_from_bundle(result, 'write_document')
    assert stored['history'][-1]['quality_warnings'] == result['quality_warnings']
    assert 'evidence' in material(stored, 'manuscript')['content']
    assert tools.verify_publication(ctx, result['artifacts']) == {'valid': True}
    with tools.paper_scope(ctx), pytest.raises(ValueError):
        tools.paper_prepare_candidate({'manuscript': manuscript + '\n[文献:invented-source]'}, receipts=[])


def test_new_reading_sources_are_recorded_from_receipts(paper_context):
    source = {'id': 'retrieved-real', 'title': 'Actual retrieval', 'text': 'Bound actual passage.',
              'citation': 'Author. Actual retrieval.', 'source': 'sciverse'}
    ctx = replace(paper_context, step_id='reading_reflection')
    with tools.paper_scope(ctx):
        receipt = tools.seal(ctx, 'papers', [source])
        result = tools.paper_prepare_candidate({
            'reflection': '# Reading\n\nA limited reflection [文献:retrieved-real].',
            'selected_paper_ids': ['retrieved-real']}, receipts=[receipt])
    pool = pool_from_bundle(result, 'reading_reflection')
    assert material(pool, 'papers')['content'] == [source]
    assert pool['current']['papers'] in pool['history'][-1]['outputs']


def test_revision_preview_and_submission_bind_the_final_reference_bytes(paper_context):
    from lazymind.document_tools.paper.review.schema import text_digest
    manuscript = '# Paper\n\n## Methods\nLimited observations.\n'
    source = {'id': 'p1', 'title': 'Source', 'text': 'Limited supplied observations.', 'citation': 'Author. Source.'}
    pool = pool_from_business({'topic': 'Study', 'manuscript': manuscript, 'papers': [source]})
    ctx = replace(paper_context, step_id='revise_document', values={'seed_pool': pool})
    plan = {'scope': 'span', 'instructions': [{'modify_type': 'update', 'target_scope': 'fragment',
                                             'content_ref': {'heading_path': ['Paper', 'Methods']},
                                             'locator_text': 'Limited observations.', 'instruction': 'Cite source.'}]}
    replacements = {'replacements': [{'old_string': 'Limited observations.',
                                      'new_string': 'Limited observations [文献:p1].'}]}
    with tools.paper_scope(ctx):
        preview = tools.paper_preview_revision(plan, replacements)
        assert '#paper-ref-p1' in preview['manuscript']
        bundle = tools.paper_prepare_candidate({
            'modify_plan': plan, 'replacements': replacements,
            'review_result': {'summary': 'Source reference added.', 'issues': [],
                              'limitations': ['No independent verification was performed.']}}, receipts=[])
    stored = pool_from_bundle(bundle, 'revise_document')
    assert material(stored, 'manuscript')['content'] == preview['manuscript']
    assert material(stored, 'review_data')['content']['manuscript_sha256'] == text_digest(preview['manuscript'])
    assert tools.verify_publication(ctx, bundle['artifacts']) == {'valid': True}


def test_unknown_operation_is_not_reexecuted_or_claimed_cancelled(paper_context, monkeypatch, tmp_path):
    monkeypatch.setenv('LAZYLLM_HOME', str(tmp_path))
    calls = []

    @tools.execute_once
    def operation(value):
        calls.append(value)
        return {'value': value}

    key = tools.digest({'scope': paper_context.scope, 'tool': 'operation', 'arguments': {'value': 1}})
    tools.save_operation(tools.execution_directory() / (key + '.json'),
                         {'state': 'running', 'scope_digest': tools.digest(paper_context.scope)})
    with tools.paper_scope(paper_context):
        assert tools.paper_execution_status(key)['state'] == 'running_or_unknown'
        assert tools.paper_cancel_execution(key)['cancel_requested']
        assert tools.paper_execution_status(key)['state'] == 'running_or_unknown'
        with pytest.raises(ValueError, match='结果未知'):
            operation(1)
    assert calls == []


def test_f4_retains_bound_analysis_and_charts_in_exact_bundle(paper_context, prepared_analysis_pool):
    import json
    from pathlib import Path
    from lazymind.document_tools.paper.workflow import add_material
    fixture = json.loads((Path(__file__).resolve().parents[4]
                          / 'tests/fixtures/paper-writing/data_analysis.json').read_text())
    pool = prepared_analysis_pool(fixture['input'])
    for kind in ('analysis', 'charts'):
        add_material(pool, kind, fixture['output'][kind], [], [])
    ctx = replace(paper_context, step_id='data_analysis', values={'seed_pool': pool},
                  request='Only redraw the figure colors.', launch_request='')
    output = {**fixture['output'], 'redraw_only': True}
    with tools.paper_scope(ctx):
        bundle = tools.paper_prepare_candidate(output, receipts=[])
        invalid = copy.deepcopy(output)
        invalid['analysis']['summary'] += ' Changed scientific interpretation.'
        with pytest.raises(ValueError, match='冻结分析'):
            tools.paper_prepare_candidate(invalid, receipts=[])
    stored = pool_from_bundle(bundle, 'data_analysis')
    assert material(stored, 'analysis')['content'] == fixture['output']['analysis']
    assert material(stored, 'charts')['content'] == fixture['output']['charts']
    assert any(item['slot'] == 'analysis_execution' for item in bundle['artifacts'])
    assert tools.verify_publication(ctx, bundle['artifacts']) == {'valid': True}


def test_finish_requires_no_new_research_material_or_request(paper_context):
    ctx = replace(paper_context, step_id='finish', values={}, request='', launch_request='')
    with tools.paper_scope(ctx):
        assert tools.paper_prepare_inputs()['input'] == {}
        bundle = tools.paper_prepare_candidate({}, receipts=[])
    assert tools.verify_publication(ctx, bundle['artifacts']) == {'valid': True}


def test_native_plot_handoff_materializes_the_returned_png(paper_context, prepared_analysis_pool, monkeypatch, tmp_path):
    import json
    import lazyllm
    from pathlib import Path
    from lazymind.chat.engine.subagent.context import SubAgentContext
    from lazymind.document_tools.paper.workflow import add_material
    fixture = json.loads((Path(__file__).resolve().parents[4]
                          / 'tests/fixtures/paper-writing/data_analysis.json').read_text())
    pool = prepared_analysis_pool(fixture['input'])
    add_material(pool, 'analysis', fixture['output']['analysis'], [], [])
    source = tmp_path / 'pool.json'
    source.write_text(json.dumps(pool))
    identity = {'session_id': 'native-plot', 'attempt_id': tmp_path.name, 'revision': 'fixed',
                'step_id': 'data_analysis', 'bindings': {'seed_pool': {'source_id': 'fixed-input'}}}
    native = SubAgentContext(task_id='native-plot', conversation_id='native-plot', agent_type='workflow_step',
                             objective='Inspect plot', workspace_path=str(tmp_path), input_slots=['seed_pool'],
                             output_slots=[], db=None, emit=lambda _event: None,
                             params={'_paper_execution': identity,
                                                      'remote_inputs': {'seed_pool': str(source)}})
    png = b'\x89PNG\r\n\x1a\ncontrolled-image-bytes'
    chart = fixture['output']['charts'][0]
    def executed(payload, **kwargs):
        assert payload['table']['rows'] == fixture['input']['data']['rows']
        return {'svg': chart['svg'], 'exports': {'png': base64.b64encode(png).decode()}}
    monkeypatch.setattr('lazymind.document_tools.paper.plotting.executor.execute_plot', executed)
    previous = lazyllm.globals.get('subagent_ctx')
    lazyllm.globals['subagent_ctx'] = native
    try:
        prepared = tools.paper_prepare_inputs()
        result = tools.paper_plot('def draw(data, statistics, spec):\n    return None',
                                  {key: value for key, value in chart.items() if key != 'svg'},
                                  prepared['existing_analysis_receipt'])
        path = Path(result['native_preview_path'])
        assert path.is_relative_to(tmp_path) and path.read_bytes() == png
    finally:
        lazyllm.globals['subagent_ctx'] = previous


def test_native_frozen_json_arguments_preserve_source_signatures(paper_context, monkeypatch):
    from types import MappingProxyType
    from lazymind.document_tools.paper.review import retrieval

    def frozen(value):
        if isinstance(value, dict):
            return MappingProxyType({key: frozen(item) for key, item in value.items()})
        if isinstance(value, list):
            return tuple(frozen(item) for item in value)
        return value

    ctx = replace(paper_context, step_id='literature_review')
    paper = {'id': 'real-source', 'title': 'Actual paper', 'text': 'Actual source text',
             'citation': 'Actual paper.'}
    monkeypatch.setattr(retrieval, 'sciverse_provider', lambda: object())
    monkeypatch.setattr(retrieval, 'read_sciverse', lambda provider, source, warnings: source)
    reference = tools.receipt_reference(ctx, 'papers', [paper])
    with tools.paper_scope(ctx):
        read = tools.paper_read_paper(frozen(reference), paper['id'])
        assert tools.unseal(ctx, read['receipt'], 'papers') == [paper]
    with tools.paper_scope(paper_context):
        candidate = tools.paper_prepare_candidate(frozen(outline()), ())
        assert tools.verify_publication(paper_context, candidate['artifacts']) == {'valid': True}


def test_receipt_reference_is_small_scoped_and_detects_tampering(paper_context):
    import json
    reference = tools.receipt_reference(paper_context, 'papers', [{'id': 'p1', 'text': 'x' * 50000}])
    assert len(json.dumps(reference)) < 150
    assert len(tools.unseal(paper_context, reference, 'papers')[0]['text']) == 50000
    with pytest.raises(ValueError, match='当前输入版本'):
        tools.unseal(replace(paper_context, attempt_id='another-attempt'), reference, 'papers')
    with pytest.raises(ValueError, match='类型'):
        tools.unseal(paper_context, {**reference, 'kind': 'analysis'}, 'analysis')
    with pytest.raises(ValueError, match='无效'):
        tools.unseal(paper_context, {'receipt_id': '../elsewhere', 'kind': 'papers'}, 'papers')
    path = tools.execution_directory() / ('receipt-' + reference['receipt_id'] + '.json')
    path.write_text('{}')
    with pytest.raises(ValueError, match='内容不一致'):
        tools.unseal(paper_context, reference, 'papers')


def test_literature_selection_resolves_actual_facts_without_retyping(paper_context):
    ctx = replace(paper_context, step_id='literature_review')
    source = {'id': 'p1', 'title': 'Actual title', 'text': 'Actual source evidence.',
              'doi': '10.1234/actual', 'year': 2024, 'reading_scope': 'abstract'}
    receipt = tools.receipt_reference(ctx, 'papers', [source])
    with tools.paper_scope(ctx):
        bundle = tools.paper_prepare_candidate({'evidence': 'Actual findings [文献:p1].',
                                               'selected_paper_ids': ['p1']}, [receipt])
    pool = pool_from_bundle(bundle, 'literature_review')
    assert material(pool, 'papers')['content'][0]['text'] == source['text']
    assert material(pool, 'papers')['content'][0]['title'] == source['title']
    assert '10.1234/actual' in material(pool, 'papers')['content'][0]['citation']


def test_native_steps_offer_only_the_saving_candidate_boundary():
    import yaml
    from pathlib import Path
    root = Path(__file__).resolve().parents[4] / 'workflows/paper-writing'
    steps = yaml.safe_load((root / 'scenario/state.yml').read_text())['steps']
    for name, node in steps.items():
        assert 'paper_prepare_candidate' not in node['tools'], name
        assert node['terminal_tools'] == ['paper_submit_candidate'], name
        assert 'paper_submit_candidate' in node['tools'], name


@pytest.mark.parametrize('no_code', [[], ''])
@pytest.mark.parametrize('metadata', [
    {},
    {'seed': '', 'metrics': '', 'assumptions': ''},
    {'seed': 42, 'column_types': {'observed': {'score': 'number'}}},
])
def test_existing_table_references_preserve_all_measurements(paper_context, no_code, metadata):
    table = {'columns': ['score'], 'column_types': ['number'], 'units': ['s'], 'rows': [[2.0], [3.0]]}
    data = {'tables': [{'id': 'observed', 'description': 'Actual observations', 'table': table}]}
    ctx = replace(paper_context, step_id='experiment', values={'seed_pool': pool_from_business({
        'topic': 'Study', 'data': data})})
    output = {'experiment': {'executed': False, 'plan': 'Organize supplied observations.',
                             'results': {'table_ids': ['observed']}, 'report': 'No new experiment.'},
              'code_files': no_code, 'result_meta': {
                  'execution_status': 'not_run', 'data_origin': 'user_supplied', 'method': 'empirical',
                  'reason': 'Supplied observations.', 'input_table_ids': ['observed'], **metadata}}
    original = copy.deepcopy(output)
    with tools.paper_scope(ctx):
        bundle = tools.paper_prepare_candidate(output, receipts=[])
        invalid_code = copy.deepcopy(output)
        invalid_code['code_files'] = 'invented.py'
        with pytest.raises(ValueError):
            tools.paper_prepare_candidate(invalid_code, receipts=[])
        wrong = copy.deepcopy(output)
        wrong['experiment']['results'] = {'table_id': 'not-bound'}
        with pytest.raises(ValueError, match='固定输入'):
            tools.paper_prepare_candidate(wrong, receipts=[])
        for state in ({'execution_status': 'succeeded'}, {'data_origin': 'observed'}):
            false_claim = copy.deepcopy(output)
            false_claim['result_meta'].update(state)
            with pytest.raises(ValueError):
                tools.paper_prepare_candidate(false_claim, receipts=[])
    pool = pool_from_bundle(bundle, 'experiment')
    experiment = material(pool, 'experiment')['content']
    meta = material(pool, 'result_meta')['content']
    assert experiment['results'] == data
    assert material(pool, 'code_files')['content'] == []
    assert meta['seed'] is None and meta['column_types'] is None
    assert meta['metrics'] == meta['assumptions'] == []
    from lazymind.document_tools.paper.experiment.handoff import to_analysis
    assert to_analysis({'topic': 'Study', 'experiment': experiment, 'result_meta': meta})['data'] == data
    assert output == original



def test_experiment_publication_includes_actual_rows_and_complete_downloads(paper_context):
    import io
    import json
    from zipfile import ZipFile

    data = {'tables': [
        {'id': 'correctness', 'description': 'Uploaded material；Deletion results；来源 upload，原文第 1—4 行。',
         'table': {'columns': ['criterion', 'success', 'total'], 'column_types': ['string', 'integer', 'integer'],
                   'units': [None, None, None], 'rows': [['Final task success', 28, 30]]}},
        {'id': 'timing', 'description': 'Uploaded material；Runtime observations',
         'table': {'columns': ['duration', 'note'], 'column_types': ['number', 'string'],
                   'units': ['s', None], 'rows': [[154.697, None]]}},
    ]}
    ctx = replace(paper_context, step_id='experiment', values={'seed_pool': pool_from_business({
        'topic': 'Study', 'data': data})})
    output = {'experiment': {'executed': False, 'plan': 'Organize the original observations.',
                             'results': {'table_ids': ['correctness', 'timing']},
                             'report': 'Two tables were prepared without new measurements.'},
              'code_files': [], 'result_meta': {
                  'execution_status': 'not_run', 'data_origin': 'user_supplied', 'method': 'empirical',
                  'reason': 'Use existing observations.', 'input_table_ids': ['correctness', 'timing']}}
    original = copy.deepcopy(output)
    with tools.paper_scope(ctx):
        bundle = tools.paper_prepare_candidate(output, receipts=[])
    files = {item['slot']: base64.b64decode(item['value']['content_base64'])
             for item in bundle['artifacts'] if item['content_type'] == 'file'}
    report = files['experiment_document'].decode()
    assert '| Final task success | 28 | 30 |' in report
    assert '| 154.697 | 缺失 |' in report and 'duration（s）' in report
    manifest = json.loads(files['experiment_results'])
    assert manifest['experiment']['results'] == data
    assert manifest['result_meta']['data_origin'] == 'user_supplied'
    with ZipFile(io.BytesIO(files['experiment_code'])) as archive:
        assert json.loads(archive.read('results.json')) == data
        assert '28,30' in archive.read('correctness.csv').decode()
        assert '154.697,' in archive.read('timing.csv').decode()
        assert 'experiment.py' not in archive.namelist()
    assert tools.verify_publication(ctx, bundle['artifacts']) == {'valid': True}
    assert output == original

def test_raw_table_import_survives_literature_filtering_without_duplicates(paper_context):
    from lazymind.document_tools.paper.workflow import add_material
    attachment = {'filename': 'observations.csv', 'text': 'score\n2\n3\n'}
    ctx = replace(paper_context, step_id='literature_review', values={
        'paper_input': {'topic': 'Study'}, 'research_materials': [attachment]})
    first = tools.base_pool(ctx)
    expected = copy.deepcopy(material(first, 'source_data')['content'])
    add_material(first, 'papers', [{'id': 'academic', 'title': 'Actual paper',
                                   'text': 'Actual published findings', 'citation': 'Actual paper.'}], [], [])
    next_context = replace(ctx, step_id='experiment', values={
        'literature_review_pool': first, 'research_materials': [attachment]})
    imported = tools.base_pool(next_context)
    assert material(imported, 'source_data')['content'] == expected
    with tools.paper_scope(next_context):
        prepared = tools.paper_prepare_inputs()
    assert prepared['table_catalog'][0]['table_id'] == expected['tables'][0]['id']
    assert prepared['table_catalog'][0]['row_count'] == 2
    assert len(prepared['table_catalog']) == 1


def test_native_raw_file_decoding_matches_external_and_keeps_table_type(paper_context, monkeypatch, tmp_path):
    import base64
    from dataclasses import asdict
    from types import SimpleNamespace
    raw = b'score\n2\n3\n'
    path = tmp_path / '0001_observed.csv'
    path.write_bytes(raw)
    identity = asdict(replace(paper_context, step_id='experiment'))
    identity.pop('values')
    native = SimpleNamespace(params={'_paper_execution': identity,
                                    'remote_inputs': {'research_materials': [str(path)]}})
    monkeypatch.setattr('lazymind.chat.engine.subagent.context.require_context', lambda: native)
    native_context = tools.native_context()
    external_context = tools.PaperContext.parse({**identity, 'values': {'research_materials': [{
        '__file_base64': base64.b64encode(raw).decode(), 'filename': 'observed.csv'}]}})
    assert native_context.values == external_context.values
    native_pool, external_pool = tools.base_pool(native_context), tools.base_pool(external_context)
    assert material(native_pool, 'source_data')['content'] == material(external_pool, 'source_data')['content']
    assert material(native_pool, 'source_data')['content']['tables'][0]['table']['rows'] == [[2], [3]]


def test_chart_receipts_publish_exact_executed_svg_without_model_copying(paper_context, prepared_analysis_pool):
    import json
    from pathlib import Path
    fixture = json.loads((Path(__file__).resolve().parents[4]
                          / 'tests/fixtures/paper-writing/data_analysis.json').read_text())
    pool = prepared_analysis_pool(fixture['input'])
    ctx = replace(paper_context, step_id='data_analysis', values={'seed_pool': pool})
    from lazymind.document_tools.paper.analysis.schema import binding
    fixture['output']['analysis'].update(binding(tools.step_inputs(ctx, pool)))
    analysis = tools.receipt_reference(ctx, 'analysis', {'analysis': fixture['output']['analysis']})
    receipts = [analysis]
    charts = []
    for chart in fixture['output']['charts']:
        receipt = tools.receipt_reference(ctx, 'chart', {'chart': chart, 'exports': {}})
        receipts.append(receipt)
        charts.append(receipt)
    with tools.paper_scope(ctx):
        bundle = tools.paper_prepare_candidate({**fixture['output'], 'charts': charts,
                                               'analysis': {'receipt': analysis, 'summary': 'Observed descriptive contrast.',
                                                            'limitations': ['No causal evidence.']}}, receipts)
        invalid = {**fixture['output'], 'analysis': {'receipt': analysis, 'row_count': 999}}
        with pytest.raises(ValueError, match='不得替换执行事实'):
            tools.paper_prepare_candidate(invalid, receipts)
    result = pool_from_bundle(bundle, 'data_analysis')
    assert material(result, 'charts')['content'] == fixture['output']['charts']
    actual = material(result, 'analysis')['content']
    assert actual['findings'] == fixture['output']['analysis']['findings']
    assert actual['summary'] == 'Observed descriptive contrast.'
    assert tools.verify_publication(ctx, bundle['artifacts']) == {'valid': True}


def test_native_analysis_declares_a_resolvable_host_vision_tool(monkeypatch):
    from pathlib import Path
    import yaml
    from lazymind.chat.engine.subagent import runner
    from lazymind.chat.engine.tools.multimodal import vision_extractor
    state = yaml.safe_load((Path(__file__).resolve().parents[4]
                            / 'workflows/paper-writing/scenario/state.yml').read_text())
    names = state['steps']['data_analysis']['tools']
    assert 'multimodal' in names
    # Only provider availability is substituted; resolve the real registry callable.
    monkeypatch.setattr(runner, 'tool_is_active', lambda config: config.name == 'multimodal')
    resolved = runner._resolve_runtime_tools([name for name in names if name == 'multimodal'])
    assert vision_extractor in runner._build_subagent_tools(resolved, tools_only=True)


@pytest.mark.parametrize('step', ['experiment', 'peer_review'])
def test_compact_contract_preserves_full_materials_and_schema_pages(paper_context, step):
    import hashlib
    import json
    papers = [{'id': 'p1', 'title': 'Actual source', 'text': 'Actual source text. ' * 20000,
               'citation': 'Actual source.'}]
    data = {'tables': [{'id': 'observed', 'table': {
        'columns': ['value'], 'column_types': ['number'], 'units': [None], 'rows': [[n] for n in range(344)]}}]}
    pool = pool_from_business({'topic': 'Study', 'papers': papers, 'source_data': data,
                              'manuscript': '# Study\n\nCited [文献:p1].'})
    ctx = replace(paper_context, step_id=step, values={'seed_pool': pool})
    original = copy.deepcopy(pool)
    with tools.paper_scope(ctx):
        prepared = tools.paper_prepare_inputs()
        assert len(tools.canonical(prepared)) < 10000
        ref = prepared['input']['papers']
        parts, offset = [], 0
        while True:
            page = tools.paper_read_material(ref['material_id'], offset=offset, limit=64000)
            parts.append(page['text'])
            if page['next_offset'] is None:
                break
            offset = page['next_offset']
        text = ''.join(parts)
        assert json.loads(text)[0]['text'] == papers[0]['text']
        assert hashlib.sha256(text.encode()).hexdigest() == ref['source_sha256']
        schema_key = 'result_meta_schema' if step == 'experiment' else 'review_schema'
        schema = json.loads(tools.paper_read_material(prepared[schema_key]['material_id'], limit=64000)['text'])
        assert schema['type'] == 'object'
        with pytest.raises(ValueError, match='绑定'):
            tools.paper_read_material('contract:execution_handle')
        if step == 'experiment':
            example = copy.deepcopy(prepared['existing_data_candidate_example'])
            example['output']['experiment']['plan'] = 'Preserve complete original observations for later analysis.'
            example['output']['experiment']['report'] = 'Original values retained, no new experiment or simulation.'
            result = tools.paper_prepare_candidate(**example)
            accepted = pool_from_bundle(result, step)
            assert material(accepted, 'experiment')['content']['results'] == data
        else:
            with pytest.raises(ValueError, match='绑定'):
                tools.paper_read_material('input:source_data')
    assert pool == original


@pytest.mark.parametrize('step', ['write_outline', 'experiment'])
def test_native_and_registered_external_adapter_share_preparation(paper_context, monkeypatch, tmp_path, step):
    import json
    from dataclasses import asdict
    from pathlib import Path
    from lazymind.chat.workflow.external_tools import (
        ExternalToolCallRequest, call_external_tool, package_tree_hash,
    )
    pool = copy.deepcopy(paper_context.values['seed_pool'])
    from lazymind.document_tools.paper.workflow import add_material
    add_material(pool, 'papers', [{'id': 'p1', 'title': 'Source', 'text': 'Actual source. ' * 2000,
                                 'citation': 'Source.'}], [], [])
    if step == 'experiment':
        add_material(pool, 'source_data', {'tables': [{
            'id': 'observed', 'description': 'Actual observations',
            'table': {'columns': ['score'], 'column_types': ['number'], 'units': ['s'], 'rows': [[2.0], [3.0]]},
        }]}, [], [])
    ctx = replace(paper_context, step_id=step, values={'seed_pool': pool})
    path = tmp_path / 'bound-pool.json'
    path.write_text(json.dumps(pool))
    identity = asdict(ctx)
    identity.pop('values')
    native = SimpleNamespace(params={'_paper_execution': identity,
                                    'remote_inputs': {'seed_pool': str(path)}})
    monkeypatch.setattr('lazymind.chat.engine.subagent.context.require_context', lambda: native)
    native_index = tools.paper_prepare_inputs()
    package = Path(__file__).resolve().parents[4] / 'workflows/paper-writing'
    files = {path.relative_to(package).as_posix(): path.read_bytes()
             for path in package.rglob('*') if path.is_file() and '__pycache__' not in path.parts}
    tree = package_tree_hash(files)
    envelope = dict(workflow_id='paper-writing', revision_id=ctx.revision, tree_hash=tree,
                    step_id=ctx.step_id, package={'tree_hash': tree, 'files': {
                        name: base64.b64encode(raw).decode() for name, raw in files.items()}})

    def external(name, arguments):
        return call_external_tool(ExternalToolCallRequest(
            **envelope, tool_name=name, trusted_context=asdict(ctx), arguments=arguments))['result']
    assert external('paper_prepare_inputs', {}) == native_index
    ref = native_index['input']['papers']['material_id']
    args = {'material_id': ref, 'offset': 100, 'limit': 4000}
    assert external('paper_read_material', args) == tools.paper_read_material(**args)
    if step == 'experiment':
        candidate = copy.deepcopy(native_index['existing_data_candidate_example'])
        candidate['output']['result_meta'].update(seed='', metrics='', column_types={'observed': {'score': 'number'}})
        native_bundle = tools.paper_prepare_candidate(**candidate)
        external_bundle = external('paper_prepare_candidate', candidate)
        assert pool_from_bundle(external_bundle, step) == pool_from_bundle(native_bundle, step)
        assert tools.verify_publication(ctx, external_bundle['artifacts']) == {'valid': True}
        accepted = pool_from_bundle(external_bundle, step)
        assert material(accepted, 'result_meta')['content']['seed'] is None
        assert material(accepted, 'experiment')['content']['results'] == material(pool, 'source_data')['content']
    assert path.read_text() == json.dumps(pool)



def test_search_uses_shared_multi_source_api_and_reads_actual_abstract(paper_context, monkeypatch):
    from lazymind.document_tools.paper.review import search
    calls = []
    candidates = [{'id': f'paper-{n}', 'title': f'Study {n}', 'source': 'crossref',
                   'abstract': f'Actual abstract {n}'} for n in range(4)]
    def retrieve(query, **kwargs):
        calls.append((query, kwargs))
        return {'papers': candidates, 'sources': [{'source': 'crossref', 'status': 'ok'},
                                                  {'source': 'arxiv', 'status': 'failed'}]}
    monkeypatch.setattr(search, 'search_papers', retrieve)
    ctx = replace(paper_context, step_id='literature_review')
    with tools.paper_scope(ctx):
        first = tools.paper_search('species morphology', count=2)
        second = tools.paper_search('species morphology', count=2, page=2)
        assert first['more'] is True and second['more'] is False
        assert [p['id'] for p in second['papers']] == ['paper-2', 'paper-3']
        assert second['warnings'] and 'arxiv' in second['warnings'][0]
        read = tools.paper_read_paper(second['receipt'], 'paper-2')
        assert read['paper']['text'] == 'Actual abstract 2'
        assert read['paper']['reading']['content_type'] == 'abstract'
        assert tools.unseal(ctx, read['receipt'], 'papers')[0] == read['paper']
    assert all(kwargs['limit'] == 2 for _, kwargs in calls)


def test_paper_candidate_uses_existing_heading_normalization_before_publication(paper_context):
    source = '# 2026 Study\n\n## 1. Methods\n## 3.3V supply\n\n1. Original list\n'
    with tools.paper_scope(paper_context):
        result = tools.paper_prepare_candidate({'outline': source}, receipts=[])
    accepted = pool_from_bundle(result)
    assert material(accepted, 'outline')['content'] == source.replace('## 1. Methods', '## Methods')
    document = next(item for item in result['artifacts'] if item['slot'] == 'write_outline_document')
    text = base64.b64decode(document['value']['content_base64']).decode()
    assert '## 1. Methods' not in text
    assert '## Methods' in text
    assert '## 3.3V supply' in text
