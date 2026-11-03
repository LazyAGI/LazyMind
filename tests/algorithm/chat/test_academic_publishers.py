import copy
from pathlib import Path

import pytest
from lazyllm.tools.agent import ToolExecutionError, ToolManager
from lazymind.chat.engine.subagent import context, tools


@pytest.fixture
def publisher(tmp_path, monkeypatch):
    path = Path(__file__).resolve().parents[3] / 'workflows/paper_expansion/scripts/tools.py'
    from lazymind.workflow_toolkit import load_workflow_package_tools
    package = {'files': {'scripts/tools.py': path.read_bytes()}}
    functions = load_workflow_package_tools(
        package, ['academic_check_relations', 'academic_validate_stage',
                  'academic_publish_stage'],
        'paper_expansion', 'test-revision',
    )
    namespace = functions['academic_check_relations'].__globals__

    class Module:
        """Replace HTTP in the actual loaded namespace."""

    module = Module()
    # Keep attributes in the real namespace so monkeypatch can inspect and
    # restore them, including pytest versions that use static attribute lookup.
    module.__dict__ = namespace
    body = {'user_instruction': 'read actual evidence', 'candidate_directions': ['agents'],
            'selected_directions': ['agents'], 'papers': [
                {'paper': {'paper_id': 'a', 'title': 'model title'}, 'is_seed': False, 'is_important': False}]}
    events = []
    ctx = context.SubAgentContext('task', 'chat', 'workflow_step', 'analyze', {
        'step_id': 'analyze_relations', 'remote_inputs': {'candidate_set': body, 'expansion_id': 'exp'},
        'workflow_runtime': {'publisher_owned_slots': ['relation_analysis', 'expansion_result', 'expansion_report']},
    }, str(tmp_path), [], ['relation_analysis', 'expansion_result', 'expansion_report'], None, events.append)
    monkeypatch.setattr(context, 'require_context', lambda: ctx)
    monkeypatch.setattr(tools, 'require_context', lambda: ctx)
    resolved = {'papers': [{'paper': {'paper_id': 'a', 'title': 'server title', 'source_version': 'v2'},
                           'is_seed': True, 'is_important': True}],
                'contents': [{'paper_id': 'a', 'status': 'ready'}]}
    monkeypatch.setattr(module, '_resolve', lambda ids: copy.deepcopy(resolved))
    monkeypatch.setattr(module, '_read_expansion_context',
                        lambda _: {'record': {'result': copy.deepcopy(body), 'reading': {}}})
    monkeypatch.setattr(module, '_model_metrics', lambda: {})
    return module, ctx, events, resolved


def test_direction_entry_accepts_no_seeds_and_seed_entry_cannot_drop_them(publisher, monkeypatch):
    module, ctx, _, resolved = publisher
    ctx.params['step_id'] = 'expand_and_screen'
    body = ctx.params['remote_inputs']['candidate_set']
    monkeypatch.setattr(module, '_read_expansion_context', lambda _: {'record': {'result': {'papers': []}}})
    assert module.academic_validate_stage(body)['status'] == 'valid'
    monkeypatch.setattr(module, '_read_expansion_context', lambda _: {
        'record': {'result': {'papers': [{'paper': {'paper_id': 'seed'}, 'is_seed': True}]}}})
    with pytest.raises(ToolExecutionError, match='all selected seed'):
        module.academic_validate_stage(body)
    for invalid in [{'test': True}, {}]:
        with pytest.raises(ToolExecutionError):
            module.academic_validate_stage(invalid)


def test_candidates_publish_authoritative_papers_once(publisher, monkeypatch):
    module, ctx, events, resolved = publisher
    ctx.params['step_id'] = 'expand_and_screen'
    ctx.output_slots = ['candidate_set', 'screening_not_required', 'expansion_list_ready']
    ctx.params['workflow_runtime']['publisher_owned_slots'].append('candidate_set')
    source = copy.deepcopy(ctx.params['remote_inputs']['candidate_set'])
    source['papers'] = []
    resolved['papers'][0]['is_seed'] = False
    monkeypatch.setattr(module, '_read_expansion_context', lambda _: {'record': {
        'result': source, 'preparation': {
            'directions': [{'name': 'agents', 'question': 'How do agents coordinate?'},
                           {'name': 'unselected', 'question': 'Other question'}],
            'research_questions': [{'direction': 'agents', 'question': 'Internal predecessor investigation'}]}}})

    args = (['a'], ['Keep the registered representative.'], [])
    assert module.academic_publish_stage(*args)['status'] == 'ok'
    assert events[0]['slot'] == 'candidate_set'
    result = events[0]['value']['data']
    assert result['papers'] == resolved['papers']
    assert result['decisions'] == args[1]
    assert result['directions'] == [{'name': 'agents', 'question': 'How do agents coordinate?'}]
    assert 'research_questions' not in result
    assert module.academic_publish_stage(*args)['already_published'] is True
    assert len(events) == 2


