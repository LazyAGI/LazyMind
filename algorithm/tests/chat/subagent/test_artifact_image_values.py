from lazymind.chat.engine.subagent.context import SubAgentContext, set_context
from lazymind.chat.engine.subagent.tools import _build_artifact_value


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
