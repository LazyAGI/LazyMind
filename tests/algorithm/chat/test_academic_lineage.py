import copy
import json
import time
from pathlib import Path

import pytest
from lazyllm.tools.agent import ToolExecutionError
from lazymind.chat.engine.subagent import context, tools
from lazymind.workflow_toolkit import load_workflow_package_tools


def _artifact(events, slot='relation_analysis'):
    value = next(event['value'] for event in reversed(events) if event.get('slot') == slot)
    return json.loads(Path(value['path']).read_text(encoding='utf-8')) if 'path' in value else value['data']


@pytest.fixture
def lineage(tmp_path, monkeypatch, request):
    from lazymind.config import config
    previous_workspace = config['agentic_workspace']
    config['agentic_workspace'] = str(tmp_path / 'shared')
    request.addfinalizer(lambda: config.__setitem__('agentic_workspace', previous_workspace))
    names = ['academic_submit_relations', 'academic_validate_relations']
    path = Path(__file__).resolve().parents[3] / 'workflows/paper_expansion/scripts/tools.py'
    functions = load_workflow_package_tools({'files': {'scripts/tools.py': path.read_bytes()}}, names,
                                            'paper_expansion', 'lineage-test')
    namespace = functions[names[0]].__globals__

    class Module:
        def __getattr__(self, name):
            return namespace[name]

        def __setattr__(self, name, value):
            namespace[name] = value

    module = Module()
    papers = [{'paper': {'paper_id': pid, 'title': 'Paper ' + pid}, 'is_seed': pid == 'a',
               'is_important': False} for pid in 'abc']
    body = {'user_instruction': 'investigate original sources and contemporaries',
            'candidate_directions': ['method'], 'selected_directions': ['method'], 'papers': papers}
    versions = {pid: {'paper_id': pid, 'status': 'ready', 'content_hash': 'hash-' + pid,
                      'source_version': 'v1'} for pid in 'abc'}
    events = []
    ctx = context.SubAgentContext('task', 'chat', 'workflow_step', 'analyze', {
        'user_id': 'owner', 'session_id': 'session', 'revision_id': 'revision', 'tree_hash': 'tree',
        'step_id': 'analyze_relations', 'remote_inputs': {'candidate_set': body, 'expansion_id': 'exp'},
        'workflow_runtime': {'publisher_owned_slots': ['relation_analysis', 'expansion_result', 'expansion_report']},
    }, str(tmp_path), [], ['relation_analysis', 'expansion_result', 'expansion_report'], None, events.append)
    ctx.execution_deadline = time.monotonic() + 1800
    from lazymind.chat.service import llm_task
    ctx.report_requests = []

    def report_model(request, _prompt, **kwargs):
        assert request.task_type == 'academic.report'
        ctx.report_requests.append(request)
        citation = f"[{request.input.data['papers'][0]['reference']}]" if request.input.data['papers'] else ''
        return {'narrative': '## 研究概述\n\n围绕研究问题综合已有材料。' + citation
                + '\n\n## 结论与局限\n\n结论仅适用于现有材料覆盖的范围。'}

    monkeypatch.setattr(llm_task, '_call_model', report_model)
    monkeypatch.setattr(context, 'require_context', lambda: ctx)
    monkeypatch.setattr(context, 'get_context', lambda: ctx)
    monkeypatch.setattr(tools, 'require_context', lambda: ctx)
    monkeypatch.setattr(module, '_resolve', lambda ids: {
        'papers': [copy.deepcopy(paper) for pid in ids for paper in papers if paper['paper']['paper_id'] == pid],
        'contents': [copy.deepcopy(versions[pid]) for pid in ids]})
    monkeypatch.setattr(module, '_read_expansion_context', lambda _: {
        'record': {'result': body, 'reading': {pid: {**versions[pid], 'positions': [0, 1]} for pid in 'abc'}},
        'source_queries': [
            {'id': 'exact', 'query': 'precise title', 'mode': 'keyword', 'created_at': '2026-11-02',
             'sources': [{'source': 'index', 'status': 'ok'}], 'paper_id': 'a'},
            {'id': 'forward', 'query': '', 'mode': 'citations', 'created_at': '2026-11-02',
             'sources': [{'source': 'index', 'status': 'ok'}], 'paper_id': 'a'}]})
    monkeypatch.setattr(module, '_model_metrics', lambda: {})
    args = {'analyses': [{'paper_id': pid, 'key_contribution': 'Mechanism of ' + pid} for pid in 'abc'],
            'claims': [{'paper_id': pid, 'field': 'key_contribution', 'positions': [0]} for pid in 'abc'],
            'relations': [{'id': 'b-c', 'source_paper_id': 'b', 'target_paper_id': 'c',
                           'relation_type': 'mechanism_comparison', 'statement': 'Different sharing scopes',
                           'conditions': 'Measured in the named configurations',
                           'evidence': [{'paper_id': 'b', 'positions': [0]}, {'paper_id': 'c', 'positions': [1]}]}],
            'research_structure': {'directions': [{'id': 'method', 'name': 'method', 'question': 'Which mechanism?'}],
                                   'nodes': [{'paper_id': 'b', 'direction_id': 'method', 'branch_question': 'sharing?',
                                              'reason': 'Different object of sharing'},
                                             {'paper_id': 'c', 'direction_id': 'method',
                                              'branch_question': 'sharing scope?', 'reason': 'Investigate scope'}]},
            'investigation': [{'question_id': kind, 'kind': kind, 'direction': 'method', 'question': kind + '?',
                               'status': 'resolved', 'conclusion': 'Search documented', 'paper_ids': ['a', 'b', 'c'],
                               'queries': ['precise title'], 'query_ids': ['exact', 'forward'],
                               'relation_ids': ['b-c'], 'unresolved_clues': [], 'gaps': []}
                              for kind in ['predecessor', 'successor', 'peer']]}
    args['relations'].append({**copy.deepcopy(args['relations'][0]), 'id': 'a-b',
                              'source_paper_id': 'a', 'target_paper_id': 'b',
                              'relation_type': 'design_inspiration', 'statement': 'Design attribution',
                              'evidence': [{'paper_id': 'a', 'positions': [0]}, {'paper_id': 'b', 'positions': [0]}]})
    args['investigation'][0]['relation_ids'] = ['a-b']
    return module, ctx, events, versions, args