def test_invalid_task_papers_reports_context_error_without_publishing(publisher, monkeypatch):
    module, ctx, events, _ = publisher
    ctx.params['step_id'] = 'expand_and_screen'
    source = copy.deepcopy(ctx.params['remote_inputs']['candidate_set'])
    source['papers'] = None
    monkeypatch.setattr(module, '_read_expansion_context', lambda _: {'record': {'result': source}})
    with pytest.raises(RuntimeError, match='Invalid task context: papers must be an array'):
        module.academic_publish_stage(['a'], [], [])
    assert events == []


@pytest.mark.parametrize(('goal', 'mode', 'ends_with_list'), [
    ('expansion', 'fast', True), ('review', 'balanced', False),
])
def test_paper_list_marker_is_only_published_for_lightweight_expansion(
        publisher, monkeypatch, goal, mode, ends_with_list):
    module, ctx, events, resolved = publisher
    ctx.params['step_id'] = 'expand_and_screen'
    ctx.output_slots[:] = ['candidate_set', 'screening_not_required', 'expansion_list_ready']
    resolved['papers'].append({'paper': {'paper_id': 'b', 'title': 'Related'}, 'is_seed': False, 'is_important': False})
    monkeypatch.setattr(module, '_read_expansion_context', lambda _: {'record': {
        'goal': goal, 'budget_mode': mode, 'result': ctx.params['remote_inputs']['candidate_set'],
        'preparation': {'directions': [{'name': 'agents', 'question': 'How do they coordinate?'}]},
    }})
    recommendation = {'paper_id': 'b', 'reason': 'Directly studies coordination', 'direction': 'agents'}
    if goal == 'expansion':
        with pytest.raises(ToolExecutionError, match='cover all non-seed papers'):
            module.academic_publish_stage(['a', 'b'], [], [])
        assert events == []
    result = module.academic_publish_stage(
        ['a', 'b'], [], [], recommendations=[recommendation] if goal == 'expansion' else None)
    assert result['status'] == 'ok'
    expected_slots = ['candidate_set', 'screening_not_required']
    if ends_with_list:
        expected_slots.append('expansion_list_ready')
    assert [event['slot'] for event in events] == expected_slots
    if goal == 'expansion':
        assert events[0]['value']['data']['recommendations'] == [recommendation]


@pytest.mark.parametrize('mode', ['balanced', 'deep'])
def test_standard_and_deep_publish_the_list_only_after_screening(publisher, monkeypatch, mode):
    module, ctx, events, resolved = publisher
    ctx.params['step_id'] = 'expand_and_screen'
    ctx.output_slots[:] = ['candidate_set', 'screening_not_required', 'expansion_list_ready']
    resolved['papers'].append({'paper': {'paper_id': 'b', 'title': 'Related'}, 'is_seed': False, 'is_important': False})
    monkeypatch.setattr(module, '_read_expansion_context', lambda _: {'record': {
        'goal': 'expansion', 'budget_mode': mode, 'result': ctx.params['remote_inputs']['candidate_set'],
    }})
    module.academic_publish_stage(['a', 'b'], [], [])
    assert [event['slot'] for event in events] == ['candidate_set']
    draft = events[0]['value']['data']
    assert draft['screening_pending'] and 'list_mode' not in draft

    ctx.params['step_id'] = 'screen_and_supplement'
    ctx.output_slots[:] = ['screened_candidates', 'screened_list_ready']
    recommendation = {'paper_id': 'b', 'reason': 'Directly studies coordination', 'direction': 'agents'}
    module.academic_publish_stage(['a', 'b'], [], [], recommendations=[recommendation])
    expected = ['candidate_set', 'screened_candidates']
    if mode == 'balanced':
        expected.append('screened_list_ready')
    assert [event['slot'] for event in events] == expected
    final = events[1]['value']['data']
    assert final['list_mode'] == mode
    assert final['recommendations'] == [recommendation]
    ctx.params['remote_inputs'].update(candidate_set=draft, screened_candidates=final)
    assert module._analysis_candidates() == final


def test_deep_analysis_cannot_replace_the_frozen_list(publisher):
    module, ctx, _, _ = publisher
    ctx.params['remote_inputs']['candidate_set']['list_mode'] = 'deep'
    with pytest.raises(ToolExecutionError, match='frozen candidate paper IDs'):
        module._lineage_input([{'paper_id': 'new', 'key_contribution': 'A substitute'}])
    result, _, _, selection = module._lineage_input([{'paper_id': 'a', 'key_contribution': 'Based on the full text'}])
    assert [row['paper']['paper_id'] for row in result['papers']] == ['a']
    assert selection == {'before_count': 1, 'changes': []}


