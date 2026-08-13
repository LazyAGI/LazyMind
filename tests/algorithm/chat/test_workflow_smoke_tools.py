import base64
import importlib.util
import json
from pathlib import Path
from types import SimpleNamespace

import pytest
import yaml


def _load_tools():
    root = Path(__file__).resolve().parents[3]
    path = root / 'workflows' / 'test-workflow' / 'scripts' / 'tools.py'
    spec = importlib.util.spec_from_file_location('workflow_smoke_tools', path)
    module = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    spec.loader.exec_module(module)
    return module


def test_smoke_tools_cover_json_types_rewrite_and_list():
    tools = _load_tools()

    metadata = tools.build_test_metadata('hello')
    typed = tools.create_typed_fixtures('hello')
    rewritten = tools.create_rewrite_fixture('hello')
    tools.rewrite_fixture(rewritten, 'revision-2')
    listed = tools.create_list_fixtures()
    result = tools.verify_fixtures(
        typed['text_path'], typed['image_url'], rewritten, listed,
    )

    assert metadata == {
        'smoke_test': True,
        'summary': 'hello',
        'schema': 'test.v1',
    }
    assert typed['image_url'].startswith('https://placehold.co/640x360/')
    assert 'Workflow+Smoke+Test' in typed['image_url']
    assert 'revision-2' in Path(rewritten).read_text(encoding='utf-8')
    assert [Path(path).name for path in listed] == ['list-1.txt', 'list-2.txt']
    assert result['status'] == 'Workflow smoke test passed'
    assert Path(result['report_path']).is_file()


class _ArtifactStore:
    def __init__(self, cardinalities):
        self.cardinalities = cardinalities
        self.values = {}

    def save(self, slot, content_type, value, caption=None):
        snapshot = value
        if content_type == 'file':
            path = Path(value)
            snapshot = {'path': str(path), 'content': path.read_bytes()}
        revision = {
            'content_type': content_type,
            'value': snapshot,
            'caption': caption,
        }
        if self.cardinalities[slot] == 'list':
            self.values.setdefault(slot, []).append([revision])
        else:
            self.values.setdefault(slot, []).append(revision)

    def latest(self, slot):
        values = self.values[slot]
        if self.cardinalities[slot] == 'list':
            return [item[-1] for item in values]
        return values[-1]


class _FixedModel:
    """Fixed model I/O; no model provider or token is used by this test."""

    def __init__(self):
        self.calls = []

    def run(self, step, prompt, inputs):
        self.calls.append({'step': step, 'prompt': prompt, 'inputs': inputs})
        outputs = {
            'prompt': {'summary': '运行测试工作流，内容是 hello'},
            'script': {'tool': 'build_test_metadata'},
            'typed_artifacts': {'tool': 'create_typed_fixtures'},
            'rewrite': {'tools': ['create_rewrite_fixture', 'rewrite_fixture']},
            'list_artifacts': {'tool': 'create_list_fixtures'},
            'verify': {'tool': 'verify_fixtures'},
        }
        return outputs[step]