def test_publish_report_and_graph_share_authored_analysis(lineage):
    module, ctx, events, _, args = lineage
    assert module.academic_submit_relations(**args)['status'] == 'ok'
    analysis = _artifact(events)
    report = _artifact(events, 'expansion_report')
    assert len(ctx.report_requests) == 1
    assert report['narrative'].startswith('## 研究概述')
    assert '## 参考文献' in report['narrative']
    assert len(ctx.report_requests[0].input.data['findings']) == 5
    for key in ('input', 'claims', 'relations', 'completion', 'analysis_version', 'source_versions'):
        assert report[key] == analysis[key]
    assert analysis['completion'] == {'status': 'complete', 'reason': 'scope_covered'}
    assert [row['key_contribution'] for row in report['input']['papers']] == ['Mechanism of ' + pid for pid in 'abc']
    assert all('verification' not in edge for edge in report['relations'])
    assert all('status' not in claim for claim in report['claims'])
    assert module.academic_validate_relations(analysis)['status'] == 'valid'
    assert module.academic_submit_relations(**args)['already_published']
    assert len(ctx.report_requests) == 1


def test_deep_report_uses_frozen_candidates(lineage, monkeypatch):
    module, ctx, events, _, args = lineage
    candidate = ctx.params['remote_inputs']['candidate_set']
    recommendations = [{'paper_id': pid, 'direction': 'method', 'reason': 'Recommendation ' + pid} for pid in 'bc']
    candidate.update(list_mode='deep', directions=[{'name': 'method', 'question': 'Which mechanism?'}],
                     recommendations=recommendations)
    args.pop('research_structure')
    module.academic_submit_relations(**args)
    report = _artifact(events, 'expansion_report')
    assert report['recommendations'] == recommendations
    assert [row['paper']['paper_id'] for row in report['input']['papers']] == list('abc')
    assert len(report['research_structure']['nodes']) == 2


