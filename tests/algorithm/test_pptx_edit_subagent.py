from unittest.mock import MagicMock

import pytest
from lazyllm.tools.agent import ToolExecutionError

from lazymind.chat.engine.subagent import pptx_edit
from lazymind.chat.engine.subagent.context import SubAgentContext
from lazymind.chat.engine.subagent.runner import _resolve_runtime_tools, _build_subagent_tools
from lazymind.chat.engine.subagent.tools import artifact_is_deliverable


@pytest.fixture
def context(monkeypatch):
    ctx = SubAgentContext('task', 'conversation', 'pptx_edit', 'edit notes',
                          {'_pptx_edit_session_id': 'session',
                           'output_slot_types': {'pptx_edit_receipt': 'json'}},
                          '/tmp', [], ['pptx_edit_receipt'], None, MagicMock())
    monkeypatch.setattr(pptx_edit, 'require_context', lambda: ctx)
    return ctx


def test_scoped_worker_tool_surface():
    runtime = _resolve_runtime_tools(['web_search', 'run_script'], {'_pptx_edit_session_id': 'session'})
    tools = _build_subagent_tools(runtime, tools_only=True)
    assert [tool.__name__ for tool in tools] == [
        'inspect_current_pptx', 'read_pptx_edit_material', 'publish_pptx_edit']


def test_publish_receipt_only_after_success(context, monkeypatch):
    edit = MagicMock(return_value={'status': 'ok', 'workflow_artifact': {'id': 'revision-2'}})
    monkeypatch.setattr(pptx_edit.tools, 'edit_presentation', edit)
    edit.return_value = {'sha256': 'hash', 'slide_count': 1,
                         'pages': [{'page': 1, 'slots': [{'id': '1:5', 'text': 'date', 'run_style': {}}],
                                    'speaker_notes': '', 'layout_checks': ['large']}]}
    view = pptx_edit.inspect_current_pptx()
    assert view['source_sha256'] == 'hash'
    assert view['pages'][0]['slots'] == [{'id': '1:5', 'text': 'date'}]
    assert 'layout_checks' not in view['pages'][0]
    assert edit.call_args.kwargs['workflow_session_id'] == 'session'
    assert not context.local_artifacts()
    edit.return_value = {'status': 'ok', 'workflow_artifact': {'id': 'revision-2'}}
    notes = [{'page': 1, 'text': '详细讲稿'}]
    pptx_edit.publish_pptx_edit('hash', speaker_notes=notes, require_all_notes=True)
    assert edit.call_args.kwargs['speaker_notes'] == notes
    assert edit.call_args.kwargs['require_all_notes'] is True
    receipt = context.local_artifacts()[0]
    assert artifact_is_deliverable(context, receipt)
    assert receipt['value']['data']['id'] == 'revision-2'
    context.emit.assert_called_once()
    with pytest.raises(ToolExecutionError):
        pptx_edit.publish_pptx_edit('hash', speaker_notes=notes)


def test_failed_publish_cannot_complete_task(context, monkeypatch):
    monkeypatch.setattr(pptx_edit.tools, 'edit_presentation', lambda *a, **kw: {'status': 'error'})
    with pytest.raises(ToolExecutionError):
        pptx_edit.publish_pptx_edit('hash')
    assert context.local_artifacts() == []
    context.emit.assert_not_called()


def test_material_read_is_restricted(context):
    with pytest.raises(ToolExecutionError):
        pptx_edit.read_pptx_edit_material('native_pptx')
    context.agent_type = 'research'
    with pytest.raises(ToolExecutionError):
        pptx_edit.inspect_current_pptx()


def test_material_uses_public_artifact_id(context, monkeypatch):
    monkeypatch.setattr(pptx_edit.tools, '_public_workflow_artifacts', lambda *a: [
        {'artifact_id': 'public-id', 'selected': True, 'validity': 'effective'}])
    client = MagicMock()
    client.read_artifact.return_value.result = {'value': {'text': 'evidence'}}
    monkeypatch.setattr(pptx_edit.tools, '_workflow_client', lambda: client)
    assert pptx_edit.read_pptx_edit_material()['artifacts'][0]['value']['text'] == 'evidence'
    client.read_artifact.assert_called_once_with('public-id')


def test_failed_edit_stops_instead_of_spinning():
    event = {'tool_results': [{'name': 'publish_pptx_edit', 'result': '{"ok":false,"value":"invalid id"}'}]}
    failures = {}
    pptx_edit.enforce_failure_budget(event, failures)
    with pytest.raises(ToolExecutionError, match='failed twice'):
        pptx_edit.enforce_failure_budget(event, failures)


def test_publish_tool_schema_is_buildable():
    from lazyllm.tools.agent.toolsManager import ModuleTool
    tool = ModuleTool(apply_func=pptx_edit.publish_pptx_edit, schema_func=pptx_edit.publish_pptx_edit)
    assert tool is not None


def test_font_edit_delegates_scope_and_keeps_one_receipt(context, monkeypatch):
    edit = MagicMock(return_value={'status': 'ok', 'workflow_artifact': {'id': 'revision-2'}})
    monkeypatch.setattr(pptx_edit.tools, 'edit_presentation', edit)
    pptx_edit.publish_pptx_edit(
        'hash',
        target_font='Noto Sans SC',
        pages=[1],
        slot_ids=['1:2'],
        allow_font_download=True)
    assert edit.call_args.kwargs['pages'] == [1]
    assert edit.call_args.kwargs['slot_ids'] == ['1:2']
    assert edit.call_args.kwargs['target_font'] == 'Noto Sans SC'
    assert edit.call_args.kwargs['allow_font_download'] is True
    assert len(context.local_artifacts()) == 1


def test_typography_batch_is_forwarded_without_dropping_false(context, monkeypatch):
    edit = MagicMock(return_value={'status': 'ok', 'workflow_artifact': {'id': 'revision-3'}})
    monkeypatch.setattr(pptx_edit.tools, 'edit_presentation', edit)
    styles = [{'pages': [1], 'font_size_pt': 28, 'bold': True},
              {'slot_ids': ['4:2'], 'bold': False, 'italic': True, 'underline': False}]
    pptx_edit.publish_pptx_edit('hash', style_edits=styles)
    assert edit.call_args.kwargs['style_edits'] == styles
    assert len(context.local_artifacts()) == 1
