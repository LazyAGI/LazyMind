import copy
import json
import time
from pathlib import Path
from types import SimpleNamespace

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
    names = ['academic_check_relations', 'academic_submit_relations']
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
    monkeypatch.setattr(context, 'require_context', lambda: ctx)
    monkeypatch.setattr(context, 'get_context', lambda: ctx)
    monkeypatch.setattr(tools, 'require_context', lambda: ctx)
    monkeypatch.setattr(module, '_resolve', lambda ids: {
        'papers': [copy.deepcopy(paper) for pid in ids for paper in papers if paper['paper']['paper_id'] == pid],
        'contents': [copy.deepcopy(versions[pid]) for pid in ids]})
    monkeypatch.setattr(module, '_read_expansion_context', lambda _: {
        'record': {'result': body, 'reading': {}}, 'source_queries': [
            {'id': 'exact', 'query': 'precise title', 'mode': 'keyword', 'created_at': '2026-11-02',
             'sources': [{'source': 'index', 'status': 'ok'}], 'paper_id': 'a'},
            {'id': 'forward', 'query': '', 'mode': 'citations', 'created_at': '2026-11-02',
             'sources': [{'source': 'index', 'status': 'ok'}], 'paper_id': 'a'}]})
    monkeypatch.setattr(module, '_model_metrics', lambda: {})
    monkeypatch.setattr(module, '_client', lambda: _model_client(1))
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


def _supported(claim):
    return dict(claim, status='supported', scope_status='supported', reason='matches supplied source')


def test_relation_to_unselected_paper_identifies_the_invalid_endpoint(lineage, monkeypatch):
    module, _, events, _, args = lineage
    args['relations'][0]['source_paper_id'] = 'not-selected'
    requests = []
    monkeypatch.setattr(module, '_verify_claims', lambda *params: requests.append(params))
    with pytest.raises(ToolExecutionError, match=r"relations\[0\] \(b-c\): source_paper_id='not-selected'"):
        module.academic_check_relations(**args)
    assert requests == [] and events == []


def test_verification_prioritizes_seed_relations_across_confirmed_directions(lineage):
    module, _, _, _, _ = lineage
    relations = [{'id': identity, 'source_paper_id': 'a' if identity.startswith('core') else 'b',
                  'target_paper_id': 'c'} for identity in (
                      'peripheral', 'core-cache', 'core-cache-extra', 'core-precision', 'core-prefill',
                      'core-persistence', 'unlinked')]
    investigation = [
        {'direction': 'cache', 'relation_ids': ['peripheral', 'core-cache', 'core-cache-extra']},
        {'direction': 'precision', 'relation_ids': ['core-precision']},
        {'direction': 'prefill', 'relation_ids': ['core-cache', 'core-prefill']},
        {'direction': 'persistence', 'relation_ids': ['core-persistence']},
    ]
    order = module._verification_relation_order(
        relations, investigation, ['cache', 'precision', 'prefill', 'persistence'], {'a'})
    assert order[:4] == ['core-cache', 'core-precision', 'core-prefill', 'core-persistence']
    assert order.index('core-cache-extra') < order.index('peripheral')
    assert len(order) == len(relations) and set(order) == {row['id'] for row in relations}


def _supported_verification_output(request):
    checks = []
    for row in request.input.data['evidence']:
        key = row['available_sources'][0]
        witness = {'status': 'supported', 'source_key': key,
                   'source_excerpt': request.input.data['source_pages_by_key'][key]['text'][:80],
                   'statement_excerpt': row['statement'], 'reason': 'Conditions retained'}
        scope = [{'kind': 'subject', **witness},
                 {'kind': 'baseline', 'status': 'not_applicable', 'source_key': None,
                  'source_excerpt': None, 'statement_excerpt': None, 'reason': 'No relative numerical baseline'}]
        if row['field'] == 'relation':
            scope.append({'kind': 'identity', **witness})
        checks.append({'index': row['index'], 'parts': [
            {'part_index': part['part_index'], 'status': 'supported', 'reason': 'Explicit source support',
             'sources': row['available_sources'][:1]} for part in row['parts']], 'scope_checks': scope})
    return {'checks': checks}


def _model_client(version):
    class Transport:
        def request(self, method, url, **kwargs):
            assert method == 'GET' and url == 'http://core/chat/models'
            assert kwargs['params'] == {'conversation_id': 'chat'}
            return SimpleNamespace(raise_for_status=lambda: None,
                                   json=lambda: {'data': {'selection': {'version': version}}})

    return SimpleNamespace(transport=Transport(), base_url='http://core', _headers=lambda: {})


def test_deep_report_reuses_frozen_recommendations_and_prepared_questions(lineage, monkeypatch):
    module, ctx, events, _, args = lineage
    candidate = ctx.params['remote_inputs']['candidate_set']
    recommendations = [
        {'paper_id': pid, 'direction': 'method', 'reason': 'Frozen recommendation for ' + pid}
        for pid in 'bc'
    ]
    candidate.update(list_mode='deep', directions=[{'name': 'method', 'question': 'Which mechanism?'}],
                     recommendations=recommendations)
    saved_context = module._read_expansion_context('exp')
    saved_context['record']['preparation'] = {'research_questions': [
        {'id': row['question_id'], 'direction': row['direction'], 'category': row['kind'],
         'question': row.pop('question')} for row in args['investigation']
    ]}
    monkeypatch.setattr(module, '_read_expansion_context', lambda _: saved_context)
    monkeypatch.setattr(module, '_verify_claims', lambda claims, allowed, **kwargs: [_supported(row) for row in claims])
    args.pop('research_structure')

    checked = module.academic_check_relations(**args)
    assert checked['can_submit']
    module.academic_submit_relations(check_id=checked['check_id'])
    analysis = _artifact(events)
    assert analysis['recommendations'] == recommendations
    assert analysis['investigation'][0]['question'] == 'predecessor?'
    assert [row['paper_id'] for row in analysis['research_structure']['nodes']] == ['b', 'c']

    assert _artifact(events)['recommendations'] == recommendations
    assert [row['paper']['paper_id'] for row in _artifact(events)['input']['papers']] == ['a', 'b', 'c']


def test_check_repair_and_submit_binds_only_latest_text_and_reuses_unchanged_checks(lineage, monkeypatch):
    module, ctx, events, _, args = lineage
    calls = []

    def check(claims, allowed, **kwargs):
        calls.append(copy.deepcopy(claims))
        return [_supported(claim) if claim['statement'] != 'Different sharing scopes'
                else dict(claim, status='uncertain', scope_status='uncertain', reason='Need component qualifier')
                for claim in claims]

    monkeypatch.setattr(module, '_verify_claims', check)
    first = module.academic_check_relations(**args)
    assert events == [] and first['issues'][0]['reason'] == 'Need component qualifier'
    args['relations'][0]['statement'] = 'Different sharing scopes within decoder components'
    second = module.academic_check_relations(**args)
    assert len(calls[0]) == 5 and len(calls[1]) == 1 and second['complete']
    # Canonical identity can change during a normal follow-up lookup without a source-content change.
    resolve = module._resolve

    def refreshed_identity(ids):
        resolved = resolve(ids)
        for row in resolved['papers']:
            if row['paper']['paper_id'] == 'b':
                row['paper']['authors'] = ['New canonical attribution']
        return resolved

    monkeypatch.setattr(module, '_resolve', refreshed_identity)
    third = module.academic_check_relations(**args)
    assert third['semantic_round'] == second['semantic_round']
    assert len(calls[2]) == 3  # b's own field and both typed relations use this identity.
    with pytest.raises(ToolExecutionError, match='latest'):
        module.academic_submit_relations(check_id=second['check_id'])
    second = third
    with pytest.raises(ToolExecutionError, match='latest'):
        module.academic_submit_relations(check_id=first['check_id'])
    module.academic_submit_relations(check_id=second['check_id'])
    relation = _artifact(events)
    assert relation['analysis_version'] == second['check_id']
    assert relation['relations'][0]['statement'].endswith('decoder components')
    assert module.academic_submit_relations(check_id=second['check_id'])['already_published']
    assert len(events) == 3