def test_named_clue_submission_reports_all_errors_then_publishes_source_mechanism(lineage, monkeypatch):
    from lazyllm.tools.agent import ToolManager
    from lazymind.chat.lazyllm_tool_docs import ensure_lazyllm_tool_docs

    module, ctx, events, _, args = lineage
    snapshot = module._read_expansion_context('exp')
    snapshot['record']['preparation'] = {'named_clues': [
        {'source_paper_id': 'a', 'name': name, 'question_ids': ['predecessor']}
        for name in ['CSA2', 'Zero SWA Caching']]}
    monkeypatch.setattr(module, '_read_expansion_context', lambda _: snapshot)
    clues = [
        {'source_paper_id': 'a', 'name': 'CSA2', 'status': 'resolved', 'paper_id': 'CSA2',
         'reason': 'The source paper introduces this attention mechanism.', 'query_ids': ['exact']},
        {'source_paper_id': 'a', 'name': 'Zero SWA Caching', 'status': 'blocked', 'paper_id': 'unknown-paper',
         'reason': 'The cited report is unavailable from the recorded retrieval.', 'query_ids': ['invented']},
    ]
    args['investigation'][0]['clue_resolutions'] = clues
    ensure_lazyllm_tool_docs([module.academic_submit_relations])
    tool = ToolManager([module.academic_submit_relations]).all_tools[0]
    with pytest.raises(ToolExecutionError) as exc:
        module.academic_submit_relations(**tool._validate_input(args))
    error = str(exc.value)
    assert 'clue_resolutions[0]' in error and 'CSA2' in error and 'source_paper_id' in error
    assert 'clue_resolutions[1]' in error and 'unknown-paper' in error and 'must omit paper_id' in error
    assert 'invented' in error and 'query receipt' in error
    assert not ctx.report_requests and not any(event.get('slot') for event in events)

    clues[0]['paper_id'] = 'a'
    clues[1].pop('paper_id')
    clues[1]['query_ids'] = ['exact']
    module.academic_submit_relations(**tool._validate_input(args))
    report = _artifact(events, 'expansion_report')
    published_clues = report['investigation'][0]['clue_resolutions']
    assert published_clues[0]['status'] == 'resolved' and published_clues[0]['paper_id'] == 'a'
    assert published_clues[1]['status'] == 'blocked' and 'paper_id' not in published_clues[1]
    assert report['completion'] == {'status': 'incomplete', 'reason': 'material_limited'}
    assert not any(gap.get('clue_name') == 'CSA2' for gap in report['coverage_gaps'])
    assert any(gap.get('clue_name') == 'Zero SWA Caching' for gap in report['coverage_gaps'])
    assert len(ctx.report_requests) == 1


