"""Contract check against the real Runtime publisher and configured algorithms."""
import json
from pathlib import Path

import lazyllm
import pytest
import yaml

from lazymind.chat.engine.subagent.context import SubAgentContext, set_context

PACKAGE = Path(__file__).resolve().parents[4] / 'workflows/paper-writing'
from lazymind.document_tools.paper import api
from lazymind.document_tools.paper import workflow as tools


@pytest.mark.parametrize('reject_output', [False, True])
def test_completion_feedback_uses_brief_text_only_after_real_publication(tmp_path, reject_output):
    from lazyllm.tools.agent import ToolExecutionError
    from lazyllm.tools.agent.functionCall import _tool_result_stop_text

    previous = lazyllm.globals.get('subagent_ctx')
    events = []
    output_slot = 'unexpected_slot' if reject_output else 'finish_document'
    set_context(SubAgentContext(
        task_id='paper-completion', conversation_id='paper-test', agent_type='workflow_step',
        objective='Check completion feedback', params={
            'step_id': 'finish', 'output_slot_types': {output_slot: 'file'},
        },
        workspace_path=str(tmp_path), input_slots=[], output_slots=[output_slot],
        db=None, emit=events.append,
    ))
    try:
        if reject_output:
            with pytest.raises(ToolExecutionError, match='Allowed keys'):
                tools.run_paper_step()
            assert not any(event['type'] == 'artifact' for event in events)
            assert any(event['type'] == 'progress' for event in events)
            assert events[-1]['type'] == 'progress'
            assert all(step['status'] != 'running'
                       for step in events[-1]['public_display']['process_steps'])
            return
        result = tools.run_paper_step()
        assert result['status'] == 'completed'
        assert result['step'] == 'finish'
        assert result['saved']['status'] == 'ok'
        assert result['saved']['saved_count'] == 1
        assert 'next_step' not in result.get('control', {})
        for payload in (result, {'ok': True, 'value': result}):
            assert _tool_result_stop_text(payload) == '产物已保存，请在工作流中查看。'
        artifacts = [event for event in events if event['type'] == 'artifact']
        assert len(artifacts) == 1
        assert artifacts[0]['slot'] == 'finish_document'
        assert '已有产物保留' in Path(artifacts[0]['value']['path']).read_text()
    finally:
        if previous is None:
            lazyllm.globals.pop('subagent_ctx', None)
        else:
            lazyllm.globals['subagent_ctx'] = previous


@pytest.mark.usefixtures('paper_simulation')
@pytest.mark.parametrize('start,feedback,recovery', [
    ('literature_review', 'refine_topic', 'recovery_research'),
    ('peer_review', 'revise_document', 'recovery_review'),
])
def test_real_artifact_publisher_across_ten_feedback_rounds(tmp_path, start, feedback, recovery, monkeypatch):
    monkeypatch.setattr(api.ALGORITHMS['research_topic'], 'AutoModel', lambda **_kwargs: lambda _prompt:
                        '# 研究选题\n\n## 研究问题\n如何改善效果？\n\n'
                        '## 文献反馈与调整依据\n根据综述调整路线。')
    from lazymind.document_tools.paper.writing import revise_document
    def resolve_scope(request, review, **kwargs):
        assert request == '按当前审稿意见修订'
        assert any(issue['id'] == 'R1' for issue in review['issues'])
        assert kwargs['workflow_context'] == {
            'current_step': 'revise_document', 'previous_completed_step': 'peer_review',
            'review_matches_manuscript': True,
        }
        return ['R1']
    monkeypatch.setattr(revise_document, 'resolve_review_issues', resolve_scope)
    state = yaml.safe_load((PACKAGE / 'scenario/state.yml').read_text())
    workflow = yaml.safe_load((PACKAGE / 'workflow.yaml').read_text())
    types = {slot['id']: slot['type'] for slot in workflow['slots']}
    previous = lazyllm.globals.get('subagent_ctx')
    events = []
    latest = {}
    offloaded = []

    def run(step, inputs):
        workspace = tmp_path / str(len(events))
        workspace.mkdir()
        bound = {}
        for key, value in inputs.items():
            path = workspace / (key + '.json')
            # Core materializes managed output bytes before the next execution.
            if isinstance(value, dict) and value.get('path'):
                assert Path(value['path']).is_relative_to(tmp_path)
                path.write_bytes(Path(value['path']).read_bytes())
            else:
                path.write_text(json.dumps(value, ensure_ascii=False))
            bound[key] = str(path)
        slots = [item['material'] for item in state['steps'][step]['outputs']]
        emitted = []
        set_context(SubAgentContext(
            task_id='paper-' + workspace.name, conversation_id='paper-test', agent_type='workflow_step',
            objective='Mock contract test', params={'step_id': step, 'remote_inputs': bound,
                'current_user_input': '按当前审稿意见修订' if step == 'revise_document' else '',
                'output_slot_types': {slot: types[slot] for slot in slots}},
            workspace_path=str(workspace), input_slots=list(inputs), output_slots=slots,
            db=None, emit=emitted.append,
        ))
        assert tools.run_paper_step()['status'] == 'completed'
        artifacts = [event for event in emitted if event['type'] == 'artifact']
        assert {event['slot'] for event in artifacts} == set(slots)
        for event in artifacts:
            latest[event['slot']] = event['value']
            if event['slot'].endswith('_pool'):
                assert event['content_type'] == 'file'
                assert event['value']['filename'] == event['slot'] + '.json'
                tools.validate_pool(tools._read_bound(event['value']['path']))
            if 'path' in event['value']:
                offloaded.append(event['slot'])
        if step == 'peer_review':
            structured = json.loads(Path(latest['peer_review_data']['path']).read_text())
            pool = tools._read_bound(latest['peer_review_pool']['path'])
            assert structured == tools.material(pool, 'review_data')['content']
            pool['selected_issue_ids'] = ['R1']
            inputs = tools.business_input('revise_document', pool)
            api.contracts.validate_input('revise_document', inputs)
            assert inputs['review_data'] == structured
        events.extend(artifacts)

    try:
        run(start, {'paper_input': {'demo': True}})
        for _ in range(10):
            run(feedback, {start+'_pool': latest[start+'_pool'], start+'_document': latest[start+'_document']})
            # Mimics the exact frozen pool/document pair supplied by Core rewind.
            run(start, {recovery+'_pool': latest[feedback+'_pool'],
                        recovery+'_document': latest[feedback+'_document']})
        value = latest[start+'_pool']
        pool = tools._read_bound(value['path']) if 'path' in value else value['data']
        assert pool['sequence'] == 21
        assert offloaded, 'must exercise real file publication, not only inline mocks'
    finally:
        if previous is None:
            lazyllm.globals.pop('subagent_ctx', None)
        else:
            lazyllm.globals['subagent_ctx'] = previous