def test_technical_retry_reuses_successes_and_does_not_consume_semantic_revision(lineage, monkeypatch):
    module, _, _, _, args = lineage
    calls = []

    def check(claims, allowed, **kwargs):
        calls.append(copy.deepcopy(claims))
        return [dict(claim, status='uncertain', verification_error='task_timeout', reason='technical failure')
                if len(calls) == 1 and claim.get('claim_id') == 'b-c' else _supported(claim) for claim in claims]

    monkeypatch.setattr(module, '_verify_claims', check)
    first = module.academic_check_relations(**args)
    second = module.academic_check_relations(**args)
    assert first['semantic_round'] == second['semantic_round'] == 1
    assert len(calls[1]) == 1 and second['complete']


def _retry_task(ctx, monkeypatch, workspace):
    retry = context.SubAgentContext('retry-task', ctx.conversation_id, ctx.agent_type, ctx.objective,
                                    copy.deepcopy(ctx.params), str(workspace), ctx.input_slots, ctx.output_slots,
                                    ctx.db, ctx.emit)
    retry.execution_deadline = time.monotonic() + 1800
    workspace.mkdir(parents=True)
    monkeypatch.setattr(context, 'require_context', lambda: retry)
    monkeypatch.setattr(context, 'get_context', lambda: retry)
    monkeypatch.setattr(tools, 'require_context', lambda: retry)
    return retry


def test_interrupted_verification_resumes_completed_batches_in_a_new_task(lineage, monkeypatch, tmp_path):
    import threading
    from lazymind.chat.service import llm_task
    module, ctx, events, versions, args = lineage
    ctx.workspace_path = str(tmp_path / 'owner' / 'first-task')
    Path(ctx.workspace_path).mkdir(parents=True)
    _with_prepared_clue(module, monkeypatch)
    args['investigation'][0]['clue_resolutions'] = [
        {'source_paper_id': 'a', 'name': 'Original mechanism', 'status': 'resolved', 'paper_id': 'b',
         'reason': 'The original source attributes this mechanism.', 'query_ids': []}]
    reads = []

    def read(method, path, payload):
        reads.append(copy.deepcopy(payload))
        return {'status': 'ready', 'positions': payload['positions'],
                'pages': [{'page': 1, 'text': 'Original mechanism'} for _ in payload['positions']],
                'content_hash': versions[payload['paper_id']]['content_hash'], 'source_version': 'v1'}

    monkeypatch.setattr(module, '_call', read)
    saved = threading.Event()
    save = module._save_lineage_state

    def checkpoint(path, state):
        save(path, state)
        if len(state['cache']) >= 3:
            saved.set()

    monkeypatch.setattr(module, '_save_lineage_state', checkpoint)
    requests = []
    interrupted = True

    def verify(request, usage, validate_output, **kwargs):
        rows = request.input.data['evidence']
        requests.append([row.get('claim_id', row['paper_id']) for row in rows])
        if interrupted and any(row['field'] == 'relation' for row in rows):
            assert saved.wait(timeout=5), 'the completed batch must be persisted before interruption'
            raise RuntimeError('transport interrupted after one completed batch')
        usage['model_calls'] = 1
        return validate_output(_supported_verification_output(request)), None, None

    monkeypatch.setattr(llm_task, '_run_task', verify)
    with pytest.raises(RuntimeError, match='transport interrupted'):
        module.academic_check_relations(**args)
    assert not any(event['type'] == 'artifact' for event in events)
    first_state = module._lineage_state()[1]
    assert len(first_state['cache']) == 3
    assert first_state['minimum_seed_context']['positions'] == {'a': [0]}
    assert sum(row['paper_id'] == 'a' for row in reads) == 2
    reads.clear()
    retry = _retry_task(ctx, monkeypatch, tmp_path / 'owner' / 'retry-task')
    assert retry.workspace_path != ctx.workspace_path
    interrupted = False
    requests.clear()
    checked = module.academic_check_relations(**args)
    assert checked['complete'] and checked['semantic_round'] == 1
    assert sum(row['paper_id'] == 'a' for row in reads) == 1
    assert len(requests) == 2 and all(len(row) == 1 for row in requests)
    assert {row[0] for row in requests} == {'b-c', 'a-b'}
    assert module._lineage_state()[1]['deadline_at'] == first_state['deadline_at']
    module.academic_submit_relations(check_id=checked['check_id'])
    assert sum(event['type'] == 'artifact' for event in events) == 3


@pytest.mark.parametrize('first_valid_delay,hard_timeout', [(480, 1800), (1500, 1800), (480, None)])
def test_retry_cannot_restart_check_deadline_or_three_semantic_rounds(
        lineage, monkeypatch, tmp_path, first_valid_delay, hard_timeout):
    from types import SimpleNamespace
    module, ctx, _, _, args = lineage
    clock = {'wall': 10000, 'monotonic': 100}
    monkeypatch.setattr(module, 'time', SimpleNamespace(
        time=lambda: clock['wall'], monotonic=lambda: clock['monotonic']))
    ctx.execution_deadline = 100 + hard_timeout if hard_timeout else None
    expected_step_deadline = 10000 + hard_timeout if hard_timeout else None
    monkeypatch.setattr(module, '_verify_claims', lambda rows, allowed, **kwargs: [_supported(row) for row in rows])
    invalid = copy.deepcopy(args)
    invalid['research_structure']['nodes'].append({
        'paper_id': 'a', 'direction_id': 'method', 'branch_question': 'Seed mechanism?', 'reason': 'seed'})
    with pytest.raises(ToolExecutionError, match='do not include seeds'):
        module.academic_check_relations(**invalid)
    invalid_state = module._lineage_state()[1]
    assert invalid_state['rounds'] == [] and 'deadline_at' not in invalid_state
    assert invalid_state.get('step_deadline_at') == expected_step_deadline
    clock.update(wall=10000 + first_valid_delay, monotonic=100 + first_valid_delay)
    for item in args['investigation']:
        item['queries'] = []
    for revision in range(3):
        args['analyses'][0]['key_contribution'] = f'Grounded contribution revision {revision}'
        assert module.academic_check_relations(**args)['semantic_round'] == revision + 1
    deadline = ctx._academic_verification_deadline
    checked_state = module._lineage_state()[1]
    expected_wall_deadline = min(expected_step_deadline or float('inf'), 10000 + first_valid_delay + 900)
    expected_science_deadline = min(expected_wall_deadline,
                                    expected_step_deadline - 180 if hard_timeout else float('inf'))
    assert checked_state['deadline_at'] == expected_wall_deadline
    assert deadline == 100 + expected_science_deadline - 10000
    clock.update(wall=expected_science_deadline + 1, monotonic=deadline + 1)
    retry = _retry_task(ctx, monkeypatch, tmp_path / 'retry-task')
    retry.execution_deadline = clock['monotonic'] + hard_timeout if hard_timeout else None
    # An unchanged technical retry remains usable but receives no renewed checking budget.
    assert module.academic_check_relations(**args)['semantic_round'] == 3
    assert retry._academic_verification_deadline <= clock['monotonic']
    assert module._lineage_state()[1]['deadline_at'] == expected_wall_deadline
    assert module._lineage_state()[1].get('step_deadline_at') == invalid_state.get('step_deadline_at')
    args['analyses'][0]['key_contribution'] = 'A fourth semantic revision'
    with pytest.raises(ToolExecutionError, match='three semantic checks'):
        module.academic_check_relations(**args)