def test_claim_schema_requires_the_published_analysis_key(publisher):
    module, _, _, _ = publisher
    from lazymind.chat.lazyllm_tool_docs import ensure_lazyllm_tool_docs
    ensure_lazyllm_tool_docs([module.academic_check_relations])
    tool = ToolManager([module.academic_check_relations]).all_tools[0]
    args = {'analyses': [{'paper_id': 'a', 'key_contribution': 'Improves by 7%'}],
            'relations': [], 'investigation': [],
            'claims': [{'paper_id': 'a', 'field': 'key_contribution',
                        'positions': [0]}]}
    assert tool._validate_input(args)['claims'][0]['field'] == 'key_contribution'
    args['claims'][0]['field'] = 'method'
    with pytest.raises(Exception, match='seed_relation|key_contribution'):
        tool._validate_input(args)


def _verification_output(request, verdict='supported', **overrides):
    checks = []
    scope_status = overrides.pop('scope_status', 'supported')
    scope_reason = overrides.pop('scope_reason', 'conditions retained')
    for row in request.input.data['evidence']:
        key = row['available_sources'][0]
        source = request.input.data['source_pages_by_key'][key]['text'][:80]
        witness = {'status': scope_status, 'source_key': key, 'source_excerpt': source,
                   'statement_excerpt': row['statement'], 'reason': scope_reason}
        scope = [{'kind': 'subject', **witness}]
        if row['baseline_required']:
            scope.append({'kind': 'baseline', **witness})
        else:
            scope.append({'kind': 'baseline', 'status': 'not_applicable', 'source_key': None,
                          'source_excerpt': None, 'statement_excerpt': None, 'reason': 'No relative baseline.'})
        if row['field'] == 'relation':
            scope.append({'kind': 'identity', **witness})
        checks.append({'index': row['index'], 'parts': [
            {'part_index': part['part_index'], 'status': verdict, 'reason': 'source-grounded verdict',
             'sources': row['available_sources'][:1]} for part in row['parts']],
            'scope_checks': scope, **overrides})
    return {'checks': checks}


def test_verification_batches_large_library_blocks_without_losing_context(publisher, monkeypatch):
    module, _, _, _ = publisher
    from lazymind.chat.service import llm_task
    monkeypatch.setattr(module, '_expansion_id', lambda: 'exp')
    monkeypatch.setattr(module, '_call', lambda *args: {
        'status': 'ready', 'positions': [0], 'pages': [{'page': 0, 'text': 'complete paper ' * 6000}],
        'content_hash': 'hash',
    })
    requests = []

    def verify(request, usage, validate_output, **kwargs):
        requests.append(request)
        paper_id = request.input.data['evidence'][0]['paper_id']
        return validate_output(_verification_output(
            request, 'supported' if paper_id == 'a' else 'unsupported')), None, None

    monkeypatch.setattr(llm_task, '_run_task', verify)
    claims = [{'paper_id': paper, 'field': 'key_contribution', 'statement': 'A critical claim.', 'positions': [0]}
              for paper in ['a', 'b']]
    checked = module._verify_claims(claims, ['a', 'b'])
    assert [(row['paper_id'], row['status']) for row in checked] == [('a', 'supported'), ('b', 'unsupported')]
    assert len(requests) == 2
    for request in requests:
        page = next(iter(request.input.data['source_pages_by_key'].values()))
        assert page['text'] == 'complete paper ' * 6000


def test_ambiguous_verification_checks_stay_uncertain_and_record_diagnostics(publisher, monkeypatch):
    import json
    import lazyllm
    from lazymind.chat.service import llm_task

    module, _, _, _ = publisher
    monkeypatch.setattr(module, '_expansion_id', lambda: 'exp')
    monkeypatch.setattr(module, '_call', lambda *args: {
        'status': 'ready', 'positions': [0], 'pages': [{'page': 1, 'text': 'source evidence'}],
        'content_hash': 'hash',
    })
    diagnostics = []
    monkeypatch.setattr(lazyllm.LOG, 'warning', diagnostics.append)
    attempts = []

    def invalid(request, prompt, **kwargs):
        attempts.append(request)
        output = _verification_output(request)
        output['checks'] = [output['checks'][0], output['checks'][0], {'index': 7}]
        return output

    monkeypatch.setattr(llm_task, '_call_model', invalid)
    claims = [{'paper_id': paper, 'field': 'key_contribution', 'statement': 'A factual claim.', 'positions': [0]}
              for paper in ['a', 'b']]
    checked = module._verify_claims(claims, ['a', 'b'])
    assert [item['status'] for item in checked] == ['uncertain', 'uncertain']
    logged = [json.loads(item.split(': ', 1)[1]) for item in diagnostics
              if item.startswith('[AcademicVerification]')]
    assert {row['paper_ids'][0] for row in logged} == {'a', 'b'}
    assert all(row['expansion_id'] == 'exp' and row['error'] == 'invalid_model_output' for row in logged)
    assert len(attempts) == 4
    assert all(item['verification_error'] == 'invalid_model_output' for item in checked)