def test_submission_reports_receipt_and_each_papers_citation_errors_together(lineage, monkeypatch):
    from lazyllm.tools.agent import ToolManager
    from lazymind.chat.lazyllm_tool_docs import ensure_lazyllm_tool_docs

    module, ctx, events, versions, args = lineage
    snapshot = module._read_expansion_context('exp')
    receipt_id = '6c1d3fe2' + 'a' * 56
    snapshot['source_queries'][0]['id'] = receipt_id
    snapshot['record']['reading']['a']['positions'] = [0, 1, 8]
    snapshot['record']['reading']['c']['content_hash'] = 'older-body'
    monkeypatch.setattr(module, '_read_expansion_context', lambda _: snapshot)
    for item in args['investigation']:
        item['query_ids'] = [receipt_id[:8], 'forward']
    args['claims'][1]['positions'] = [0, 8]
    ensure_lazyllm_tool_docs([module.academic_submit_relations])
    tool = ToolManager([module.academic_submit_relations]).all_tools[0]
    args['analyses'][0]['claims'] = []
    with pytest.raises(ValueError, match='claims'):
        tool._validate_input(args)
    args['analyses'][0].pop('claims')
    with pytest.raises(ToolExecutionError) as exc:
        module.academic_submit_relations(**tool._validate_input(args))
    error = str(exc.value)
    assert receipt_id[:8] in error and 'without abbreviation' in error
    assert "claims[1], paper_id='b': unread positions=[8]; recorded positions=[0, 1]" in error
    assert "claims[2], paper_id='c': source version changed" in error
    assert "relations[0], paper_id='c': source version changed" in error
    assert not ctx.report_requests and not any(event.get('slot') for event in events)

    for item in args['investigation']:
        item['query_ids'] = [receipt_id, 'forward']
    args['claims'][1]['positions'] = [0]
    args['claims'][1]['evidence'] = [{'paper_id': 'b', 'positions': [0]}, {'paper_id': 'a', 'positions': [8]}]
    snapshot['record']['reading']['c']['content_hash'] = versions['c']['content_hash']
    module.academic_submit_relations(**tool._validate_input(args))
    report = _artifact(events, 'expansion_report')
    claim = next(row for row in report['claims'] if row['paper_id'] == 'b')
    assert [(ref['paper_id'], ref['positions']) for ref in claim['evidence']] == [('b', [0]), ('a', [8])]
    assert report['completion']['status'] == 'complete'
    assert len(ctx.report_requests) == 1


def test_unavailable_paper_cannot_publish_fabricated_read_positions(lineage):
    module, ctx, events, versions, args = lineage
    versions['c']['status'] = 'needs_review'
    with pytest.raises(ToolExecutionError, match="content status='needs_review'; there is no ready source"):
        module.academic_submit_relations(**args)
    assert not ctx.report_requests and not any(event.get('slot') for event in events)


@pytest.mark.parametrize('fault', ['endpoint', 'unread', 'version', 'query', 'seed', 'structure'])
def test_invalid_research_does_not_generate_or_publish(lineage, fault):
    module, ctx, events, versions, args = lineage
    if fault == 'endpoint':
        args['relations'][0]['source_paper_id'] = 'not-selected'
    elif fault == 'unread':
        args['claims'][0]['positions'] = [8]
    elif fault == 'version':
        original = module._read_expansion_context('exp')
        original['record']['reading']['a']['content_hash'] = 'old'
        module._read_expansion_context = lambda _: original
    elif fault == 'query':
        args['investigation'][0]['query_ids'] = ['invented']
    elif fault == 'seed':
        args['analyses'] = args['analyses'][1:]
        args['selection_reasons'] = [{'paper_id': 'a', 'reason': 'Dropped seed'}]
    else:
        args['research_structure']['nodes'].pop()
    with pytest.raises(ToolExecutionError):
        module.academic_submit_relations(**args)
    assert not ctx.report_requests
    assert not any(event.get('slot') for event in events)


@pytest.mark.parametrize('failure', ['timeout', 'citation', 'source_changed', 'cancelled'])
def test_report_failure_publishes_nothing(lineage, monkeypatch, failure):
    import asyncio
    from lazymind.chat.service import llm_task
    module, _, events, versions, args = lineage

    def fail(request, prompt, **kwargs):
        if failure == 'timeout':
            raise llm_task.LLMTaskCallError('task_timeout', retryable=False)
        if failure == 'cancelled':
            raise asyncio.CancelledError()
        if failure == 'source_changed':
            versions['a']['content_hash'] = 'new-body'
        return {'narrative': '研究结论 [999]' if failure == 'citation' else '研究结论 [1]'}

    monkeypatch.setattr(llm_task, '_call_model', fail)
    with pytest.raises((llm_task.LLMTaskError, ToolExecutionError, asyncio.CancelledError)):
        module.academic_submit_relations(**args)
    assert not any(event.get('slot') for event in events)