@pytest.mark.parametrize('changed_scope', ['candidate_set', 'revision_id', 'record_version'])
def test_new_input_or_plugin_revision_has_an_isolated_check_scope(lineage, monkeypatch, tmp_path, changed_scope):
    module, ctx, _, _, args = lineage
    requests = []
    monkeypatch.setattr(module, '_verify_claims', lambda rows, allowed, **kwargs: (
        requests.append(copy.deepcopy(rows)) or [_supported(row) for row in rows]))
    first = module.academic_check_relations(**args)
    retry = _retry_task(ctx, monkeypatch, tmp_path / 'new-scope-task')
    if changed_scope == 'record_version':
        read_context = module._read_expansion_context

        def revised_context(identity):
            value = read_context(identity)
            value['record']['version'] = 1
            return value

        monkeypatch.setattr(module, '_read_expansion_context', revised_context)
    elif changed_scope == 'candidate_set':
        retry.params['remote_inputs']['candidate_set']['user_instruction'] = 'A newly bound research instruction'
    else:
        retry.params['revision_id'] = 'new-plugin-revision'
    second = module.academic_check_relations(**args)
    assert first['semantic_round'] == second['semantic_round'] == 1
    assert len(requests[0]) == len(requests[1]) == 5


def test_explicit_model_selection_starts_a_new_epoch_but_fallback_does_not(lineage, monkeypatch, tmp_path):
    module, ctx, _, _, args = lineage
    clock = {'wall': 10000, 'monotonic': 100}
    monkeypatch.setattr(module, 'time', SimpleNamespace(
        time=lambda: clock['wall'], monotonic=lambda: clock['monotonic']))
    ctx.execution_deadline = 1900
    requests = []
    monkeypatch.setattr(module, '_verify_claims', lambda rows, allowed, **kwargs: (
        requests.append(copy.deepcopy(rows)) or [_supported(row) for row in rows]))
    for revision in range(3):
        args['analyses'][0]['key_contribution'] = f'Grounded contribution revision {revision}'
        module.academic_check_relations(**args)
    first_state = module._lineage_state()[1]
    clock.update(wall=11801, monotonic=1901)
    retry = _retry_task(ctx, monkeypatch, tmp_path / 'automatic-fallback-task')
    retry.execution_deadline = 3701
    monkeypatch.setattr(module, '_model_metrics', lambda: {'model': 'automatic-fallback-model'})
    checked = module.academic_check_relations(**args)
    assert checked['semantic_round'] == 3 and not requests[-1]
    assert retry._academic_verification_deadline <= clock['monotonic']
    assert module._lineage_state()[1]['step_deadline_at'] == first_state['step_deadline_at']
    args['analyses'][0]['key_contribution'] = 'New explicit-model analysis'
    with pytest.raises(ToolExecutionError, match='three semantic checks'):
        module.academic_check_relations(**args)
    explicit = _retry_task(retry, monkeypatch, tmp_path / 'explicit-model-task')
    explicit.execution_deadline = 3701
    monkeypatch.setattr(module, '_client', lambda: _model_client(2))
    checked = module.academic_check_relations(**args)
    assert checked['semantic_round'] == 1 and len(requests[-1]) == 5
    assert explicit._academic_verification_deadline == 2801
    assert module._lineage_state()[1]['step_deadline_at'] > first_state['step_deadline_at']


def test_failure_before_first_check_keeps_step_deadline_on_retry(lineage, monkeypatch, tmp_path):
    from types import SimpleNamespace
    module, ctx, _, _, _ = lineage
    clock = {'wall': 10000, 'monotonic': 100}
    monkeypatch.setattr(module, 'time', SimpleNamespace(
        time=lambda: clock['wall'], monotonic=lambda: clock['monotonic']))
    ctx.execution_deadline = 1900
    module.academic_expansion_context('exp')
    clock.update(wall=11621, monotonic=1721)
    assert clock['monotonic'] < ctx.execution_deadline
    retry = _retry_task(ctx, monkeypatch, tmp_path / 'retry-before-check')
    retry.execution_deadline = 3521
    module.academic_expansion_context('exp')
    assert retry._academic_verification_deadline <= clock['monotonic']
    # No transport call is made for further evidence acquisition, but metadata can finalize a partial result.
    calls = []

    class Transport:
        def request(self, method, url, **kwargs):
            calls.append(kwargs['timeout'])
            return SimpleNamespace(raise_for_status=lambda: None, json=lambda: {'data': {'record': {}}})

    monkeypatch.setattr(module, '_client', lambda: SimpleNamespace(
        transport=Transport(), base_url='http://core', _headers=lambda: {}))
    assert module._call('POST', '/paper-contents:read', {}) == {'status': 'stage_timeout'}
    assert module._call('POST', '/papers:search', {}) == {'status': 'stage_timeout'}
    assert not calls
    assert module._call('GET', '/expansions/exp') == {'record': {}}
    assert calls == [5]


def test_source_replacement_invalidates_receipt_and_only_rechecks_affected_conclusions(lineage, monkeypatch):
    module, _, events, versions, args = lineage
    calls = []
    monkeypatch.setattr(module, '_verify_claims', lambda claims, allowed, **kwargs: (
        calls.append(copy.deepcopy(claims)) or [_supported(claim) for claim in claims]))
    first = module.academic_check_relations(**args)
    versions['c']['content_hash'] = 'replacement-c'
    with pytest.raises(ToolExecutionError, match='content changed'):
        module.academic_submit_relations(check_id=first['check_id'])
    assert not events
    second = module.academic_check_relations(**args)
    assert {row.get('claim_id', row['paper_id']) for row in calls[1]} == {'c', 'b-c'}
    assert second['check_id'] != first['check_id']


def test_report_projects_only_checked_facts(lineage, monkeypatch):
    module, ctx, events, _, args = lineage
    args['relations'][0]['conditions'] = ''
    monkeypatch.setattr(module, '_verify_claims', lambda rows, allowed, **kwargs: [_supported(row) for row in rows])
    checked = module.academic_check_relations(**args)
    module.academic_submit_relations(check_id=checked['check_id'])
    relation = _artifact(events)
    report = _artifact(events, 'expansion_report')
    assert _artifact(events, 'expansion_result') == relation['input']
    assert report['input'] == relation['input']
    assert report['reading'] == relation['reading']
    assert report['relations'] == relation['relations']
    assert 'reading_plan' not in report and 'reading_order' not in report
    assert '查证范围' not in report['narrative'] and '待解决' not in report['narrative']
    assert 'Invented direct inheritance' not in report['narrative']
    assert 'Different sharing scopes' in report['narrative']
    assert '未建立共同实验条件' not in report['narrative']
    assert report['analysis_version'] == relation['analysis_version']


def test_arbitrary_nonseed_pair_evidence_reaches_the_existing_verifier(lineage, monkeypatch):
    module, _, _, _, args = lineage
    from lazymind.chat.service import llm_task
    requests = []
    monkeypatch.setattr(module, '_call', lambda method, path, payload: {
        'status': 'ready', 'positions': payload['positions'],
        'pages': [{'page': 0, 'text': 'Evidence of ' + payload['paper_id']} for _ in payload['positions']],
        'content_hash': 'hash-' + payload['paper_id'], 'source_version': 'v1'})

    def verify(request, usage, validate_output, **kwargs):
        requests.append(request)
        usage['model_calls'] = 1
        return validate_output(_supported_verification_output(request)), None, None

    monkeypatch.setattr(llm_task, '_run_task', verify)
    checked = module.academic_check_relations(**args)
    assert checked['relations'][0]['verification']['status'] == 'supported'
    pairs = [item for request in requests for item in request.input.data['evidence'] if item['field'] == 'relation']
    assert len(pairs) == 2
    pairs = [item for item in pairs if item['claim_id'] == 'b-c']
    assert pairs[0]['relation_type'] == 'mechanism_comparison'
    request = next(request for request in requests if any(item.get('claim_id') == 'b-c'
                   for item in request.input.data['evidence']))
    assert {page['paper_id'] for page in request.input.data['source_pages_by_key'].values()} >= {'b', 'c'}
    assert all(ref['content_hash'] and ref['source_version'] == 'v1'
               for ref in checked['relations'][0]['evidence'])
    module.academic_submit_relations(check_id=checked['check_id'])
    published = lineage[2][-1]['value']['data']
    assert all('scope_checks' not in row and 'baseline_required' not in row for row in published['claims'])
    assert all('scope_checks' not in row['verification'] for row in published['relations'])