def test_relationship_verification_keeps_seed_and_own_sources_per_field(publisher, monkeypatch):
    module, ctx, _, _ = publisher
    ctx.params['remote_inputs']['candidate_set']['papers'][0]['is_seed'] = True
    from lazymind.chat.service import llm_task
    monkeypatch.setattr(module, '_expansion_id', lambda: 'exp')
    monkeypatch.setattr(module, '_call', lambda method, path, payload: {
        'status': 'ready', 'positions': [0], 'pages': [{'page': 1, 'text': payload['paper_id']}],
        'content_hash': 'hash-' + payload['paper_id'],
    })
    requests = []

    def verify(request, usage, validate_output, **kwargs):
        requests.append(request)
        assert len(request.input.data['evidence']) == 1
        identity = request.input.data['evidence'][0]['paper_id']
        assert {page['paper_id'] for page in request.input.data['source_pages_by_key'].values()} == {'a', identity}
        return validate_output(_verification_output(request)), None, None

    monkeypatch.setattr(llm_task, '_run_task', verify)
    claims = [{'paper_id': paper, 'field': 'seed_relation',
               'statement': 'Relationship across both papers.', 'positions': [0]}
              for paper in ['a', 'b']]
    assert all(row['status'] == 'supported' for row in module._verify_claims(claims, ['a', 'b']))
    assert len(requests) == 2


def test_verification_covers_all_analysis_fields_in_larger_paper_sets(publisher, monkeypatch):
    module, _, _, _ = publisher
    from lazymind.chat.service import llm_task
    monkeypatch.setattr(module, '_call', lambda method, path, payload: {
        'status': 'ready', 'positions': [0], 'pages': [{'page': 1, 'text': payload['paper_id']}],
        'content_hash': 'hash-' + payload['paper_id'],
    })

    def verify(request, usage, validate_output, **kwargs):
        return validate_output(_verification_output(request)), None, None

    monkeypatch.setattr(llm_task, '_run_task', verify)
    papers = [f'paper-{index}' for index in range(24)]
    claims = [{'paper_id': paper, 'field': field, 'statement': 'A source-grounded conclusion.', 'positions': [0]}
              for paper in papers for field in ['seed_relation', 'direction_relation', 'key_contribution']]
    checked = module._verify_claims(claims, papers)
    assert len(checked) == 72
    assert all(row['status'] == 'supported' for row in checked)
    with pytest.raises(ToolExecutionError, match='duplicate paper and analysis field'):
        module._verify_claims([claims[0], claims[0]], papers)


@pytest.mark.parametrize('part_status,scope_status,expected', [
    ('uncertain', 'supported', 'uncertain'),
    ('unsupported', 'uncertain', 'unsupported'),
    ('supported', 'uncertain', 'uncertain'),
    ('supported', 'supported', 'supported'),
])
def test_whole_field_requires_every_subclaim_and_material_condition(
        publisher, monkeypatch, part_status, scope_status, expected):
    from lazymind.chat.service import llm_task
    module, _, _, _ = publisher
    monkeypatch.setattr(module, '_call', lambda *args: {
        'status': 'ready', 'positions': [0], 'pages': [{'page': 1, 'text': 'complete evidence'}],
        'content_hash': 'hash', 'source_version': 'v1',
    })

    def verify(request, validate_output, **kwargs):
        output = _verification_output(request, scope_status=scope_status)
        if scope_status == 'uncertain':
            output['checks'][0]['scope_checks'][0].update(
                statement_excerpt=None, reason='The source parameter belongs to a different module.')
        output['checks'][0]['parts'][1]['status'] = part_status
        return validate_output(output), None, None

    monkeypatch.setattr(llm_task, '_run_task', verify)
    result = module._verify_claims([{'paper_id': 'a', 'field': 'key_contribution',
                                    'statement': '已证明的机制，不充分的因果断言。', 'positions': [0]}], ['a'])
    assert result[0]['status'] == expected
    assert len(result[0]['parts']) == 2
    assert result[0]['parts'][1]['text'] == '不充分的因果断言。'
    assert result[0]['content_hash'] == 'hash' and result[0]['source_version'] == 'v1'


