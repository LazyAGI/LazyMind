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
        package, ['academic_submit_relations', 'academic_publish_expansion_result', 'academic_validate_stage'],
        'paper_expansion', 'test-revision',
    )
    namespace = functions['academic_submit_relations'].__globals__

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
    monkeypatch.setattr(module, 'academic_expansion_context',
                        lambda _: {'record': {'result': copy.deepcopy(body), 'reading': {}}})
    monkeypatch.setattr(module, '_model_metrics', lambda: {})
    return module, ctx, events, resolved


def test_typed_submission_uses_bound_authoritative_metadata_and_publishes_once(publisher):
    module, ctx, events, _ = publisher
    from lazymind.chat.lazyllm_tool_docs import ensure_lazyllm_tool_docs
    ensure_lazyllm_tool_docs([module.academic_submit_relations, module.academic_publish_expansion_result])
    tool = ToolManager([module.academic_submit_relations]).all_tools[0]
    args = tool._validate_input({'analyses': [{'paper_id': 'a', 'key_contribution': 'Based on the full text.'}]})
    module.academic_submit_relations(**args)
    relation = events[0]['value']['data']
    result = relation['input']
    assert result['papers'][0] == {
        'paper': {'paper_id': 'a', 'title': 'server title', 'source_version': 'v2'},
        'is_seed': True, 'is_important': True, 'key_contribution': 'Based on the full text.',
    }
    assert module.academic_submit_relations(**args)['already_published'] is True
    assert len(events) == 1
    with pytest.raises(ToolExecutionError, match='already published'):
        module.academic_submit_relations([{'paper_id': 'a', 'key_contribution': 'different'}])
    assert len(events) == 1
    ctx.params['step_id'] = 'compose_result'
    ctx.params['remote_inputs']['relation_analysis'] = relation
    module.academic_publish_expansion_result('Research narrative', ['a'], [])
    assert events[-1]['slot'] == 'expansion_report'
    assert set(events[-2]['value']['data']) == module._INPUT_KEYS


@pytest.mark.parametrize('analyses,reason', [
    ([], 'cover exactly'), ([{'test': True}], 'paper_id'),
    ([{'paper_id': 'a'}], 'requires at least one'),
    ([{'paper_id': 'unknown'}], 'cover exactly'),
    ([{'paper_id': 'a'}, {'paper_id': 'a'}], 'duplicate'),
    ([{'paper_id': 'a', 'key_contribution': {}}], 'plain string'),
])
def test_invalid_publication_keeps_previous_output(publisher, analyses, reason):
    module, _, events, _ = publisher
    with pytest.raises(ToolExecutionError, match=reason):
        module.academic_submit_relations(analyses)
    assert events == []


def test_fulltext_failure_and_genuine_empty_results_are_publishable(publisher):
    module, ctx, events, resolved = publisher
    resolved['contents'][0]['status'] = 'failed'
    module.academic_submit_relations([{'paper_id': 'a'}])
    assert 'key_contribution' not in events[0]['value']['data']['input']['papers'][0]
    ctx.task_id = 'empty-task'
    ctx._local_artifacts.clear()
    ctx.params['remote_inputs']['candidate_set']['papers'] = []
    resolved['papers'], resolved['contents'] = [], []
    module.academic_submit_relations([])
    assert events[-1]['value']['data']['input']['papers'] == []


def test_missing_bound_input_does_not_fall_back_to_other_artifacts(publisher):
    module, ctx, events, _ = publisher
    del ctx.params['remote_inputs']['candidate_set']
    with pytest.raises(RuntimeError, match='bound Workflow input is missing'):
        module.academic_submit_relations([])
    assert events == []


def test_direction_entry_accepts_no_seeds_and_seed_entry_cannot_drop_them(publisher, monkeypatch):
    module, ctx, _, resolved = publisher
    ctx.params['step_id'] = 'prepare_plan'
    body = ctx.params['remote_inputs']['candidate_set']
    monkeypatch.setattr(module, 'academic_expansion_context', lambda _: {'record': {'result': {'papers': []}}})
    assert module.academic_validate_stage(body)['status'] == 'valid'
    monkeypatch.setattr(module, 'academic_expansion_context', lambda _: {
        'record': {'result': {'papers': [{'paper': {'paper_id': 'seed'}, 'is_seed': True}]}}})
    with pytest.raises(ToolExecutionError, match='all selected seed'):
        module.academic_validate_stage(body)
    for invalid in [{'test': True}, {}]:
        with pytest.raises(ToolExecutionError):
            module.academic_validate_stage(invalid)


def test_repeated_critical_submission_does_not_repeat_verification(publisher, monkeypatch):
    module, _, _, _ = publisher
    calls = []

    def verify(claims, allowed):
        calls.append(claims)
        return [dict(claims[0], status='supported')]

    monkeypatch.setattr(module, '_verify_claims', verify)
    claims = [{'paper_id': 'a', 'field': 'key_contribution', 'statement': 'Improves by 7%', 'positions': [2]}]
    analyses = [{'paper_id': 'a', 'key_contribution': 'Improves by 7%'}]
    module.academic_submit_relations(analyses, claims)
    assert module.academic_submit_relations(analyses, claims)['already_published']
    assert len(calls) == 1