def test_typed_endpoint_without_source_page_keeps_compact_identity(lineage):
    module, ctx, _, _, _ = lineage
    papers = ctx.params['remote_inputs']['candidate_set']['papers']
    papers[0]['paper'].update(authors=['A', 'B', 'C', 'D', 'E'], year=2025,
                              published_at='2025-01-01', source_version_date='2026-01-01', source_version='v2')
    item = {'field': 'relation', 'source_paper_id': 'a', 'target_paper_id': 'b',
            'relation_type': 'design_inspiration', 'statement': 'The named source inspired this design.',
            'conditions': '', 'parts': module._claim_parts('The named source inspired this design.'),
            'available_sources': ['b:0']}
    request = module._verification_request([item], {'b:0': {'text': 'An explicit source attribution.'}},
                                           {'papers': papers, 'user_instruction': '',
                                            'selected_directions': []}, 'model')
    identities = request.input.data['paper_identities']
    assert set(identities) == {'a', 'b'}
    assert identities['a']['authors'] == ['A', 'B', 'C']
    assert identities['a']['authors_total'] == 5 and identities['a']['authors_truncated'] is True
    assert identities['a']['year'] == 2025 and identities['a']['source_version'] == 'v2'
    output = _supported_verification_output(request)
    # A truncated author list alone does not mechanically contradict a supported source witness.
    module._validate_verification(output, request.input.data['evidence'], request.input.data['source_pages_by_key'])
    assert module._verification_verdict(item, output['checks'][0])['status'] == 'supported'


def test_unchecked_fields_and_failed_forward_queries_cannot_mark_research_complete(lineage, monkeypatch):
    module, _, _, _, args = lineage
    monkeypatch.setattr(module, '_verify_claims', lambda rows, allowed, **kwargs: [_supported(row) for row in rows])
    args['claims'] = []
    assert not module.academic_check_relations(**args)['complete']
    args['claims'] = [{'paper_id': pid, 'field': 'key_contribution', 'positions': [0]} for pid in 'abc']
    context = module.academic_expansion_context('exp')
    for query in context['source_queries']:
        query['sources'][0]['status'] = 'provider_failed'
        query['paper_id'] = 'unrelated'
    monkeypatch.setattr(module, '_read_expansion_context', lambda _: context)
    checked = module.academic_check_relations(**args)
    assert not checked['complete']
    assert [(gap['direction'], gap['kind']) for gap in checked['coverage_gaps']] == [('method', 'successor')]
    assert checked['coverage_gaps'][0]['reason']


def test_runtime_source_changes_do_not_consume_semantic_checks_and_report_rejects_stale_content(lineage, monkeypatch):
    module, ctx, events, versions, args = lineage
    monkeypatch.setattr(module, '_verify_claims', lambda rows, allowed, **kwargs: [_supported(row) for row in rows])
    first = module.academic_check_relations(**args)
    versions['b']['content_hash'] = 'newer-b'
    second = module.academic_check_relations(**args)
    assert second['semantic_round'] == first['semantic_round'] == 1
    versions['b']['content_hash'] = 'replaced-again'
    with pytest.raises(ToolExecutionError, match='source content changed since checking'):
        module.academic_submit_relations(check_id=second['check_id'])
    assert not events


def test_revised_conclusion_keeps_cached_seed_evidence(lineage, monkeypatch):
    module, _, _, _, args = lineage
    calls = []
    monkeypatch.setattr(module, '_verify_claims', lambda claims, allowed, **kwargs: (
        calls.append(copy.deepcopy(claims)) or [_supported(claim) for claim in claims]))
    module.academic_check_relations(**args)
    args['analyses'][2]['key_contribution'] = 'Scoped revised mechanism of c'
    module.academic_check_relations(**args)
    assert len(calls[1]) == 1
    assert any(ref['paper_id'] == 'a' for ref in calls[1][0]['_effective_evidence'])


def _with_prepared_clue(module, monkeypatch):
    original = module._read_expansion_context
    context = original('exp')
    context['record']['preparation'] = {
        'research_questions': [{'id': 'prepared-origin', 'direction': 'method', 'category': 'predecessor'}],
        'named_clues': [{'name': 'Original mechanism', 'source_paper_id': 'a',
                         'question_ids': ['prepared-origin'], 'source_excerpt': 'Original mechanism',
                         'source_version': 'v1', 'positions': [0]}]}
    context['record']['reading'] = {'a': {'content_hash': 'hash-a', 'source_version': 'v1', 'positions': [0]}}
    monkeypatch.setattr(module, '_call', lambda method, path, payload: {
        'status': 'ready', 'positions': payload['positions'], 'content_hash': 'hash-a', 'source_version': 'v1',
        'pages': [{'page': 1, 'text': 'Original mechanism'} for _ in payload['positions']]})
    monkeypatch.setattr(module, '_read_expansion_context', lambda _: copy.deepcopy(context))
    return context


def test_unhandled_named_clue_requires_actual_stopping_reason_before_partial_submission(lineage, monkeypatch):
    module, ctx, events, _, args = lineage
    clock = {'wall': 10000, 'monotonic': 100}
    monkeypatch.setattr(module, 'time', SimpleNamespace(
        time=lambda: clock['wall'], monotonic=lambda: clock['monotonic']))
    ctx.execution_deadline = 1000
    prepared_context = _with_prepared_clue(module, monkeypatch)
    monkeypatch.setattr(module, '_verify_claims', lambda rows, allowed, **kwargs: [_supported(row) for row in rows])
    checked = module.academic_check_relations(**args)
    assert not checked['complete']
    gap = next(row for row in checked['coverage_gaps'] if row.get('clue_name') == 'Original mechanism')
    assert gap['question_id'] == 'prepared-origin' and gap['reason']
    assert not checked['can_submit'] and checked['completion'] is None
    assert checked['research_seconds_remaining'] == 720
    with pytest.raises(ToolExecutionError, match='actionable gaps'):
        module.academic_submit_relations(check_id=checked['check_id'])
    state = module._lineage_state()[1]
    assert state['step_deadline_at'] == state['deadline_at'] == 10900
    assert ctx._academic_verification_deadline == 820
    prepared_context['record']['reading']['a'].update(updated_at='later', positions=[0, 99])
    monkeypatch.setattr(module, '_call', lambda *a, **kw: pytest.fail(
        'unchanged validated anchors must not be reread after the science deadline'))
    clock.update(wall=10721, monotonic=821)
    assert clock['monotonic'] < ctx.execution_deadline
    checked = module.academic_check_relations(**args)
    assert checked['completion'] == {'status': 'incomplete', 'reason': 'budget_exhausted'}
    assert checked['research_seconds_remaining'] == 0
    assert ctx.execution_deadline - clock['monotonic'] == 179
    previous_proof = copy.deepcopy(module._lineage_state()[1]['minimum_seed_context'])
    prepared_context['record']['preparation']['named_clues'][0]['source_excerpt'] = 'mechanism'
    checked = module.academic_check_relations(**args)
    assert all(row['status'] == 'supported' for row in checked['checks'])
    assert module._lineage_state()[1]['minimum_seed_context'] == previous_proof
    module.academic_submit_relations(check_id=checked['check_id'])
    assert _artifact(events)['coverage_gaps'] == checked['coverage_gaps']


