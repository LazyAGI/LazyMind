"""Delivery-first publication uses real artifact preparation and event emission."""
import importlib.util
import json
from pathlib import Path

import pytest
import yaml

from lazymind.chat.engine.subagent.context import SubAgentContext
from lazymind.chat.engine.subagent import tools as artifact_tools

ROOT = Path(__file__).resolve().parents[3]


def setup_writer(tmp_path, monkeypatch, flat=False):
    spec = importlib.util.spec_from_file_location('delivery_writer', ROOT / 'workflows/writer-workflow/scripts/tools.py')
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    events = []
    workflow_state = yaml.safe_load((ROOT / 'workflows/writer-workflow/scenario/state.yml').read_text())
    step = 'write_flat_document' if flat else 'write_document'
    output_slots = [item['material'] for item in workflow_state['steps'][step]['outputs']]
    context = SubAgentContext(
        task_id='writer-task', conversation_id='conversation', agent_type='workflow_step',
        objective='write', params={}, workspace_path=str(tmp_path / 'workspace'),
        input_slots=[], output_slots=output_slots, db=None, emit=events.append,
    )
    monkeypatch.setattr(module, 'require_context', lambda: context)
    monkeypatch.setattr(artifact_tools, 'require_context', lambda: context)
    source = tmp_path / 'source'
    source.mkdir()

    def file(key, content='{}'):
        path = source / f'{key}.json'
        path.write_text(content)
        return str(path)

    result = {'draft_document': file('body', '# Body'),
              'resolved_media_assets': file('media'), 'target_document': file('target'),
              'visual_plan': file('visual'), 'operation': 'generate'}
    if flat:
        result['short_writing_plan'] = file('short')
    else:
        result['section_instructions'] = file('instructions')
        result['draft_blocks'] = [file('block1'), file('block2')]
        result['document_revision_result'] = file('revision_result')
        result['document_write_result'] = file('write_result')
    state = {'result': result, 'completed': False}
    checkpoint = tmp_path / 'checkpoint.json'
    command = file('command', json.dumps({
        'action': 'create', 'source_role': 'none', 'target_stage': 'document',
        'next_step': 'write_flat_document' if flat else 'write_document',
        'structure_mode': 'flat' if flat else 'sectioned', 'user_instruction': 'write',
        'request_fingerprint': module._writer_request_fingerprint('write'),
    }))
    inputs = {'writer_command': command, 'writing_task': file('task', '{"query":"write","task_type":"write","output":{"representation":"markdown"}}'),
              'writing_context': file('context', '{"context_id":"ctx"}')}
    context.params.update(user_input='write', remote_inputs=inputs)
    monkeypatch.setattr(module, '_draft_workspace_state', lambda fingerprint: (
        json.loads(checkpoint.read_text()), checkpoint))

    def forbidden(*args, **kwargs):
        pytest.fail('Publication recovery reran generation or provider writing')

    for name in ('writer_generate_draft_document', 'writer_generate_short_writing_plan',
                 'writer_publish_revision', 'writer_prepare_markdown_for_editor'):
        monkeypatch.setattr(module, name, forbidden)
    return module, context, events, state, checkpoint


@pytest.mark.parametrize('flat', [False, True])
@pytest.mark.parametrize('recover', [False, True])
def test_no_image_empty_plan_has_no_pending_save(tmp_path, monkeypatch, flat, recover):
    module, context, events, state, checkpoint = setup_writer(tmp_path, monkeypatch, flat)
    state['result'].update(visual_plan='', visual_need_count=0)
    if recover:
        state.update(generation_completed=True, completed=True, artifacts_saved=False,
                     pending_auxiliary_artifacts=[{'key': 'visual_plan', 'index': 0, 'error': 'empty path'}])
        checkpoint.write_text(json.dumps(state))
        result = module.writer_flat_draft_workspace() if flat else module.writer_draft_workspace()
    else:
        result = module._save_draft_delivery(state, checkpoint, flat=flat)
    assert result['delivery_saved'] and result['artifacts_saved']
    assert not result['pending_auxiliary_artifacts']
    saved = json.loads(checkpoint.read_text())
    assert saved['artifacts_saved'] and not saved['pending_auxiliary_artifacts']
    slots = [event['slot'] for event in events if event['type'] == 'artifact']
    assert ('flat_draft_document' if flat else 'draft_document') in slots
    assert ('flat_visual_plan' if flat else 'visual_plan') not in slots