@pytest.mark.parametrize('missing_kind', ['subject', 'baseline', 'identity'])
def test_scope_witnesses_control_the_checked_published_verdict(publisher, missing_kind):
    module, _, _, _ = publisher
    item = {'field': 'relation', 'statement': 'Throughput improves 1.85×.',
            'conditions': 'Both systems use the same acceleration and hardware.',
            'source_paper_id': 'a', 'target_paper_id': 'b',
            'parts': module._claim_parts('Throughput improves 1.85×.'), 'available_sources': ['a:0']}
    request = module._verification_request([item], {'a:0': {'text': 'Compared with the full duplication baseline.'}},
                                           {'papers': [], 'user_instruction': '', 'selected_directions': []}, 'model')
    output = _verification_output(request)
    check = output['checks'][0]
    witness = next(row for row in check['scope_checks'] if row['kind'] == missing_kind)
    witness.update(statement_excerpt=None, reason='The full duplication baseline is absent from the claim.')
    if missing_kind == 'identity':
        witness.update(source_key=None, source_excerpt=None, reason='The cited work identity is not established.')
    # A positive free-form label cannot override the missing structured witness.
    check.update(scope_status='supported', scope_reason='All conditions are retained.')
    module._validate_verification(output, request.input.data['evidence'], request.input.data['source_pages_by_key'])
    result = module._verification_verdict(item, check)
    assert result['status'] == result['scope_status'] == 'uncertain'
    assert witness['reason'] in result['scope_reason']
    assert 'scope_checks' not in result and 'baseline_required' not in result

    # The material qualifier can be retained in conditions rather than repeated in the statement.
    output = _verification_output(request)
    for witness in output['checks'][0]['scope_checks']:
        witness['statement_excerpt'] = item['conditions']
    module._validate_verification(output, request.input.data['evidence'], request.input.data['source_pages_by_key'])
    assert module._verification_verdict(item, output['checks'][0])['status'] == 'supported'


@pytest.mark.parametrize('invalid', ['quote', 'source', 'statement', 'missing_checks', 'numeric_baseline'])
def test_invalid_scope_witnesses_are_rejected_at_the_verifier_boundary(publisher, invalid):
    module, _, _, _ = publisher
    item = {'field': 'key_contribution', 'statement': 'Throughput improves 50%.',
            'parts': module._claim_parts('Throughput improves 50%.'), 'available_sources': ['a:0']}
    request = module._verification_request([item], {'a:0': {'text': 'Measured against the original system.'}},
                                           {'papers': [], 'user_instruction': '', 'selected_directions': []}, 'model')
    output = _verification_output(request)
    check = output['checks'][0]
    witness = check['scope_checks'][0]
    if invalid == 'quote':
        witness['source_excerpt'] = 'A fabricated measurement.'
    elif invalid == 'source':
        witness['source_key'] = 'not-an-allowed-source'
    elif invalid == 'statement':
        witness['statement_excerpt'] = 'A condition never stated.'
    elif invalid == 'missing_checks':
        del check['scope_checks']
    else:
        check['scope_checks'][1].update(status='not_applicable', source_key=None,
                                        source_excerpt=None, statement_excerpt=None)
    with pytest.raises(ValueError):
        module._validate_verification(output, request.input.data['evidence'],
                                      request.input.data['source_pages_by_key'])


def test_parallel_verification_preserves_session_and_seed_evidence(publisher, monkeypatch):
    import threading
    import lazyllm
    from lazymind.chat.service import llm_task
    module, ctx, events, _ = publisher
    ctx.params['remote_inputs']['candidate_set']['papers'][0]['is_seed'] = True
    monkeypatch.setattr(module, '_call', lambda method, path, payload: {
        'status': 'ready', 'positions': [0], 'pages': [{'page': 1, 'text': payload['paper_id']}],
        'content_hash': 'hash-' + payload['paper_id'],
    })
    barrier = threading.Barrier(3)
    sid = lazyllm.globals._sid
    queued = []

    def verify(request, validate_output, **kwargs):
        assert lazyllm.globals._sid == sid
        assert len(request.input.data['evidence']) == 1
        queued.append(request.input.data['evidence'][0])
        seed_key = next(key for key, page in request.input.data['source_pages_by_key'].items()
                        if page['paper_id'] == 'a')
        assert all(seed_key in row['available_sources'] for row in request.input.data['evidence'])
        barrier.wait(timeout=3)
        return validate_output(_verification_output(request)), None, None

    monkeypatch.setattr(llm_task, '_run_task', verify)
    claims = [{'paper_id': paper, 'field': field, 'statement': 'A grounded finding.', 'positions': [0]}
              for paper in ['a', 'b', 'c'] for field in module._ANALYSIS_FIELDS]
    claims.extend({'paper_id': 'b', 'field': 'relation', 'claim_id': 'edge-' + str(index),
                   'source_paper_id': 'a', 'target_paper_id': 'b', 'relation_type': 'mechanism_comparison',
                   'statement': 'A grounded comparison.', 'positions': [0]} for index in range(6))
    relations = [{**row, 'id': row['claim_id']} for row in claims if row['field'] == 'relation']
    investigation = [{'direction': topic, 'relation_ids': ['edge-' + str(index), 'edge-' + str(index + 1)]}
                     for index, topic in zip(range(0, 6, 2), ['cache', 'precision', 'prefill'])]
    order = module._verification_relation_order(relations, investigation, ['cache', 'precision', 'prefill'], {'a'})
    checkpointed = []
    checked = module._verify_claims(claims, ['a', 'b', 'c'], relation_order=order,
                                    checkpoint=lambda rows, metric: checkpointed.extend(rows))
    assert all(row['field'] == 'relation' for row in queued[:3])
    assert {row['claim_id'] for row in queued[:3]} == {'edge-0', 'edge-2', 'edge-4'}
    assert [(row['paper_id'], row['field'], row.get('claim_id')) for row in checked] == [
        (row['paper_id'], row['field'], row.get('claim_id')) for row in claims]
    assert [(index, row.get('claim_id')) for index, row in sorted(checkpointed)] == [
        (index, row.get('claim_id')) for index, row in enumerate(claims)]
    assert len(checked) == 15 and all(row['status'] == 'supported' for row in checked)
    assert events[-1]['current_phase'] == '独立核验 15/15 项，技术失败 0 项'