def test_clue_resolution_requires_a_retained_identity_and_supported_original_source_role(lineage, monkeypatch):
    module, _, _, _, args = lineage
    _with_prepared_clue(module, monkeypatch)
    calls = []
    monkeypatch.setattr(module, '_verify_claims', lambda rows, allowed, **kwargs: (
        calls.append(copy.deepcopy(rows)) or [_supported(row) for row in rows]))
    resolution = {'source_paper_id': 'a', 'name': 'Original mechanism', 'status': 'resolved',
                  'paper_id': 'b', 'reason': 'The registered original method is attributed in the source.',
                  'query_ids': ['exact']}
    args['investigation'][0]['clue_resolutions'] = [resolution]
    first = module.academic_check_relations(**args)
    assert first['complete']
    resolution['paper_id'] = 'c'
    second = module.academic_check_relations(**args)
    assert not second['complete']
    assert any('尚无已支持关系' in row['reason'] for row in second['coverage_gaps'])
    assert calls[1] == []  # A disposition revision reuses unchanged scientific checks.
    resolution['paper_id'] = 'not-retained'
    with pytest.raises(ToolExecutionError, match='final paper set'):
        module.academic_check_relations(**args)


def test_clue_exclusion_and_obstacles_are_recorded_without_inventing_scientific_relations(lineage, monkeypatch):
    module, _, _, _, args = lineage
    _with_prepared_clue(module, monkeypatch)
    monkeypatch.setattr(module, '_verify_claims', lambda rows, allowed, **kwargs: [_supported(row) for row in rows])
    disposition = {'source_paper_id': 'a', 'name': 'Original mechanism', 'status': 'excluded',
                   'reason': 'The named implementation is outside the confirmed research scope.', 'query_ids': []}
    item = args['investigation'][0]
    item['clue_resolutions'] = [disposition]
    assert module.academic_check_relations(**args)['complete']
    disposition.update(status='blocked', reason='The original PDF returns HTTP 403; no accessible copy was found.')
    item['status'] = 'blocked'
    checked = module.academic_check_relations(**args)
    assert not checked['complete']
    assert any('HTTP 403' in row['reason'] and row.get('clue_name') == 'Original mechanism'
               for row in checked['coverage_gaps'])
    disposition['paper_id'] = 'b'
    with pytest.raises(ToolExecutionError, match='Original mechanism.*only resolved clues may carry paper_id'):
        module.academic_check_relations(**args)
    disposition.pop('paper_id')
    disposition['query_ids'] = ['invented-receipt']
    with pytest.raises(ToolExecutionError, match='actual task query receipts'):
        module.academic_check_relations(**args)
    disposition['query_ids'] = []
    disposition['reason'] = ' '
    with pytest.raises(ToolExecutionError, match='reason'):
        module.academic_check_relations(**args)


def test_a_seed_named_mechanism_can_resolve_through_its_own_verified_contribution(lineage, monkeypatch):
    module, _, _, _, args = lineage
    _with_prepared_clue(module, monkeypatch)
    monkeypatch.setattr(module, '_verify_claims', lambda rows, allowed, **kwargs: [_supported(row) for row in rows])
    args['investigation'][2]['clue_resolutions'] = [{
        'source_paper_id': 'a', 'name': 'Original mechanism', 'status': 'resolved', 'paper_id': 'a',
        'reason': 'The named mechanism is this paper’s own contribution, not a predecessor paper.', 'query_ids': []}]
    checked = module.academic_check_relations(**args)
    assert checked['complete']
    assert all(edge['source_paper_id'] != edge['target_paper_id'] for edge in checked['relations'])


def test_selection_changes_require_exact_reasons_and_preserve_bound_removed_metadata(lineage, monkeypatch):
    module, ctx, events, versions, args = lineage
    baseline = copy.deepcopy(ctx.params['remote_inputs']['candidate_set'])
    args['analyses'] = args['analyses'][:2]
    args['claims'] = args['claims'][:2]
    args['relations'] = [args['relations'][1]]
    args['research_structure']['nodes'] = args['research_structure']['nodes'][:1]
    for item in args['investigation']:
        item['paper_ids'] = ['a', 'b']
        item['relation_ids'] = ['a-b']
    with pytest.raises(ToolExecutionError, match='exactly every added or removed'):
        module.academic_check_relations(**args)
    assert ctx.params['remote_inputs']['candidate_set'] == baseline
    assert events == []
    monkeypatch.setattr(module, '_verify_claims', lambda rows, allowed, **kwargs: [_supported(row) for row in rows])
    args['selection_reasons'] = [{'paper_id': 'c', 'reason': 'This paper concerns a different research object.'}]
    ctx.execution_deadline = time.monotonic() - 1
    checked = module.academic_check_relations(**args)
    module.academic_submit_relations(check_id=checked['check_id'])
    relation = _artifact(events)
    assert relation['selection'] == {'before_count': 3, 'changes': [{
        'action': 'removed', 'paper': baseline['papers'][2],
        'reason': 'This paper concerns a different research object.'}]}
    assert ctx.params['remote_inputs']['candidate_set'] == baseline
    # The checked receipt owns its removed metadata even if the live upstream later changes.
    ctx.params['remote_inputs']['candidate_set']['papers'][2]['paper']['title'] = 'Changed upstream title'
    assert relation['selection']['changes'][0]['paper'] == baseline['papers'][2]
    assert relation['selection']['changes'][0]['paper']['paper']['paper_id'] == 'c'
    assert relation['selection']['changes'][0]['paper']['is_seed'] is False
    assert relation['selection']['changes'][0]['paper']['is_important'] is False


def test_selection_added_paper_uses_registered_metadata_and_rejects_extra_reasons(lineage, monkeypatch):
    module, ctx, _, _, args = lineage
    bound = ctx.params['remote_inputs']['candidate_set']
    registered = copy.deepcopy(bound['papers'])
    registered.append({'paper': {'paper_id': 'd', 'title': 'Authoritative additional method'},
                       'is_seed': False, 'is_important': False})
    monkeypatch.setattr(module, '_resolve', lambda ids: {
        'papers': [copy.deepcopy(row) for row in registered if row['paper']['paper_id'] in ids],
        'contents': [{'paper_id': identity, 'status': 'failed'} for identity in ids]})
    analyses = [*args['analyses'], {'paper_id': 'd'}]
    reason = {'paper_id': 'd', 'reason': 'Adds the original method used by the seed.'}
    result, _, _, selection = module._lineage_input(analyses, [reason])
    assert selection == {'before_count': 3, 'changes': [{
        'action': 'added', 'paper': registered[-1], 'reason': reason['reason']}]}
    assert [row['paper']['paper_id'] for row in result['papers']] == list('abcd')
    with pytest.raises(ToolExecutionError, match='one concrete reason'):
        module._lineage_input(analyses, [reason, {'paper_id': 'b', 'reason': 'unchanged'}])
    with pytest.raises(ToolExecutionError, match='one concrete reason'):
        module._lineage_input(analyses, [reason, reason])
    assert module._lineage_input(args['analyses'], [])[3] == {'before_count': 3, 'changes': []}


def test_report_requires_supported_scope_as_well_as_supported_fact(lineage, monkeypatch):
    module, _, events, _, args = lineage
    monkeypatch.setattr(module, '_verify_claims', lambda rows, allowed, **kwargs: [_supported(row) for row in rows])
    checked = module.academic_check_relations(**args)
    module.academic_submit_relations(check_id=checked['check_id'])
    relation = _artifact(events)
    assert 'Different sharing scopes' in module._checked_report_narrative(relation)
    relation['relations'][0]['verification']['scope_status'] = 'uncertain'
    narrative = module._checked_report_narrative(relation)
    assert 'Different sharing scopes' not in narrative
    assert 'Design attribution' in narrative