@pytest.mark.parametrize('visual_need_count', [None, 1])
def test_empty_plan_with_unknown_or_required_images_remains_pending(tmp_path, monkeypatch, visual_need_count):
    module, context, events, state, checkpoint = setup_writer(tmp_path, monkeypatch)
    state['result'].update(visual_plan='', visual_need_count=visual_need_count)
    result = module._save_draft_delivery(state, checkpoint)
    assert result['delivery_saved'] and not result['artifacts_saved']
    assert [item['key'] for item in result['pending_auxiliary_artifacts']] == ['visual_plan']


@pytest.mark.parametrize('flat', [False, True])
def test_auxiliary_failure_keeps_body_and_resumes_save_only(tmp_path, monkeypatch, flat):
    module, context, events, state, checkpoint = setup_writer(tmp_path, monkeypatch, flat)
    missing = state['result']['visual_plan']
    state['result']['visual_need_count'] = 0
    Path(missing).unlink()
    result = module._save_draft_delivery(state, checkpoint, flat=flat)
    body = 'flat_draft_document' if flat else 'draft_document'
    media = 'flat_resolved_media_assets' if flat else 'resolved_media_assets'
    visual = 'flat_visual_plan' if flat else 'visual_plan'
    assert result['status'] == 'completed' and result['delivery_saved']
    assert not result['artifacts_saved']
    assert [item['key'] for item in result['pending_auxiliary_artifacts']] == [visual]
    slots = [event['slot'] for event in events]
    assert slots.index(media) < slots.index(body)
    assert 'target_document' not in slots
    assert state['result']['target_document'] == str(tmp_path / 'source' / 'target.json')
    assert Path(state['result']['target_document']).read_text() == '{}'
    assert all(slots.index(key) > slots.index(body) for key in slots if key in module._DRAFT_AUXILIARY_KEYS)
    assert Path(state['result']['draft_document']).read_text() == '# Body'
    before = len(events)
    Path(missing).write_text('{}')
    resumed = module.writer_flat_draft_workspace() if flat else module.writer_draft_workspace()
    assert resumed['artifacts_saved'] and not resumed['pending_auxiliary_artifacts']
    assert [event['slot'] for event in events[before:] if event['type'] == 'artifact'] == [visual]
    assert json.loads(checkpoint.read_text())['completed']


@pytest.mark.parametrize('flat', [False, True])
def test_delivery_preflight_failure_emits_no_body_and_keeps_generation(tmp_path, monkeypatch, flat):
    module, context, events, state, checkpoint = setup_writer(tmp_path, monkeypatch, flat)
    missing = state['result']['resolved_media_assets']
    Path(missing).unlink()
    with pytest.raises(Exception, match='does not exist'):
        module._save_draft_delivery(state, checkpoint, flat=flat)
    assert not events
    assert json.loads(checkpoint.read_text())['generation_completed']
    Path(missing).write_text('{}')
    result = module.writer_flat_draft_workspace() if flat else module.writer_draft_workspace()
    assert result['artifacts_saved']