@pytest.mark.parametrize('failure', [
    'invalid_model_output', 'task_timeout_once', 'task_timeout_twice', 'task_timeout_without_budget',
    'nonretryable_timeout', 'authentication_failed', 'rate_limited', 'uncertain', 'cancelled',
])
def test_only_failed_batches_retry_and_never_publish_as_supported(publisher, monkeypatch, failure):
    import asyncio
    import time
    from collections import Counter
    from lazymind.chat.service import llm_task
    module, ctx, _, _ = publisher
    monkeypatch.setattr(module, '_call', lambda *args: {
        'status': 'ready', 'positions': [0], 'pages': [{'page': 1, 'text': 'evidence'}],
        'content_hash': 'hash',
    })
    scientific_deadline = time.monotonic() + (179 if failure == 'task_timeout_without_budget' else 900)
    ctx._academic_verification_deadline = scientific_deadline
    calls, field_calls, requests, prompts, controls = Counter(), Counter(), [], [], []

    def verify(request, prompt, **kwargs):
        paper = request.input.data['evidence'][0]['paper_id']
        calls[paper] += 1
        field = request.input.data['evidence'][0]['field']
        field_calls[(paper, field)] += 1
        if paper != 'b':
            return _verification_output(request)
        requests.append(copy.deepcopy(request.input.data))
        prompts.append(prompt)
        controls.append(kwargs['call_control'].deadline)
        if failure == 'invalid_model_output':
            return {'checks': []}
        if failure == 'cancelled':
            raise asyncio.CancelledError()
        if failure == 'uncertain':
            return _verification_output(request, verdict='uncertain')
        if failure == 'task_timeout_once' and field_calls[(paper, field)] > 1:
            return _verification_output(request)
        code = 'task_timeout' if failure.startswith('task_timeout') or failure == 'nonretryable_timeout' else failure
        raise llm_task.LLMTaskCallError(code, retryable=failure != 'nonretryable_timeout')

    monkeypatch.setattr(llm_task, '_call_model', verify)
    claims = [{'paper_id': paper, 'field': field, 'statement': 'A grounded finding.', 'positions': [0]}
              for paper in ['a', 'b'] for field in module._ANALYSIS_FIELDS]
    if failure == 'cancelled':
        with pytest.raises(asyncio.CancelledError):
            module._verify_claims(claims, ['a', 'b'])
        assert 1 <= calls['b'] <= 3
        return
    checked = module._verify_claims(claims, ['a', 'b'])
    repeated = failure in {'invalid_model_output', 'task_timeout_once', 'task_timeout_twice'}
    assert calls == {'a': 3, 'b': 6 if repeated else 3}
    assert ctx._academic_last_verification_metrics['model_calls'] == sum(calls.values())
    for field in module._ANALYSIS_FIELDS:
        attempts = [value for value in requests if value['evidence'][0]['field'] == field]
        assert all(value == attempts[0] for value in attempts)
    assert ctx._academic_verification_deadline == scientific_deadline
    assert all(value <= scientific_deadline for value in controls)
    if failure.startswith('task_timeout'):
        for field in module._ANALYSIS_FIELDS:
            attempts = [prompt for request, prompt in zip(requests, prompts)
                        if request['evidence'][0]['field'] == field]
            assert all(value == attempts[0] for value in attempts)
    assert [row['status'] for row in checked[:3]] == ['supported'] * 3
    if failure == 'task_timeout_once':
        assert [row['status'] for row in checked[3:]] == ['supported'] * 3
        assert all(not row.get('verification_error') for row in checked[3:])
    else:
        assert [row['status'] for row in checked[3:]] == ['uncertain'] * 3
        if failure == 'uncertain':
            assert all(not row.get('verification_error') for row in checked[3:])
        else:
            error = ('task_timeout' if failure.startswith('task_timeout')
                     or failure == 'nonretryable_timeout' else failure)
            assert all(row['verification_error'] == error for row in checked[3:])