def test_complete_smoke_workflow_with_fixed_model_io():
    tools = _load_tools()
    root = Path(__file__).resolve().parents[3]
    plugin = yaml.safe_load(
        (root / 'workflows/test-workflow/workflow.yaml').read_text(encoding='utf-8'),
    )
    state = yaml.safe_load(
        (root / 'workflows/test-workflow/scenario/state.yml').read_text(encoding='utf-8'),
    )
    cardinalities = {
        slot['id']: slot.get('cardinality', 'single') for slot in plugin['slots']
    }
    artifacts = _ArtifactStore(cardinalities)
    model = _FixedModel()
    summary = '运行测试工作流，内容是 hello'

    expected_steps = [
        'prompt', 'script', 'typed_artifacts', 'rewrite', 'list_artifacts', 'verify',
    ]
    current = state['transitions']['__start__'][0]['to']
    visited = []
    while current != '__end__':
        visited.append(current)
        definition = state['steps'][current]
        inputs = {
            item['material']: artifacts.latest(item['material'])
            for item in definition.get('inputs', [])
        }
        action = model.run(current, definition['prompt'], inputs)

        if current == 'prompt':
            artifacts.save('prompt_result', 'text', action['summary'])
        elif current == 'script':
            metadata = tools.build_test_metadata(summary)
            artifacts.save('metadata_json', 'json', metadata)
        elif current == 'typed_artifacts':
            typed = tools.create_typed_fixtures(summary)
            artifacts.save('text_attachment', 'file', typed['text_path'])
            artifacts.save('image_attachment', 'image', typed['image_url'])
        elif current == 'rewrite':
            path = tools.create_rewrite_fixture(summary)
            artifacts.save('rewritten_attachment', 'file', path)
            tools.rewrite_fixture(path, 'revision-2')
            artifacts.save('rewritten_attachment', 'file', path)
        elif current == 'list_artifacts':
            for index, path in enumerate(tools.create_list_fixtures(), start=1):
                artifacts.save('list_attachments', 'file', path, f'item-{index}')
        elif current == 'verify':
            text = artifacts.latest('text_attachment')['value']['path']
            image = artifacts.latest('image_attachment')['value']
            rewritten = artifacts.latest('rewritten_attachment')['value']['path']
            listed = [item['value']['path'] for item in artifacts.latest('list_attachments')]
            result = tools.verify_fixtures(text, image, rewritten, listed)
            artifacts.save('verification_report', 'file', result['report_path'])
            artifacts.save('test_status', 'text', result['status'])
        current = state['transitions'][current][0]['to']

    assert visited == expected_steps
    assert [call['step'] for call in model.calls] == expected_steps
    assert '{{user_input}}' in model.calls[0]['prompt']
    assert model.calls[1]['inputs']['prompt_result']['value'] == summary
    assert artifacts.latest('metadata_json')['content_type'] == 'json'
    assert artifacts.latest('image_attachment')['value'].startswith('https://placehold.co/')
    rewrite_revisions = artifacts.values['rewritten_attachment']
    assert len(rewrite_revisions) == 2
    assert b'revision-2' not in rewrite_revisions[0]['value']['content']
    assert b'revision-2' in rewrite_revisions[1]['value']['content']
    listed = artifacts.latest('list_attachments')
    assert [item['caption'] for item in listed] == ['item-1', 'item-2']
    assert artifacts.latest('test_status')['value'] == 'Workflow smoke test passed'
    report = artifacts.latest('verification_report')['value']['content']
    assert all(json.loads(report).values())


def test_workflow_action_route_uses_pinned_definition_and_server_owned_arguments(monkeypatch):
    from fastapi import HTTPException
    from lazymind.chat.api import workflow_routes

    definition = yaml.safe_dump({'artifact_actions': {'rewrite_selection': {
        'slots': ['draft_document'], 'preview_tool': 'preview_rewrite',
    }}}).encode()
    package = {
        'revision_id': 'revision-1', 'tree_hash': 'tree-1',
        'files': {'workflow.yaml': base64.b64encode(definition).decode()},
    }
    fetches = []

    class FakeWorkflowClient:
        def __init__(self, *_args, **_kwargs):
            pass

        def get_workflow(self, workflow_id, revision_id):
            fetches.append((workflow_id, revision_id))
            return SimpleNamespace(result=package)

    def preview_rewrite(*, artifact, artifact_store, slot, instruction):
        return artifact, artifact_store, slot, instruction

    monkeypatch.setattr(workflow_routes, 'WorkflowClient', FakeWorkflowClient)
    monkeypatch.setattr(
        workflow_routes, 'load_workflow_package_tools',
        lambda loaded, names, *_identity: (
            {'preview_rewrite': preview_rewrite} if loaded is package and names else {}
        ),
    )
    monkeypatch.setattr(workflow_routes, 'inject_model_config', lambda _config: None)
    monkeypatch.setattr(workflow_routes, 'inject_tool_config', lambda _config: None)

    payload = {
        'workflow_id': 'writer-workflow',
        'revision_id': 'revision-1',
        'tree_hash': 'tree-1',
        'action': 'rewrite_selection',
        'phase': 'preview',
        'slot': 'draft_document',
        'artifact': {'path': '/tmp/draft.md'},
        'artifact_store': '/tmp/action',
        'arguments': {'instruction': '润色'},
    }

    request = workflow_routes.WorkflowActionInvokeRequest.model_validate(payload)
    assert workflow_routes.invoke_workflow_action(request)['result'] == (
        {'path': '/tmp/draft.md'}, '/tmp/action', 'draft_document', '润色',
    )
    assert fetches == [('writer-workflow', 'revision-1')]

    payload['arguments'] = {'instruction': '润色', 'slot': 'outline_document'}
    with pytest.raises(HTTPException, match='reserved arguments') as error:
        workflow_routes.invoke_workflow_action(
            workflow_routes.WorkflowActionInvokeRequest.model_validate(payload),
        )
    assert error.value.status_code == 400