def test_incomplete_report_preserves_actual_material_gap(lineage):
    module, ctx, events, _, args = lineage
    args['investigation'][0].update(status='blocked', gaps=['原文引用条目未提供可获取的全文'])
    module.academic_submit_relations(**args)
    report = _artifact(events, 'expansion_report')
    assert report['completion'] == {'status': 'incomplete', 'reason': 'material_limited'}
    assert report['coverage_gaps'][0]['reason'] == '原文引用条目未提供可获取的全文'
    assert ctx.report_requests[0].input.data['limitations'] == report['coverage_gaps']


def test_actionable_gaps_require_research_until_budget_is_exhausted(lineage):
    module, ctx, events, _, args = lineage
    args['investigation'][0].update(status='open', gaps=['尚需阅读原文方法章节'])
    with pytest.raises(ToolExecutionError, match='actionable gaps'):
        module.academic_submit_relations(**args)
    assert not ctx.report_requests
    ctx.tool_call_budget = {name: {'remaining': 0} for name in ('academic_read_paper', 'academic_search_papers')}
    module.academic_submit_relations(**args)
    assert _artifact(events)['completion'] == {'status': 'incomplete', 'reason': 'budget_exhausted'}


def test_topic_only_successor_search_uses_investigation_papers(lineage):
    module, ctx, events, _, args = lineage
    ctx.params['remote_inputs']['candidate_set']['papers'][0]['is_seed'] = False
    args['research_structure']['nodes'].append({'paper_id': 'a', 'direction_id': 'method',
                                               'branch_question': 'Topic method?', 'reason': 'Reference method'})
    module.academic_submit_relations(**args)
    assert _artifact(events)['research_complete']


def test_postcheck_rejects_forged_analysis(lineage):
    module, _, events, _, args = lineage
    module.academic_submit_relations(**args)
    analysis = copy.deepcopy(_artifact(events))
    analysis['relations'][0]['statement'] = 'Different analysis'
    with pytest.raises(ToolExecutionError, match='this execution'):
        module.academic_validate_relations(analysis)


def test_report_call_obeys_remaining_execution_time(lineage):
    module, ctx, _, _, args = lineage
    ctx.execution_deadline = time.monotonic() + 60
    module.academic_submit_relations(**args)
    request = ctx.report_requests[0]
    assert 0 < request.options['wall_timeout_seconds'] <= 60