def test_invalid_paper_positions_are_corrected_before_model_verification(publisher, monkeypatch):
    import httpx
    from lazymind.chat.service import llm_task
    module, _, events, _ = publisher
    calls = []

    def read(method, path, payload):
        if payload['positions'] != [0]:
            response = httpx.Response(400, json={'code': 'INVALID_CURSOR'},
                                      request=httpx.Request(method, 'http://core' + path))
            response.raise_for_status()
        return {'status': 'ready', 'positions': [0], 'content_hash': 'hash',
                'pages': [{'page': 1, 'text': 'actual source'}]}

    def verify(request, prompt, **kwargs):
        calls.append(request)
        return _verification_output(request)

    monkeypatch.setattr(module, '_call', read)
    monkeypatch.setattr(llm_task, '_call_model', verify)
    claim = {'paper_id': 'a', 'field': 'key_contribution', 'statement': 'Improves accuracy by 25%.'}
    with pytest.raises(ToolExecutionError, match='do not mix seed-paper positions'):
        module._verify_claims([{**claim, 'positions': [0, 18]}], {'a'})
    assert not calls and not events
    verified = module._verify_claims([{**claim, 'positions': [0]}], {'a'})
    assert len(calls) == 1
    assert verified[0]['status'] == 'supported'


def test_fields_share_read_pages_of_the_same_paper_with_traceable_positions(publisher, monkeypatch):
    from lazymind.chat.service import llm_task
    module, _, _, _ = publisher
    monkeypatch.setattr(module, '_call', lambda method, path, payload: {
        'status': 'ready', 'positions': payload['positions'], 'content_hash': 'same-version',
        'pages': [{'page': position + 1, 'text': 'source ' + str(position)} for position in payload['positions']],
    })

    def verify(request, prompt, **kwargs):
        pages = request.input.data['source_pages_by_key']
        supporting_key = next(key for key, page in pages.items() if page['position'] == 1)
        assert all(supporting_key in row['available_sources'] for row in request.input.data['evidence'])
        output = _verification_output(request)
        for row in output['checks']:
            row['parts'][0]['sources'] = [supporting_key]
        return output

    monkeypatch.setattr(llm_task, '_call_model', verify)
    checked = module._verify_claims([
        {'paper_id': 'a', 'field': 'key_contribution', 'statement': 'A grounded result.', 'positions': [0]},
        {'paper_id': 'a', 'field': 'seed_relation', 'statement': 'A grounded comparison.', 'positions': [1]},
    ], ['a'])
    assert all(row['status'] == 'supported' and row['positions'] == [0, 1] for row in checked)
    assert all(row['parts'][0]['sources'] == ['a:1'] for row in checked)
    assert all(row['content_hash'] == 'same-version' for row in checked)


def test_large_history_keeps_all_current_papers_visible_and_preserves_full_snapshot(publisher, monkeypatch):
    import json
    import lazyllm
    from lazymind.chat.engine.tools.file_resources.tools import build_resource_read_tools
    from lazymind.common.token_estimation import estimate_tokens
    module, ctx, _, _ = publisher
    papers = [{'paper': {'paper_id': f'paper-{index}', 'title': f'Paper {index}',
                         'abstract': 'Complete abstract.', 'authors': ['Author']},
               'is_seed': index == 0} for index in range(33)]
    full = {'record': {'id': 'exp', 'result': {'papers': papers, 'user_instruction': 'same scope'},
                       'reading': {'paper-0': {'positions': [0, 3], 'content_hash': 'hash-v1'}},
                       'preparation': {'directions': [{'name': 'route', 'paper_ids': ['paper-0'], 'papers': papers}]},
                       'report': {'source_queries': 'history marker ' * 20000, 'limitations': ['partial reading']},
                       'progress': {'old_result': papers}},
            'rounds': [{'id': 'round-1', 'version': 1, 'status': 'completed', 'result': papers}],
            'contents': [{'paper_id': 'paper-0', 'status': 'ready', 'content_hash': 'hash-v1'}],
            'source_queries': [{
                'id': 'actual-query', 'query': 'Original method', 'mode': 'citations',
                'paper_id': 'paper-0', 'paper_ids': ['paper-1'], 'created_at': '2026-11-02',
                'sources': [{'source': 'arxiv', 'status': 'rate_limited', 'retry_at': '2026-11-03',
                             'count': 1, 'has_more': True, 'next_cursor': 'exact-source-cursor', 'elapsed_ms': 123}]}]}
    monkeypatch.setattr(module, '_read_expansion_context', lambda _: copy.deepcopy(full))
    visible = module.academic_expansion_context('exp')
    assert [row['paper']['paper_id'] for row in visible['record']['result']['papers']] == [
        f'paper-{index}' for index in range(33)]
    assert visible['record']['reading'] == full['record']['reading']
    query = visible['source_queries'][0]
    assert {key: value for key, value in query.items() if key != 'sources'} == {
        key: value for key, value in full['source_queries'][0].items() if key != 'sources'}
    assert query['sources'] == [{'source': 'arxiv', 'status': 'rate_limited', 'retry_at': '2026-11-03',
                                 'count': 1, 'has_more': True}]
    assert visible['record']['report']['limitations'] == ['partial reading']
    assert estimate_tokens(json.dumps(visible, ensure_ascii=False)) < 28000
    path = Path(ctx.workspace_path) / visible['full_context_resource'].removeprefix('workspace://')
    assert json.loads(path.read_text()) == {**full, 'bound_inputs': visible['bound_inputs']}
    assert visible['full_context_resource'].startswith('workspace://tool_spills/')
    monkeypatch.setattr(lazyllm, 'globals', {'agentic_config': {
        'agent_type': 'workflow_step', 'workflow_workspace_path': ctx.workspace_path}})
    readers = {tool.__name__: tool for tool in build_resource_read_tools()}
    read = readers['read_file_resource'](visible['full_context_resource'])
    assert read['target'] == visible['full_context_resource']
    assert read['kind'] == 'workspace'
    assert 'paper-0' in read['text']
    assert read['next_offset'] is not None
    match = readers['search_file_resource'](visible['full_context_resource'], 'same scope')['matches'][0]
    page = readers['read_file_resource'](visible['full_context_resource'], offset=match['line'], limit=1)
    assert 'same scope' in page['text']