@pytest.mark.parametrize('failure_slot', ['draft_document', 'draft_blocks'])
def test_lost_ack_replays_same_sequence_after_process_restart(tmp_path, monkeypatch, failure_slot):
    module, context, events, state, checkpoint = setup_writer(tmp_path, monkeypatch)
    failed = False

    def fail_after_emit(event):
        nonlocal failed
        events.append(event)
        if event.get('slot') == failure_slot and not failed:
            failed = True
            raise OSError('lost acknowledgement')

    context.emit = fail_after_emit
    if failure_slot == 'draft_document':
        with pytest.raises(OSError, match='lost acknowledgement'):
            module._save_draft_delivery(state, checkpoint)
    else:
        result = module._save_draft_delivery(state, checkpoint)
        assert result['pending_auxiliary_artifacts']
    original = [e for e in events if e.get('type') == 'artifact']
    context._local_artifacts.clear()
    context._artifact_counts.clear()
    context.emit = events.append
    result = module.writer_draft_workspace()
    assert result['artifacts_saved']
    recovered = [e for e in events[len(original):] if e.get('type') == 'artifact']
    for event in original:
        assert event in recovered  # Same frozen file carrier AND event sequence.
    blocks = [e for e in recovered if e['slot'] == 'draft_blocks']
    assert [e['seq'] for e in blocks] == [1, 2]
    assert {a['slot'] for a in context.local_artifacts()} >= {'draft_document', 'resolved_media_assets'}


def test_body_contract_only_requires_delivery_outputs():
    state = yaml.safe_load((ROOT / 'workflows/writer-workflow/scenario/state.yml').read_text())
    # Exercise the actual contract shape used by the workflow compiler.
    steps = state['steps']
    for name, body in [('write_document', 'draft_document'), ('write_flat_document', 'flat_draft_document')]:
        outputs = steps[name]['outputs']
        assert [item['material'] for item in outputs if item.get('required', True)] == [body]
        assert not any(item['material'].endswith('target_document') for item in outputs)
        assert next(item for item in steps[name]['inputs'] if item['material'] == 'media_assets')['required']
    assert any(item['material'] == 'media_assets' and item.get('required', True)
               for item in steps['prepare']['outputs'])


def test_completed_legacy_checkpoint_does_not_create_versions(tmp_path, monkeypatch):
    module, context, events, state, checkpoint = setup_writer(tmp_path, monkeypatch)
    state.update(completed=True, artifacts_saved=True, saved_artifact_keys=['draft_document'])
    result = module._save_draft_delivery(state, checkpoint)
    assert result['status'] == 'completed'
    assert not events


def test_flat_no_image_checkpoint_survives_generation_retry(monkeypatch, tmp_path):
    module, context, events, state, checkpoint = setup_writer(tmp_path, monkeypatch, flat=True)
    body = state['result']['draft_document']
    plan = state['result']['short_writing_plan']
    state['result'] = {}
    checkpoint.write_text(json.dumps(state))
    task_path = Path(context.params['remote_inputs']['writing_task'])
    task = json.loads(task_path.read_text())
    task['constraints'] = {'visual_policy': {'allow_visuals': False}}
    task_path.write_text(json.dumps(task))
    plan_visuals = module.writer_generate_short_visual_plan
    calls = []
    monkeypatch.setattr(module, 'writer_generate_short_writing_plan', lambda **kw: plan)

    def visuals(**kwargs):
        calls.append('visuals')
        result = plan_visuals(**kwargs)
        assert result['visual_need_count'] == 0
        assert module._read_json_file(result['visual_plan'])['instructions'] == []
        return result

    def generate(**kwargs):
        assert Path(kwargs['visual_plan_path']).is_file()
        calls.append('generate')
        if calls.count('generate') == 1:
            raise TimeoutError('retry')
        return body

    monkeypatch.setattr(module, 'writer_generate_short_visual_plan', visuals)
    monkeypatch.setattr(module, 'writer_generate_short_document', generate)
    monkeypatch.setattr(module, 'writer_resolve_visual_media', lambda **kw: pytest.fail('unexpected images'))
    with pytest.raises(TimeoutError, match='retry'):
        module.writer_flat_draft_workspace()
    for _ in range(2):
        result = module.writer_flat_draft_workspace()
        assert result['delivery_saved'] and result['artifacts_saved']
        assert not result['pending_auxiliary_artifacts']
    assert calls == ['visuals', 'generate', 'generate']
    assert sum(event.get('slot') == 'flat_draft_document' for event in events) == 1
    assert sum(event.get('slot') == 'flat_visual_plan' for event in events) == 1
