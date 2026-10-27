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

        def __getattr__(self, name):
            return namespace[name]

        def __setattr__(self, name, value):
            namespace[name] = value

    module = Module()
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
