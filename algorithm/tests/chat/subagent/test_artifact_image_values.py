import json

import lazyllm

from lazymind.chat.engine.subagent import tools as subagent_tools
from lazymind.chat.engine.subagent.context import SubAgentContext, set_context
from lazymind.chat.engine.subagent.tools import (
    _build_artifact_value,
    _validate_declared_artifact_type,
    get_artifact,
    patch_artifact,
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
    ctx.params['remote_inputs']['empty_images'] = []
    empty = get_artifact('empty_images')['result']

    assert [item['value']['path'] for item in all_items] == paths
    assert [item['value']['path'] for item in second] == paths[1:]
    assert empty['status'] == 'empty'


def test_get_artifact_returns_external_workflow_scalar_as_text(tmp_path):
    ctx = _context(str(tmp_path))
    ctx.params.update({
        'remote_inputs': {'topic': '人工智能辅助软件测试', 'word_target': '400'},
        'remote_input_types': {'topic': 'text', 'word_target': 'text'},
        'remote_input_value_slots': ['topic', 'word_target'],
    })
    set_context(ctx)
    lazyllm.globals['agentic_config'] = {'workflow_session_id': 'session-1'}

    topic = get_artifact('topic')['result']['artifacts'][0]
    target = get_artifact('word_target')['result']['artifacts'][0]

    assert topic == {
        'slot': 'topic', 'content_type': 'text',
        'value': {'text': '人工智能辅助软件测试'},
    }
    assert target['value']['text'] == '400'


def test_find_artifact_does_not_treat_plain_text_as_a_path(monkeypatch, tmp_path):
    set_context(_context(str(tmp_path)))
    monkeypatch.setattr(
        subagent_tools,
        'get_artifact',
        lambda slot, sort_order=None: {
            'success': True,
            'result': {
                'status': 'ok',
                'artifacts': [{'value': {'text': 'ordinary artifact content'}}],
            },
        },
    )
    lazyllm.globals['agentic_config'] = {'workflow_session_id': 'session-1'}

    result = subagent_tools.find_artifact('report')

    assert result['success'] is True
    assert result['result']['status'] == 'error'
    assert 'no resolvable path' in result['result']['message']


def test_patch_artifact_decodes_model_facing_json_string(tmp_path):
    ctx = _context(str(tmp_path))
    set_context(ctx)
    ctx.write_draft('metadata', 'json', '{"status":"old"}', pending_commit=False)

    result = patch_artifact(
        'metadata', '{"status":"new"}', patch_type='json_merge',
    )

    assert result['result']['status'] == 'ok'
    content, original_type = ctx.read_draft('metadata')
    assert original_type == 'json'
    assert json.loads(content) == {'status': 'new'}

    result = patch_artifact(
        'metadata', '[{"op":"replace","path":"/status","value":"final"}]',
        patch_type='json_patch',
    )
    assert result['result']['status'] == 'ok'
    assert json.loads(ctx.read_draft('metadata')[0]) == {'status': 'final'}

    ctx.write_draft('report', 'text', 'before', pending_commit=False)
    result = patch_artifact(
        'report', '{"old_str":"before","new_str":"after"}', patch_type='str_replace',
    )
    assert result['result']['status'] == 'ok'
    assert ctx.read_draft('report')[0] == 'after'


def test_patch_artifact_rejects_invalid_json_string_without_dirtying_draft(tmp_path):
    ctx = _context(str(tmp_path))
    set_context(ctx)
    ctx.write_draft('report', 'text', 'before', pending_commit=False)

    result = patch_artifact('report', 'not-json', patch_type='str_replace')

    assert result['result']['status'] == 'error'
    assert ctx.list_pending_drafts() == []
    assert ctx.read_draft('report')[0] == 'before'