def test_single_field_requests_preserve_complete_shared_seed_pages(publisher, monkeypatch):
    from lazymind.chat.service import llm_task
    module, ctx, _, _ = publisher
    ctx.params['remote_inputs']['candidate_set']['papers'][0]['is_seed'] = True
    seed_text = 'complete seed mechanism ' * 4000
    monkeypatch.setattr(module, '_call', lambda method, path, payload: {
        'status': 'ready', 'positions': [0], 'content_hash': 'hash-' + payload['paper_id'],
        'pages': [{'page': 1, 'text': seed_text if payload['paper_id'] == 'a' else 'complete method'}],
    })
    calls = []

    def verify(request, prompt, **kwargs):
        calls.append(request)
        return _verification_output(request)

    monkeypatch.setattr(llm_task, '_call_model', verify)
    claims = [{'paper_id': paper, 'field': field, 'statement': 'A grounded result.', 'positions': [0]}
              for paper in ['a', 'b'] for field in module._ANALYSIS_FIELDS]
    checked = module._verify_claims(claims, ['a', 'b'])
    assert len(calls) == 6 and all(row['status'] == 'supported' for row in checked)
    assert all(len(request.input.data['evidence']) == 1 for request in calls)
    assert all(any(page['text'] == seed_text for page in request.input.data['source_pages_by_key'].values())
               for request in calls)


@pytest.mark.parametrize('status', [400, 422])
def test_core_argument_errors_preserve_actionable_code_and_message(publisher, monkeypatch, status):
    import httpx
    from types import SimpleNamespace
    module, _, _, _ = publisher
    message = 'paper must be a saved seed or an externally discovered candidate of this expansion'
    response = httpx.Response(status, json={'code': 'INVALID_SELECTION', 'message': message},
                              request=httpx.Request('POST', 'http://core/academic/paper-contents:read'))
    transport = SimpleNamespace(request=lambda *args, **kwargs: response)
    monkeypatch.setattr(module, '_client', lambda: SimpleNamespace(
        transport=transport, base_url='http://core', _headers=lambda: {}))
    with pytest.raises(ToolExecutionError, match='INVALID_SELECTION: ' + message):
        module.academic_read_paper('exp', paper_id='not-registered')
    with pytest.raises(ToolExecutionError, match='INVALID_SELECTION: ' + message):
        module._call('POST', '/expansions/exp/papers:resolve', {'paper_ids': ['not-registered']})


def test_core_service_failure_keeps_original_http_error(publisher, monkeypatch):
    import httpx
    from types import SimpleNamespace
    module, _, _, _ = publisher
    response = httpx.Response(503, json={'code': 'UNAVAILABLE', 'message': 'service unavailable'},
                              request=httpx.Request('POST', 'http://core/academic/paper-contents:read'))
    monkeypatch.setattr(module, '_client', lambda: SimpleNamespace(
        transport=SimpleNamespace(request=lambda *args, **kwargs: response),
        base_url='http://core', _headers=lambda: {}))
    with pytest.raises(httpx.HTTPStatusError) as error:
        module.academic_read_paper('exp', paper_id='a')
    assert error.value.response is response
