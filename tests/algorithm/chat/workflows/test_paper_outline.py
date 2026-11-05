"""Outline boundary and edited-artifact handoff regressions (not live acceptance)."""
import copy
from unittest.mock import Mock

import pytest

from lazymind.document_tools.paper import contracts, workflow
from lazymind.document_tools.paper.writing import write_outline, write_document
from lazymind.document_tools.paper.runtime_events import bind_runtime_events

OUTLINE = '# Test paper\n\n## Method\n\n## Limitations'


@pytest.mark.parametrize('publication_fails', [False, True])
def test_completed_revision_is_one_delta_and_still_supports_publication_failure(publication_fails):
    emitted = []
    body = '# Revised manuscript\n\n' + '完整修订结果。' * 3000
    with bind_runtime_events(emitted.append, 'revise_document_document') as events:
        events.completed_preview(body)
        assert [e['type'] for e in emitted] == [
            'artifact_stream_start', 'artifact_stream', 'artifact_stream_end',
        ]
        assert emitted[1]['delta'] == body
        assert not any(e.get('progress') == 100 for e in emitted)
        if publication_fails:
            events.abort()
            assert emitted[-1]['type'] == 'artifact_stream_abort'
        else:
            events.complete()
            assert emitted[-1]['progress'] == 99  # Core confirms publication separately.
        count = len(emitted)
        events.completed_preview('late text')
        events.delta('late delta')
        assert len(emitted) == count


@pytest.mark.parametrize('validation_fails', [False, True])
def test_finished_draft_preview_flushes_before_publication(validation_fails):
    emitted = []
    with bind_runtime_events(emitted.append, 'write_document_document') as events:
        events.progress(80, '正文已生成')
        events.delta(OUTLINE)
        events.finish_preview()
        events.finish_preview()
        assert sum(e['type'] == 'artifact_stream_end' for e in emitted) == 1
        assert not any(e.get('progress') == 100 for e in emitted)
        # Validation still runs after the display queue is flushed.
        events.progress(90, '正在检查全文')
        if validation_fails:
            events.abort()
            events.abort()
            assert sum(e['type'] == 'artifact_stream_abort' for e in emitted) == 1
            assert not any(e.get('progress') == 100 for e in emitted)
        else:
            events.complete()
            assert emitted[-1]['progress'] == 99  # Core confirms publication separately.
            assert sum(e['type'] == 'artifact_stream_end' for e in emitted) == 1
    streams = [e for e in emitted if e['type'].startswith('artifact_stream')]
    assert len({e['stream_id'] for e in streams}) == 1
    assert [e['chunk_index'] for e in streams] == sorted({e['chunk_index'] for e in streams})
    assert ''.join(e['delta'] for e in streams if 'delta' in e) == OUTLINE


def test_finished_preview_can_restart_without_reusing_old_content():
    emitted = []
    with bind_runtime_events(emitted.append, 'write_document_document') as events:
        events.delta('old')
        events.finish_preview()
        events.restart('new')
        events.delta(' draft')
        events.finish_preview()
        events.abort()
    starts = [e for e in emitted if e['type'] == 'artifact_stream_start']
    assert len(starts) == 2
    latest = [e for e in emitted if e.get('stream_id') == starts[-1]['stream_id']]
    assert ''.join(e['delta'] for e in latest if 'delta' in e) == 'new draft'
    assert latest[-1]['type'] == 'artifact_stream_abort'


