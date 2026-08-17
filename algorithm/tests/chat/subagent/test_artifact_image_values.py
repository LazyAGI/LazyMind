import lazyllm

from lazymind.chat.engine.subagent.context import SubAgentContext, set_context
from lazymind.chat.engine.subagent.tools import (
    _build_artifact_value,
    _validate_declared_artifact_type,
    get_artifact,
)


def _context(workspace_path: str) -> SubAgentContext:
    return SubAgentContext(
        task_id='task-1',
        conversation_id='conversation-1',
        agent_type='workflow_step',
        objective='save image',
        params={},
        workspace_path=workspace_path,
        input_slots=[],
        output_slots=['enhanced_image_output'],
        db=None,  # type: ignore[arg-type]
        emit=lambda _event: None,
    )


def test_image_artifact_accepts_structured_path_and_caption(tmp_path):
    workspace = tmp_path / 'workspace'
    workspace.mkdir()
    set_context(_context(str(workspace)))
    source = tmp_path / 'edited.png'
    source.write_bytes(b'edited-image')

    value, content_type = _build_artifact_value({
        'path': str(source),
        'caption': 'edited result',
    }, 'image')

    assert content_type == 'image'
    assert value['caption'] == 'edited result'
    assert value['path'].startswith(str(workspace))
    assert value['path'] != str(source)


def test_image_artifact_accepts_structured_static_file_url(tmp_path):
    set_context(_context(str(tmp_path)))
    signed_url = '/static-files/ai_generated/result.png?expires=123&sig=test'

    value, content_type = _build_artifact_value({
        'image_url': signed_url,
        'caption': 'fallback result',
    }, 'image')

    assert content_type == 'image'
    assert value == {'path': signed_url, 'caption': 'fallback result'}


def test_text_artifact_unwraps_structured_text_value(tmp_path):
    set_context(_context(str(tmp_path)))

    value, content_type = _build_artifact_value({'text': '# Validation report'}, 'text')

    assert content_type == 'text'
    assert value == {'text': '# Validation report'}


def test_file_artifact_accepts_structured_path(tmp_path):
    set_context(_context(str(tmp_path)))
    source = tmp_path / 'outline.md'
    source.write_text('# Outline', encoding='utf-8')

    value, content_type = _build_artifact_value({'path': str(source)}, 'file')

    assert content_type == 'file'
    assert value['path'] == str(source)


def test_workflow_file_slot_rejects_text(tmp_path):
    ctx = _context(str(tmp_path))
    ctx.params['output_slot_types'] = {'enhanced_image_output': 'file'}

    assert _validate_declared_artifact_type(ctx, 'enhanced_image_output', 'text')
    assert _validate_declared_artifact_type(ctx, 'enhanced_image_output', 'file') is None


def test_get_artifact_preserves_remote_list_input_order(tmp_path):
    paths = [str(tmp_path / 'one.png'), str(tmp_path / 'two.png')]
    ctx = _context(str(tmp_path))
    ctx.params['remote_inputs'] = {'effect_images': paths}
    set_context(ctx)
    lazyllm.globals['agentic_config'] = {'workflow_session_id': 'session-1'}

    all_items = get_artifact('effect_images')['result']['artifacts']
    second = get_artifact('effect_images', sort_order=2)['result']['artifacts']

    assert [item['value']['path'] for item in all_items] == paths
    assert [item['value']['path'] for item in second] == paths[1:]