@pytest.mark.asyncio
async def test_large_frozen_inputs_use_existing_path_transport_through_context_submission_and_report(
        lineage, monkeypatch):
    import base64
    import json
    import lazyllm
    import yaml
    from lazymind.chat.workflow.remote_executor import RemoteWorkflowExecutor
    from lazymind.chat.engine.subagent.runner import _workflow_material_bindings_section
    from lazymind.chat.engine.tools.file_resources.tools import build_resource_read_tools

    module, ctx, events, versions, args = lineage
    monkeypatch.setenv('LAZYMIND_SUBAGENT_WORKSPACE', ctx.workspace_path)
    source_context = copy.deepcopy(module._read_expansion_context('exp'))
    source_context['record']['result']['papers'] = source_context['record']['result']['papers'][:1]
    source_context['record']['report'] = {'internal_history': 'historical detail ' * 40000}
    source_context['contents'] = [*copy.deepcopy(list(versions.values())),
                                  {'paper_id': 'discovery-only', 'status': 'unavailable'}]
    source_context['contents'][1]['status'] = 'unavailable'
    monkeypatch.setattr(module, '_read_expansion_context', lambda _: copy.deepcopy(source_context))
    seed_record = copy.deepcopy(source_context['record']['result'])
    candidate = copy.deepcopy(ctx.params['remote_inputs']['candidate_set'])
    for row in candidate['papers']:
        row['paper']['abstract'] = 'Full frozen metadata for ' + row['paper']['paper_id'] + ' ' + ('source ' * 10000)
        row['paper']['published_at'] = '2026-01-02T00:00:00Z'
    payloads = {'expansion_id': 'exp', 'candidate_set': candidate}
    package = Path(__file__).resolve().parents[3] / 'workflows' / 'paper_expansion'
    workflow = yaml.safe_load((package / 'workflow.yaml').read_text(encoding='utf-8'))
    state = yaml.safe_load((package / 'scenario/state.yml').read_text(encoding='utf-8'))
    slot_types = {row['id']: row['type'] for row in workflow['slots']}

    class Runtime:
        async def input(self, _client, _attempt, _lease, material):
            content = (payloads[material].encode() if material == 'expansion_id'
                       else json.dumps(payloads[material], ensure_ascii=False, indent=2).encode())
            return {'name': material + '.json', 'mime_type': 'application/json',
                    'content_base64': base64.b64encode(content).decode()}

    worker = RemoteWorkflowExecutor()
    worker.runtime = Runtime()
    monkeypatch.setattr(lazyllm, 'globals', {'agentic_config': {
        'agent_type': 'workflow_step', 'workflow_workspace_path': ctx.workspace_path}})

    async def bind(material):
        step = 'analyze_relations'
        inputs = state['steps'][step]['inputs']
        contract = {'declared_input_types': {row['material']: slot_types[row['material']] for row in inputs},
                    'declared_input_transports': {row['material']: row.get('transport', 'auto') for row in inputs},
                    'inputs': {'expansion_id': {'source_type': 'input_resource'},
                               material: {'source_type': 'artifact'}}}
        inputs = await worker._resolve_inputs(object(), 'attempt', 'lease', contract, ctx.workspace_path)
        ctx.params.update(remote_inputs=inputs, remote_input_types=contract['declared_input_types'],
                          remote_input_transports=worker._resolved_input_transports(contract, inputs))
        assert inputs['expansion_id'] == 'exp'
        prompt = _workflow_material_bindings_section(ctx.params)
        bindings = json.loads(prompt.rsplit('\n', 1)[-1])
        assert bindings[material] == {'type': 'json', 'kind': 'path', 'path': inputs[material]}
        assert 'Full frozen metadata' not in prompt
        return inputs[material]

    candidate_path = await bind('candidate_set')
    visible = module.academic_expansion_context()
    assert visible['record']['result'] == seed_record
    assert 'full_context_resource' in visible
    assert {row['paper_id']: row['status'] for row in visible['contents']} == {
        'a': 'ready', 'b': 'unavailable', 'c': 'ready'}
    directory = visible['bound_inputs']['candidate_set']['papers']
    assert [row['paper']['paper_id'] for row in directory] == list('abc')
    assert directory[0]['is_seed'] and not directory[0]['is_important']
    assert directory[0]['paper']['published_at'] == '2026-01-02T00:00:00Z'
    assert 'abstract' not in directory[0]['paper']
    readers = {tool.__name__: tool for tool in build_resource_read_tools()}
    assert 'Full frozen metadata for a' in readers['read_file_resource'](candidate_path)['text']
    assert module._bound('candidate_set') == candidate
    source_context['contents'][1]['status'] = 'ready'
    monkeypatch.setattr(module, '_resolve', lambda ids: {
        'papers': [copy.deepcopy(row) for row in candidate['papers'] if row['paper']['paper_id'] in ids],
        'contents': [copy.deepcopy(versions[identity]) for identity in ids]})
    module.academic_submit_relations(**args)
    relation = _artifact(events)
    assert relation['input']['papers'][0]['paper']['abstract'] == candidate['papers'][0]['paper']['abstract']
    report = _artifact(events, 'expansion_report')
    assert report['input'] == relation['input']
    assert report['relations'] == relation['relations']
    assert report['analysis_version'] == relation['analysis_version']