def test_outline_step_only_calls_public_outline_generation(monkeypatch):
    toolkit = Mock()
    def stream_outline(**kwargs):
        kwargs['on_delta']('# Test paper')
        kwargs['on_delta']('\n\n')
        kwargs['on_delta']('## Method\n\n## Limitations')
        return OUTLINE
    toolkit.stream_outline.side_effect = stream_outline
    prepared = {'writing_task': {'constraints': {}}, 'writing_context': {},
                'outline': None, 'charts': []}
    monkeypatch.setattr(write_outline, 'WriterCreateToolkit', lambda: toolkit)
    monkeypatch.setattr(write_outline, 'prepare_writer_context', lambda *a, **k: copy.deepcopy(prepared))
    inputs = contracts.validate_input('write_outline', {'topic': 'Test', 'question': 'Compare methods'})
    write_outline.validate_inputs(inputs)
    emitted = []
    with bind_runtime_events(emitted.append, 'write_outline_document') as events:
        result = write_outline.run(inputs)
        events.complete()
    assert any(event.get('current_phase') == '正在输出大纲内容' for event in emitted)
    assert emitted[-1]['current_phase'] == '产物已生成，正在确认发布'
    deltas = [event for event in emitted if event.get('type') == 'artifact_stream']
    assert deltas
    assert ''.join(event['delta'] for event in deltas) == OUTLINE
    contracts.validate_output('write_outline', result, inputs)
    assert result == {'outline': OUTLINE, 'is_demo': False}
    assert [call[0] for call in toolkit.method_calls] == ['stream_outline']


def test_confirmed_outline_is_reused_without_regeneration():
    toolkit = Mock()
    prepared = {'writing_task': {}, 'writing_context': {}, 'outline': OUTLINE, 'charts': []}
    assert write_outline.prepare_outline(prepared, toolkit) == OUTLINE
    toolkit.generate_outline.assert_not_called()


def test_edited_outline_slot_overrides_pool_and_preserves_request(monkeypatch):
    pool = workflow.pool_from_business({'topic': 'Test', 'question': 'Compare methods', 'request': 'Use Chinese'})
    monkeypatch.setattr(workflow, 'run_algorithm', lambda *a, **k: {'outline': OUTLINE, 'is_demo': False})
    result = workflow.execute_step('write_outline', pool, 'Use Chinese; mark missing evidence')
    edited = OUTLINE.replace('Limitations', 'Limitations and future work')
    resumed = workflow.pool_from_inputs({'write_outline_pool': result['pool'], 'write_outline_document': edited})
    assert workflow.material(resumed, 'outline')['content'] == edited
    assert workflow.material(resumed, 'manuscript') is None
    assert workflow.material(result['pool'], 'outline')['content'] == OUTLINE
    request = workflow._step_request('write_document', resumed, {'current_user_input': ''}, {'write_outline_pool': 'bound'})
    assert request == 'Use Chinese; mark missing evidence'


def test_outline_step_rejects_existing_manuscript():
    inputs = contracts.validate_input('write_outline', {'topic': 'Test', 'manuscript': OUTLINE})
    with pytest.raises(ValueError, match='existing manuscript'):
        write_outline.validate_inputs(inputs)


def test_drafting_preserves_confirmed_outline_content_and_version(monkeypatch):
    outline = OUTLINE + '\nDescribe limitations without inventing results.\n<!-- paper-charts: [] -->'
    pool = workflow.pool_from_business({'topic': 'Test', 'outline': outline})
    body = OUTLINE + '\nThe supplied evidence has limitations.'
    monkeypatch.setattr(write_document, 'run', lambda inputs: {'manuscript': body, 'is_demo': False})
    result = workflow.execute_step('write_document', pool, 'Write from the confirmed outline.')
    assert result['pool']['current']['outline'] == pool['current']['outline']
    assert workflow.material(result['pool'], 'outline')['content'] == outline
    assert workflow.material(result['pool'], 'manuscript')['content'] == body
    assert len(result['pool']['history'][-1]['outputs']) == 1


def test_drafting_requires_outline_and_checks_confirmed_headings():
    inputs = contracts.validate_input('write_document', {'topic': 'Test', 'question': 'Compare methods'})
    with pytest.raises(contracts.MissingMaterials, match='大纲'):
        write_document.validate_inputs(inputs)
    inputs['outline'] = OUTLINE
    write_document.validate_inputs(inputs)
    result = {'manuscript': OUTLINE + '\nBody text.', 'is_demo': False}
    contracts.validate_output('write_document', result, inputs)
    write_document.validate_result(result, inputs)
    with pytest.raises(ValueError, match='大纲与稿件不一致'):
        write_document.validate_result({**result, 'manuscript': OUTLINE.replace('Method', 'Other')}, inputs)
