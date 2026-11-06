"""Host-side constraints, source transfer and one-use retry regressions."""
import copy
from unittest.mock import MagicMock, patch

import pytest

from lazymind.document_tools.paper import contracts, workflow


@pytest.mark.parametrize('prompt,target,sections', [
    ('请写一篇约4000字中文论文，包括摘要、方法、结论。', 4000, ['abstract', 'methods', 'conclusion']),
    ('请撰写论文。', 8000, []),
])
def test_outline_binds_explicit_requirements_before_default(monkeypatch, prompt, target, sections):
    pool = workflow.pool_from_business(dict(topic='Paper', request=prompt, writing_materials='Original notes'))
    original = copy.deepcopy(pool)
    seen = []

    def run(step, value):
        seen.append(value)
        return dict(outline='# Paper\n\n## 摘要\n\n## 方法\n\n## 结论', is_demo=False)
    monkeypatch.setattr(workflow, 'run_algorithm', run)
    result = workflow.execute_step('write_outline', pool)
    assert seen[0]['constraints']['word_target'] == target
    assert seen[0]['constraints']['required_sections'] == sections
    assert result['pool']['constraints'] == seen[0]['constraints']
    assert pool == original


def test_review_receives_original_research_materials():
    notes = 'PT0005→PT0007; MiniMax: 3647→2831 字。'
    pool = workflow.pool_from_business(dict(
        topic='Paper', writing_materials=notes, manuscript='# Paper\n\n## 结果\n待核对。'))
    value = contracts.validate_input('peer_review', workflow.business_input('peer_review', pool))
    assert value['writing_materials'] == notes


@pytest.mark.parametrize('sections', [['unknown'], ['abstract', 'abstract'], 'abstract', [None]])
def test_invalid_required_sections_rejected(sections):
    with pytest.raises(ValueError):
        contracts.normalize_constraints({'required_sections': sections})


@pytest.mark.parametrize('conflict', [False, True])
@pytest.mark.parametrize('handoff', [False, True])
def test_explicit_retry_is_single_use_and_stops_model_loop(conflict, handoff):
    from lazymind.chat.workflow import workflow_manager as manager
    from lazymind.workflow_sdk import WorkflowClientError
    cfg = dict(user_authorized_workflow_retry=True)
    toolkit, client = MagicMock(), MagicMock()
    frontier = dict(state_version=7, retryable_steps=['data_analysis'])
    toolkit.get_ready_steps.return_value = client.get_ready_steps.return_value = frontier
    toolkit.get_workflow_state.return_value = client.get_state.return_value = {}
    response = MagicMock(result=dict(accepted=True, status='active'))
    responses = [WorkflowClientError('STATE_VERSION_CONFLICT', 'stale')] if conflict else []
    if handoff:
        client.advance.side_effect = responses + [response, response]
    else:
        waiting = dict(status='active', workflow_state={'display': {
            'status': 'waiting', 'approval_step_id': 'data_analysis'}})
        toolkit.advance_step.side_effect = responses + [waiting, waiting]
    with (patch.object(manager, '_agentic_config', return_value=cfg),
          patch.object(manager, '_client', return_value=client)):
        tool = (manager._handoff_tool('session', '重试') if handoff else
                next(t for t in manager._safe_session_tools(toolkit, 'session', '重试') if t.__name__ == 'advance_step'))
        result = tool('data_analysis' if handoff else ['data_analysis'])
        assert result['_agent_control']['stop'] is True
        assert not cfg.get('user_authorized_workflow_retry')
        result = tool('data_analysis' if handoff else ['data_analysis'])
        assert result['_agent_control']['stop'] is True
    calls = client.advance.call_args_list if handoff else toolkit.advance_step.call_args_list
    origins = [c.args[0].retry_origin if handoff else c.kwargs['retry_origin'] for c in calls]
    assert origins == (['user', 'user', 'automatic'] if conflict else ['user', 'automatic'])


@pytest.mark.parametrize('structured,language', [({}, 'en'), ({'paper_language': 'zh-CN'}, 'zh-CN')])
def test_explicit_english_request_resolved_before_defaults(structured, language):
    value = dict(topic='Paper', request='Write a paper in English, 2000 words, including abstract.',
                 constraints=structured, writing_materials='Source notes')
    assert workflow.pool_from_business(value)['constraints']['paper_language'] == language
    assert contracts.validate_input('write_document', value)['constraints']['paper_language'] == language


@pytest.mark.parametrize('status,outcome,reasons,stop', [
    ('failed', 'step_failed', {'t': 'MISSING_MATERIAL: analysis.results: no estimates'}, True),
    ('failed', 'step_failed', {'t': 'temporary model transport error'}, False),
    ('active', None, {'t': 'MISSING_MATERIAL: old failure'}, False),
    ('active', None, {}, False),
])
def test_missing_material_stops_only_current_failed_receipt(status, outcome, reasons, stop):
    from lazymind.chat.workflow.workflow_manager import _with_terminal_agent_control
    value = dict(status=status, outcome=outcome, failure_reasons=reasons,
                 retryable_steps=['data_analysis'], workflow_state={'attempt_history': {
                     'data_analysis': [{'attempt': 1, 'status': 'failed'}]}})
    original = copy.deepcopy(value)
    result = _with_terminal_agent_control(value)
    assert bool(result.get('_agent_control', {}).get('stop')) is stop
    assert result['retryable_steps'] == ['data_analysis']
    assert value == original


@pytest.mark.parametrize('workflow_id,stop', [('paper-writing', True), ('other-workflow', False)])
def test_paper_failure_stops_advance_tool_before_automatic_recovery(workflow_id, stop):
    from lazymind.chat.workflow import workflow_manager as manager
    toolkit = MagicMock()
    toolkit.get_ready_steps.return_value = dict(state_version=7, ready_steps=['experiment'])
    receipt = dict(status='failed', outcome='step_failed',
                   failure_reasons={'task': 'F3 材料超过当前模型预算，请缩小范围；未截断数据。'},
                   retryable_steps=['experiment'], rewindable_steps=['literature_review'],
                   workflow_state={'session_id': 'session', 'status': 'failed'})
    original = copy.deepcopy(receipt)
    toolkit.advance_step.return_value = receipt
    context = {}
    with patch.object(manager, '_agentic_config', return_value={}):
        tools = manager._safe_session_tools(toolkit, 'session', '继续', workflow_context=context)
        tool = next(t for t in tools if t.__name__ == 'advance_step')
        context['workflow_id'] = workflow_id
        result = tool(['experiment'])
    assert bool(result.get('_agent_control', {}).get('stop')) is stop
    toolkit.advance_step.assert_called_once()
    assert receipt == original