def test_published_selection_added_paper_has_workflow_paper_json_shape(lineage, monkeypatch):
    module, ctx, events, _, args = lineage
    original_resolve = module._resolve
    added = {'paper': {'paper_id': 'd', 'title': 'Registered predecessor', 'doi': '10.test/origin'},
             'is_seed': False, 'is_important': True}
    raw = {'key_contribution': 'Unverified performance improvement',
           'seed_relation': 'Unverified seed inheritance', 'direction_relation': 'Unverified direction comparison'}
    removed = ctx.params['remote_inputs']['candidate_set']['papers'][2]
    removed.update({field: 'Old unverified ' + field for field in raw})
    frozen_removed_metadata = {key: copy.deepcopy(removed[key]) for key in ('paper', 'is_seed', 'is_important')}

    def resolve(ids):
        value = original_resolve([identity for identity in ids if identity != 'd'])
        if 'd' in ids:
            value['papers'].append(copy.deepcopy(added))
            value['contents'].append({'paper_id': 'd', 'status': 'failed'})
        return value

    monkeypatch.setattr(module, '_resolve', resolve)
    monkeypatch.setattr(module, '_verify_claims', lambda rows, allowed, **kwargs: [_supported(row) for row in rows])
    args['analyses'] = [row for row in args['analyses'] if row['paper_id'] != 'c'] + [{'paper_id': 'd', **raw}]
    args['claims'] = [row for row in args['claims'] if row['paper_id'] != 'c']
    args['relations'] = [row for row in args['relations'] if row['id'] != 'b-c']
    args['research_structure']['nodes'] = [args['research_structure']['nodes'][0], {
        'paper_id': 'd', 'direction_id': 'method', 'branch_question': 'Original design?',
        'reason': 'Provides original design context'}]
    for item in args['investigation']:
        item.update(paper_ids=['a', 'b', 'd'], relation_ids=['a-b'])
    args['selection_reasons'] = [
        {'paper_id': 'd', 'reason': 'Original design used by the seed.'},
        {'paper_id': 'c', 'reason': 'Different research object.'}]
    checked = module.academic_check_relations(**args)
    module.academic_submit_relations(check_id=checked['check_id'])
    receipt = _artifact(events)
    added_change = next(row for row in receipt['selection']['changes'] if row['action'] == 'added')
    removed_change = next(row for row in receipt['selection']['changes'] if row['action'] == 'removed')
    assert added_change == {'action': 'added', 'paper': added, 'reason': 'Original design used by the seed.'}
    assert removed_change == {
        'action': 'removed', 'paper': frozen_removed_metadata, 'reason': 'Different research object.'}
    assert added_change['paper']['paper']['paper_id'] == 'd'
    assert added_change['paper']['paper']['title'] == 'Registered predecessor'
    assert added_change['paper']['is_seed'] is False
    assert added_change['paper']['is_important'] is True
    checked_paper = next(row for row in receipt['input']['papers'] if row['paper']['paper_id'] == 'd')
    for field, statement in raw.items():
        assert field not in added_change['paper'] and field not in removed_change['paper']
        assert checked_paper[field] != statement
        verdict = next(row for row in receipt['claims'] if row['paper_id'] == 'd' and row['field'] == field)
        assert verdict['status'] == 'uncertain' and verdict['statement'] == statement
    assert removed['key_contribution'] == 'Old unverified key_contribution'


@pytest.mark.parametrize('stopping_reason', ['verified', 'material_limited', 'technical_failure'])
def test_checked_completion_is_frozen_in_published_analysis_and_report(lineage, monkeypatch, stopping_reason):
    module, ctx, events, versions, args = lineage

    def verify(rows, allowed, **kwargs):
        return [dict(_supported(row), status='uncertain', verification_error='task_timeout')
                if stopping_reason == 'technical_failure' else _supported(row) for row in rows]
    monkeypatch.setattr(module, '_verify_claims', verify)
    if stopping_reason == 'material_limited':
        item = args['investigation'][0]
        item.update(status='blocked', gaps=['Original method source returns HTTP 403; no accessible copy found.'])
        versions['b']['status'] = 'failed'
    checked = module.academic_check_relations(**args)
    expected = {'status': 'complete' if stopping_reason == 'verified' else 'incomplete', 'reason': stopping_reason}
    assert checked['completion'] == expected and checked['can_submit']
    module.academic_submit_relations(check_id=checked['check_id'])
    relation = _artifact(events)
    assert relation['completion'] == expected
    assert relation['analysis_version'] == checked['analysis_version']
    report = _artifact(events, 'expansion_report')
    assert report['completion'] == relation['completion']
    assert report['research_complete'] == relation['research_complete']
    assert report['coverage_gaps'] == relation['coverage_gaps']
    assert report['selection'] == relation['selection']


@pytest.mark.parametrize('first_tool', ['academic_search_papers', 'academic_read_paper'])
def test_real_research_quotas_allow_partial_submission_only_when_both_routes_are_exhausted(
        lineage, monkeypatch, first_tool):
    from lazyllm.tools.agent import ToolManager, fc_register
    from lazymind.chat.engine.agent_runtime.tool_call_guard import ToolCallQuota, ToolExecutionMiddleware
    from lazymind.chat.engine.tools.workspace_context import WorkspaceContext
    from lazymind.chat.engine.subagent.runner import _build_subagent_plan

    module, ctx, events, _, args = lineage
    _with_prepared_clue(module, monkeypatch)
    monkeypatch.setattr(module, '_verify_claims', lambda rows, allowed, **kwargs: [_supported(row) for row in rows])
    calls = []

    @fc_register(host_file='NONE')
    def academic_read_paper():
        '''Read available source evidence.'''
        calls.append('academic_read_paper')
        return {'status': 'ready'}

    @fc_register(host_file='NONE')
    def academic_search_papers():
        '''Search alternative source identities.'''
        calls.append('academic_search_papers')
        return {'papers': []}

    plan = _build_subagent_plan(ctx, None, tools=[], tool_prompt_appendices={})
    ctx.execution_deadline = time.monotonic() + 1800
    quota = ToolCallQuota({'academic_read_paper': 1, 'academic_search_papers': 1},
                          observer=plan.execution_options.tool_call_budget_observer)
    middleware = ToolExecutionMiddleware(
        ToolManager([academic_read_paper, academic_search_papers]), call_quota=quota,
        workspace_permission=WorkspaceContext(local_runtime=False))

    def execute(name, identity):
        return middleware.execute_with_records({'id': identity, 'function': {'name': name, 'arguments': {}}})

    execute(first_tool, 'first')
    first = module.academic_check_relations(**args)
    assert not first['can_submit'] and first['completion'] is None
    assert first['tool_call_budget'][first_tool] == {'limit': 1, 'used': 1, 'remaining': 0}
    with pytest.raises(ToolExecutionError, match='actionable gaps'):
        module.academic_submit_relations(check_id=first['check_id'])
    second_tool = 'academic_read_paper' if first_tool == 'academic_search_papers' else 'academic_search_papers'
    execute(second_tool, 'second')
    blocked = execute(first_tool, 'over-limit')
    assert 'call cap' in str(blocked.results)
    assert calls == [first_tool, second_tool]
    second = module.academic_check_relations(**args)
    assert second['semantic_round'] == first['semantic_round'] == 1
    assert second['can_submit']
    assert second['completion'] == {'status': 'incomplete', 'reason': 'budget_exhausted'}
    assert first['tool_call_budget'][second_tool]['remaining'] == 1
    module.academic_submit_relations(check_id=second['check_id'])
    receipt = _artifact(events)
    assert receipt['completion'] == second['completion']
    assert receipt['tool_call_budget'] == second['tool_call_budget']