def test_verification_checks_the_published_analysis_not_an_easier_claim(publisher, monkeypatch):
    module, _, events, _ = publisher
    checked = []

    def verify(claims, allowed):
        checked.extend(claims)
        return [dict(claims[0], status='unsupported')]

    monkeypatch.setattr(module, '_verify_claims', verify)
    module.academic_submit_relations(
        [{'paper_id': 'a', 'key_contribution': 'Improves accuracy by 25%.'}],
        [{'paper_id': 'a', 'field': 'key_contribution', 'statement': 'This paper studies retrieval.', 'positions': [2]}],
    )
    assert checked[0]['statement'] == 'Improves accuracy by 25%.'
    assert '25%' not in events[0]['value']['data']['input']['papers'][0]['key_contribution']


def test_claim_schema_requires_the_published_analysis_key(publisher):
    module, _, _, _ = publisher
    from lazymind.chat.lazyllm_tool_docs import ensure_lazyllm_tool_docs
    ensure_lazyllm_tool_docs([module.academic_submit_relations])
    tool = ToolManager([module.academic_submit_relations]).all_tools[0]
    args = {'analyses': [{'paper_id': 'a', 'key_contribution': 'Improves by 7%'}],
            'claims': [{'paper_id': 'a', 'field': 'key_contribution',
                        'positions': [0]}]}
    assert tool._validate_input(args)['claims'][0]['field'] == 'key_contribution'
    args['claims'][0]['field'] = 'method'
    with pytest.raises(Exception, match='seed_relation|key_contribution'):
        tool._validate_input(args)


def test_verification_batches_large_library_blocks_without_losing_context(publisher, monkeypatch):
    module, _, _, _ = publisher
    from lazymind.chat.service import llm_task
    monkeypatch.setattr(module, '_expansion_id', lambda: 'exp')
    monkeypatch.setattr(module, '_call', lambda *args: {
        'status': 'ready', 'positions': [0], 'pages': [{'page': 0, 'text': 'complete paper ' * 6000}],
        'content_hash': 'hash',
    })
    requests = []

    def verify(request, usage):
        requests.append(request)
        return {'checks': [{'index': row['index'], 'status': 'supported', 'reason': 'source supports it'}
                           for row in request.input.data['evidence']]}, None, None

    monkeypatch.setattr(llm_task, '_run_task', verify)
    claims = [{'paper_id': paper, 'field': 'key_contribution', 'statement': 'A critical claim.', 'positions': [0]}
              for paper in ['a', 'b']]
    checked = module._verify_claims(claims, ['a', 'b'])
    assert [row['status'] for row in checked] == ['supported', 'supported']
    assert len(requests) == 2
    assert all(next(iter(request.input.data['source_pages_by_paper_and_position'].values()))['text'] == 'complete paper ' * 6000
               for request in requests)


def test_missing_or_budget_exhausted_evidence_publishes_an_uncertain_partial_result(publisher, monkeypatch):
    module, ctx, events, _ = publisher
    module.academic_submit_relations([{'paper_id': 'a', 'key_contribution': 'Improves accuracy by 25%.'}])
    relation = events[-1]['value']['data']
    assert relation['claims'][0]['status'] == 'uncertain'
    assert relation['claims'][0]['positions'] == []
    assert '25%' not in relation['input']['papers'][0]['key_contribution']

    ctx._local_artifacts.clear()
    monkeypatch.setattr(module, '_expansion_id', lambda: 'exp')
    monkeypatch.setattr(module, '_call', lambda *args: {'status': 'budget_exhausted'})
    module.academic_submit_relations(
        [{'paper_id': 'a', 'key_contribution': 'Improves accuracy by 25%.'}],
        [{'paper_id': 'a', 'field': 'key_contribution', 'positions': [0]}],
    )
    relation = events[-1]['value']['data']
    assert relation['claims'][0]['status'] == 'uncertain'
    assert relation['claims'][0]['positions'] == []
    assert '25%' not in relation['input']['papers'][0]['key_contribution']


def test_relationship_verification_shares_both_papers_when_evidence_fits(publisher, monkeypatch):
    module, _, _, _ = publisher
    from lazymind.chat.service import llm_task
    monkeypatch.setattr(module, '_expansion_id', lambda: 'exp')
    monkeypatch.setattr(module, '_call', lambda method, path, payload: {
        'status': 'ready', 'positions': [0], 'pages': [{'page': 1, 'text': payload['paper_id']}],
        'content_hash': 'hash-' + payload['paper_id'],
    })
    requests = []

    def verify(request, usage):
        requests.append(request)
        assert set(request.input.data['source_pages_by_paper_and_position']) == {'a:0', 'b:0'}
        return {'checks': [{'index': row['index'], 'status': 'supported', 'reason': 'both sources support it'}
                           for row in request.input.data['evidence']]}, None, None

    monkeypatch.setattr(llm_task, '_run_task', verify)
    claims = [{'paper_id': paper, 'field': 'seed_relation', 'statement': 'Relationship across both papers.', 'positions': [0]}
              for paper in ['a', 'b']]
    assert all(row['status'] == 'supported' for row in module._verify_claims(claims, ['a', 'b']))
    assert len(requests) == 1
