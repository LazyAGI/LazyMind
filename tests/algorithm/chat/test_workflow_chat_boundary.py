"""Main Chat launches Workflow work without performing source collection first."""
import base64
from pathlib import Path
from unittest.mock import MagicMock

import pytest
import yaml

from lazymind.chat.workflow import workflow_manager as manager


ROOT = Path(__file__).resolve().parents[3]


@pytest.fixture
def ppt_package():
    source = (ROOT / 'workflows/ppt-workflow/workflow.yaml').read_bytes()
    package = yaml.safe_load(source)
    package['files'] = {'workflow.yaml': base64.b64encode(source).decode()}
    return package


@pytest.mark.parametrize('entry', ['discovery', 'selected', 'active'])
def test_chat_boundary_reaches_all_workflow_entry_points(monkeypatch, ppt_package, entry):
    client = MagicMock()
    client.get_workflow.return_value.result = ppt_package
    client.get_state.return_value = {
        'session_id': 'session', 'status': 'active', 'projection': {'ready': ['collect_materials']},
    }
    monkeypatch.setattr(manager, '_client', lambda: client)
    monkeypatch.setattr(manager, '_agentic_config', lambda: {'enable_workflow': True})
    item = {
        'workflow_id': 'ppt-workflow', 'workflow_ref': 'builtin:ppt-workflow',
        'revision_id': 'revision', 'runtime': ppt_package['runtime'],
        'name': ppt_package['name'], 'when_to_use': ppt_package['when_to_use'],
        'tool_name': 'trigger_ppt_workflow',
    }
    contribution = manager.resolve_workflow_injection(
        {'session_id': 'session', 'workflow_id': 'ppt-workflow', 'runtime': ppt_package['runtime']}
        if entry == 'active' else None,
        current_query='从 WorkLog 挑选贡献最大的4条工作记录，生成12页PPTX',
        workflow_catalog=[item], workflow_activations=[item],
        allowed_workflow_refs=['builtin:ppt-workflow'] if entry == 'selected' else None,
    )
    prompt = contribution.runtime_context
    assert 'main Chat owns requirement analysis and launch' in prompt
    assert 'Document body reads (doc read)' in prompt
    assert 'belong inside the owning Workflow steps' in prompt
    assert 'ordinary questions' in prompt
    assert 'read the entire attachment first' not in prompt
    client.transport.request.assert_not_called()


@pytest.mark.parametrize('managed', [False, True])
def test_startup_guidance_does_not_require_attachment_inspection(managed):
    policy = {
        'clarification_fields': [{'id': 'topic', 'question': '主题？', 'type': 'text',
                                  **({'required': True} if managed else {})}],
        'attachment_clarification': {
            'question_id_prefix': 'template_detail_',
            'instructions': 'Read every source document before launching.',
        },
    }
    prompt = manager._startup_clarification_guidance(policy)
    assert 'Read every source document before launching.' not in prompt
    assert 'read the entire attachment first' not in prompt
    assert ('Bind attachments without reading their bodies' in prompt if managed
            else 'trigger immediately without reading source documents' in prompt)


def test_ppt_trigger_preserves_collection_request_without_reading_sources(monkeypatch, ppt_package):
    brief = ('我叫白云浮，做转正答辩，用 template.pptx 生成12 页pptx；'
             '从 WorkLog 的工作记录中选出贡献最大的4条，均为我个人完成；'
             '面向直属领导，十五分钟演讲、五分钟问答。')
    client, toolkit = MagicMock(), MagicMock()
    client.get_workflow.return_value.result = ppt_package
    client.get_state.return_value = {'projection': {'ready': ['analyze_requirements']}}
    toolkit.prepare_workflow.return_value = {'session_id': 'session', 'ready_steps': ['analyze_requirements']}
    monkeypatch.setattr(manager, '_client', lambda: client)
    monkeypatch.setattr(manager, 'HostWorkflowToolkit', lambda *a, **kw: toolkit)
    monkeypatch.setattr(manager, '_agentic_config', lambda: {})
    monkeypatch.setattr(manager, '_resolve_workflow_attachment', lambda name: ('/uploads/' + name, None))
    monkeypatch.setattr(manager, '_import_attachment', lambda path: 'resource:template')
    monkeypatch.setattr(manager, '_import_text_binding', lambda key, value: {'resource_id': key, 'value': value})
    item = {
        'workflow_id': 'ppt-workflow', 'workflow_ref': 'builtin:ppt-workflow',
        'tool_name': 'trigger_ppt_workflow', 'runtime': ppt_package['runtime'],
    }
    trigger = manager._workflow_trigger_tools([item], set(), current_query=brief)[0]
    result = trigger(input_bindings={
        'topic': '转正答辩', 'template_pptx': 'template.pptx',
        'presentation_output_format': 'native_pptx',
    }, clarification_extractions={'slide_count': [{'value': '12', 'evidence': '12 页pptx'}]})
    assert result['session_id'] == 'session'
    toolkit.prepare_workflow.assert_called_once()
    submitted = toolkit.prepare_workflow.call_args.kwargs
    assert brief in submitted['request_context']
    assert submitted['input_bindings']['template_pptx'] == 'resource:template'
    assert submitted['clarification_values']['presentation_output_format'] == 'native_pptx'
    assert submitted['clarification_values']['slide_count'] == '12'
    toolkit.advance_step.assert_not_called()
    client.transport.request.assert_not_called()