@pytest.mark.asyncio
async def test_large_frozen_inputs_use_existing_path_transport_through_context_check_and_report(lineage, monkeypatch):
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
        assert inputs[material] in prompt
        assert 'Full frozen metadata' not in prompt
        return inputs[material]

    candidate_path = await bind('candidate_set')
    visible = module.academic_expansion_context('exp')
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
    monkeypatch.setattr(module, '_verify_claims', lambda rows, allowed, **kwargs: [_supported(row) for row in rows])
    checked = module.academic_check_relations(**args)
    module.academic_submit_relations(check_id=checked['check_id'])
    relation = _artifact(events)
    assert relation['input']['papers'][0]['paper']['abstract'] == candidate['papers'][0]['paper']['abstract']
    report = _artifact(events, 'expansion_report')
    assert report['input'] == relation['input']
    assert report['relations'] == relation['relations']
    assert report['analysis_version'] == relation['analysis_version']


@pytest.mark.parametrize('symlink', [False, True])
def test_bound_path_json_rejects_external_files_and_escaping_symlinks(lineage, monkeypatch, tmp_path, symlink):
    import lazyllm
    module, ctx, _, _, _ = lineage
    external = tmp_path.parent / (tmp_path.name + '-outside.json')
    external.write_text('{"papers": []}', encoding='utf-8')
    path = external
    if symlink:
        path = Path(ctx.workspace_path) / 'external-link.json'
        path.symlink_to(external)
    monkeypatch.setattr(lazyllm, 'globals', {'agentic_config': {
        'agent_type': 'workflow_step', 'workflow_workspace_path': ctx.workspace_path}})
    ctx.params.update(remote_inputs={'candidate_set': str(path)},
                      remote_input_types={'candidate_set': 'json'},
                      remote_input_transports={'candidate_set': 'path'})
    try:
        with pytest.raises(ValueError, match='current Workflow workspace'):
            module._bound('candidate_set')
    finally:
        external.unlink()


def test_scientific_completion_reserve_does_not_extend_declared_hard_timeout(monkeypatch):
    import asyncio
    from lazymind.chat.workflow.execution_policy import bounded_frames

    original_timeout = asyncio.timeout
    declared_timeouts, stopped, closed = [], [], []

    def expire_at_next_suspension(seconds):
        declared_timeouts.append(seconds)
        return original_timeout(0)

    monkeypatch.setattr(asyncio, 'timeout', expire_at_next_suspension)
    params = {'step_id': 'analyze_relations', 'workflow_runtime': {'execution_limits': {
        'analyze_relations': {'rounds': 32, 'timeout': 1800}}}}

    async def frames():
        try:
            yield 'started'
            await asyncio.Event().wait()
        finally:
            closed.append(True)

    async def collect():
        return [frame async for frame in bounded_frames(frames(), params, lambda: stopped.append(True))]

    with pytest.raises(RuntimeError, match='WORKFLOW_STEP_TIMEOUT: analyze_relations exceeded its declared timeout'):
        asyncio.run(collect())
    assert declared_timeouts == [1800]
    assert stopped == closed == [True]


def test_prepared_seed_body_context_is_scope_checked_shared_and_cached(lineage, monkeypatch):
    from lazymind.chat.service import llm_task
    module, _, _, versions, args = lineage
    current = module._read_expansion_context('exp')
    clues = [
        {'name': 'Core', 'source_paper_id': 'a', 'source_version': 'v1', 'positions': [1, 2],
         'source_excerpt': 'Core condition.', 'question_ids': ['selected']},
        {'name': 'Other scope', 'source_paper_id': 'a', 'source_version': 'old', 'positions': [99],
         'source_excerpt': 'not selected', 'question_ids': ['outside']},
        {'name': 'Non-seed', 'source_paper_id': 'b', 'source_version': 'old', 'positions': [99],
         'source_excerpt': 'not a seed', 'question_ids': ['selected']},
        {'name': 'Reference', 'source_paper_id': 'a', 'source_version': 'v1', 'positions': [5],
         'reference_key': '7', 'source_excerpt': 'Original reference.', 'question_ids': ['selected']},
        {'name': 'Reference only', 'source_paper_id': 'a', 'source_version': 'v1',
         'reference_key': '8', 'source_excerpt': 'Not a body position.', 'question_ids': ['selected']}]
    current['record']['preparation'] = {
        'research_questions': [{'id': 'selected', 'direction': 'method', 'category': 'predecessor'},
                               {'id': 'outside', 'direction': 'other', 'category': 'peer'}], 'named_clues': clues}
    current['record']['reading'] = {'a': {'content_hash': 'hash-a', 'source_version': 'v1',
                                          'positions': [0, 1, 2, 3, 4, 5]}}
    monkeypatch.setattr(module, '_read_expansion_context', lambda _: copy.deepcopy(current))
    read_calls, requests = [], []
    body = {0: 'Source mechanism.', 1: 'Core', 2: 'condition.', 3: 'New evidence.', 5: 'Unrelated body.'}

    def read(method, path, payload):
        read_calls.append(copy.deepcopy(payload))
        result = {'status': 'ready', 'content_hash': versions[payload['paper_id']]['content_hash'],
                  'source_version': 'v1'}
        if payload.get('view') == 'references':
            return {**result, 'references': [{'reference_key': '7', 'raw_text': 'Original reference.'}]}
        return {**result, 'positions': payload['positions'], 'pages': [
            {'page': pos + 1, 'text': body[pos] if payload['paper_id'] == 'a' else 'Other mechanism.'}
            for pos in payload['positions']]}

    def verify(request, usage, validate_output, **kwargs):
        requests.append(request)
        usage['model_calls'] = 1
        return validate_output(_supported_verification_output(request)), None, None

    monkeypatch.setattr(module, '_call', read)
    monkeypatch.setattr(llm_task, '_run_task', verify)
    first = module.academic_check_relations(**args)
    assert len(requests) == 5
    assert all({page['position'] for page in req.input.data['source_pages_by_key'].values()
                if page['paper_id'] == 'a'} == {0, 1, 2} for req in requests)
    receipt = module._lineage_state()[1]['receipts'][first['check_id']]
    assert all(next(ref for ref in row['evidence'] if ref['paper_id'] == 'a')['positions'] == [0, 1, 2]
               for row in receipt['relations'])
    assert all('reference_keys' not in call or 'positions' not in call for call in read_calls)
    assert not any(99 in call.get('positions', []) for call in read_calls)
    # A changed excerpt on the same verified body pages does not change the scientific input.
    clues[0]['source_excerpt'] = 'Core'
    requests.clear()
    module.academic_check_relations(**args)
    assert not requests
    # Regrouping an already shared seed page changes this claim, not all unrelated successful verdicts.
    args['claims'][0]['positions'] = [0, 1]
    requests.clear()
    second = module.academic_check_relations(**args)
    assert len(requests) == 1
    assert requests[0].input.data['evidence'][0]['field'] == 'key_contribution'
    assert requests[0].input.data['evidence'][0]['paper_id'] == 'a'
    clues.append({'name': 'New', 'source_paper_id': 'a', 'source_version': 'v1', 'positions': [3],
                  'source_excerpt': 'New evidence.', 'question_ids': ['selected']})
    requests.clear()
    third = module.academic_check_relations(**args)
    assert len(requests) == 5 and third['semantic_round'] == second['semantic_round']
    assert all({page['position'] for page in req.input.data['source_pages_by_key'].values()
                if page['paper_id'] == 'a'} == {0, 1, 2, 3} for req in requests)


@pytest.mark.parametrize('mismatch', ['reading_hash', 'reading_version', 'unread_position', 'clue_version',
                                      'source_hash', 'source_version', 'noncontiguous_excerpt'])
def test_prepared_seed_source_inconsistency_rejects_before_scientific_calls(lineage, monkeypatch, mismatch):
    module, _, events, _, args = lineage
    current = module._read_expansion_context('exp')
    clue = {'name': 'Core', 'source_paper_id': 'a', 'source_version': 'v1', 'positions': [1, 2],
            'source_excerpt': 'Core condition.', 'question_ids': ['selected']}
    current['record']['preparation'] = {
        'research_questions': [{'id': 'selected', 'direction': 'method', 'category': 'predecessor'}],
        'named_clues': [clue]}
    reading = {'content_hash': 'hash-a', 'source_version': 'v1', 'positions': [1, 2, 3]}
    current['record']['reading'] = {'a': reading}
    if mismatch == 'reading_hash':
        reading['content_hash'] = 'changed'
    elif mismatch == 'reading_version':
        reading['source_version'] = 'v2'
    elif mismatch == 'unread_position':
        reading['positions'] = [1]
    elif mismatch == 'clue_version':
        clue['source_version'] = 'v2'
    elif mismatch == 'noncontiguous_excerpt':
        clue['positions'] = [1, 3]
    monkeypatch.setattr(module, '_read_expansion_context', lambda _: current)
    monkeypatch.setattr(module, '_call', lambda method, path, payload: {
        'status': 'ready', 'content_hash': 'changed' if mismatch == 'source_hash' else 'hash-a',
        'source_version': 'v2' if mismatch == 'source_version' else 'v1', 'positions': payload['positions'],
        'pages': [{'page': pos + 1, 'text': 'Core' if pos == 1 else 'condition.'} for pos in payload['positions']]})
    calls = []
    monkeypatch.setattr(module, '_verify_claims', lambda *a, **kw: calls.append(a))
    with pytest.raises(ToolExecutionError, match='prepared seed'):
        module.academic_check_relations(**args)
    assert not calls and not events
    assert not module._lineage_state()[1]['receipts']


@pytest.mark.parametrize('existing_proof', [False, True])
def test_expired_unverified_seed_context_returns_safe_partial_without_reusing_old_verdicts(
        lineage, monkeypatch, existing_proof):
    from lazymind.chat.service import llm_task
    module, ctx, events, versions, args = lineage
    clock = {'wall': 10000, 'monotonic': 100}
    monkeypatch.setattr(module, 'time', SimpleNamespace(
        time=lambda: clock['wall'], monotonic=lambda: clock['monotonic']))
    ctx.execution_deadline = 1000 if existing_proof else 280
    prepared_context = _with_prepared_clue(module, monkeypatch)
    monkeypatch.setattr(module, '_call', lambda method, path, payload: {
        'status': 'ready', 'positions': payload['positions'],
        'content_hash': versions[payload['paper_id']]['content_hash'], 'source_version': 'v1',
        'pages': [{'page': pos + 1, 'text': 'Original mechanism'} for pos in payload['positions']]})

    def supported(request, usage, validate_output, **kwargs):
        usage['model_calls'] = 1
        return validate_output(_supported_verification_output(request)), None, None

    monkeypatch.setattr(llm_task, '_run_task', supported)
    previous_proof, previous_calls = None, 0
    if existing_proof:
        module.academic_check_relations(**args)
        previous = module._lineage_state()[1]
        previous_proof, previous_calls = copy.deepcopy(previous['minimum_seed_context']), previous['model_calls']
        assert previous['cache'] and all(row['status'] == 'supported' for row in previous['cache'].values())
        # New body context cannot reuse the previous scientific verdicts once the window is exhausted.
        prepared_context['record']['preparation']['named_clues'][0]['positions'] = [1]
        prepared_context['record']['reading']['a']['positions'] = [0, 1]
        clock.update(wall=10721, monotonic=821)
    monkeypatch.setattr(module, '_call', lambda *a, **kw: pytest.fail('expired check must not read new sources'))
    monkeypatch.setattr(llm_task, '_run_task', lambda *a, **kw: pytest.fail('expired check must not call a model'))
    checked = module.academic_check_relations(**args)
    assert checked['completion'] == {'status': 'incomplete', 'reason': 'budget_exhausted'}
    assert checked['can_submit'] and checked['research_seconds_remaining'] == 0
    assert all(row['status'] == 'uncertain' and row['verification_error'] == 'stage_timeout'
               for row in checked['checks'])
    assert all(row['verification']['status'] == 'uncertain'
               and row['verification']['verification_error'] == 'stage_timeout' for row in checked['relations'])
    assert all(not ref['content_hash'] and not ref['positions'] for row in checked['relations']
               for ref in row['evidence'])
    state = module._lineage_state()[1]
    assert state['minimum_seed_context'] == previous_proof and state['model_calls'] == previous_calls
    assert len(state['cache']) == (5 if existing_proof else 0)
    if existing_proof:
        assert all(row['status'] == 'supported' for row in state['cache'].values())
    assert clock['monotonic'] < ctx.execution_deadline
    module.academic_submit_relations(check_id=checked['check_id'])
    assert _artifact(events)['completion'] == checked['completion']


def test_baseline_relations_follow_investigation_topics_without_changing_primary_membership(lineage, monkeypatch):
    module, ctx, events, _, args = lineage
    body = ctx.params['remote_inputs']['candidate_set']
    body['selected_directions'] = body['candidate_directions'] = ['method', 'storage']
    args['research_structure']['directions'].append({'id': 'storage', 'name': 'storage',
                                                    'question': 'How does persistent storage change?'})
    args['research_structure']['nodes'][1]['direction_id'] = 'storage'
    baseline = {**copy.deepcopy(args['relations'][1]), 'id': 'storage-baseline',
                'source_paper_id': 'b', 'target_paper_id': 'a', 'relation_type': 'method_modification',
                'statement': 'Persistent storage is revised with bounded recovery.',
                'conditions': 'Under the stated deployment and recovery conditions.'}
    comparison = {**copy.deepcopy(baseline), 'id': 'storage-comparison', 'relation_type': 'mechanism_comparison',
                  'statement': 'The baseline and revised storage have different recovery costs.'}
    orphan = {**copy.deepcopy(args['relations'][0]), 'id': 'unlinked',
              'statement': 'Unlinked established relation.'}
    args['relations'].extend([baseline, comparison, orphan])
    for item in list(args['investigation']):
        extra = copy.deepcopy(item)
        extra.update(question_id='storage-' + item['kind'], direction='storage',
                     conclusion='Unverified investigation note must not become report facts.',
                     relation_ids=['storage-baseline'] if item['kind'] == 'predecessor'
                     else ['storage-comparison'] if item['kind'] == 'peer' else [])
        args['investigation'].append(extra)
    monkeypatch.setattr(module, '_verify_claims', lambda rows, allowed, **kwargs: [_supported(row) for row in rows])
    checked = module.academic_check_relations(**args)
    assert checked['complete'] and not checked['coverage_gaps']
    module.academic_submit_relations(check_id=checked['check_id'])
    relation = _artifact(events)
    assert relation['research_structure'] == args['research_structure']
    assert next(row for row in relation['research_structure']['nodes'] if row['paper_id'] == 'b')[
        'direction_id'] == 'method'
    report = _artifact(events, 'expansion_report')
    method_text, storage_text = report['narrative'].split('## storage', 1)
    assert baseline['statement'] not in method_text and comparison['statement'] not in method_text
    assert baseline['statement'] in storage_text and baseline['conditions'] in storage_text
    assert comparison['statement'] in storage_text and '### 条件比较' in storage_text
    assert orphan['statement'] not in report['narrative']
    assert any(row['id'] == 'unlinked' for row in report['relations'])
    assert 'Unverified investigation note' not in report['narrative']
    assert report['relations'] == relation['relations'] and report['input'] == relation['input']
