from pathlib import Path
from unittest.mock import MagicMock

import pytest

from lazymind.chat.workflow.client import (
    AdvanceRequest, StepCommand, WorkflowClient, WorkflowClientError,
)
from lazymind.chat.workflow.file_adapter import LazyMindHostFileAdapter


def response(status, body):
    value = MagicMock(status_code=status)
    value.json.return_value = body
    return value


def test_advance_uses_typed_facade_payload_without_retry():
    transport = MagicMock()
    transport.post.return_value = response(200, {
        'ok': True, 'request_id': 'r1', 'result': {'accepted': True},
    })
    client = WorkflowClient('http://core/api/core', 'u1', transport=transport)
    result = client.advance(AdvanceRequest(
        session_id='ws1', expected_state_version=2,
        steps=[StepCommand(step_id='draft', user_input='write')], command_id='cmd1',
        workflow_mode='auto',
    ))
    assert result.result['accepted'] is True
    call = transport.post.call_args
    assert call.args[0].endswith('/workflow-sessions/ws1:advance-step')
    assert call.kwargs['json']['contract_version'] == 'workflow.v1'
    assert call.kwargs['json']['workflow_mode'] == 'auto'
    assert call.kwargs['json']['steps'][0]['step_id'] == 'draft'


def test_structured_error_mapping():
    transport = MagicMock()
    transport.post.return_value = response(409, {'ok': False, 'error': {
        'code': 'STATE_VERSION_CONFLICT', 'message': 'stale', 'retryable': False,
        'details': {'actual': 3},
    }})
    with pytest.raises(WorkflowClientError) as caught:
        WorkflowClient('http://core', transport=transport).advance(AdvanceRequest(
            session_id='ws1', expected_state_version=2,
            steps=[StepCommand(step_id='draft')],
        ))
    assert caught.value.code == 'STATE_VERSION_CONFLICT'
    assert caught.value.details == {'actual': 3}
    assert transport.post.call_count == 1


def test_host_file_adapter_returns_only_stable_resource_fields(tmp_path: Path):
    source = tmp_path / 'requirements.txt'
    source.write_text('stable input')
    transport = MagicMock()
    transport.post.return_value = response(200, {'ok': True, 'result': {
        'resource_id': 'res1', 'name': source.name, 'mime_type': 'text/plain',
        'size': 12, 'content_hash': 'sha256:abc', 'revision': 1,
    }})
    resource = LazyMindHostFileAdapter('http://core', 'u1', transport=transport).import_attachment(str(source))
    assert resource.resource_id == 'res1'
    assert resource.name == source.name
    assert resource.mime_type == 'text/plain'
    assert resource.size == 12
    assert resource.to_binding() == {'resource_id': 'res1', 'revision': 1, 'content_hash': 'sha256:abc'}
    assert not hasattr(resource, 'path')
    assert not hasattr(resource, 'url')
    assert str(tmp_path) not in repr(resource)


def test_imported_text_preparation_sends_binding_reference_not_resource_metadata():
    transport = MagicMock()
    transport.post.side_effect = [
        response(200, {'ok': True, 'result': {
            'resource_id': 'text-1', 'name': 'request.txt', 'mime_type': 'text/plain; charset=utf-8',
            'size': 7, 'content_hash': 'sha256:request', 'revision': 2,
        }}),
        response(200, {'ok': True, 'result': {'preparation_id': 'prepare-1'}}),
    ]
    adapter = LazyMindHostFileAdapter('http://core', 'u1', transport=transport)
    resource = adapter.import_text('request.txt', 'test me')
    result = adapter.client.prepare_workflow('novel-chapter-generation', input_bindings={
        'user_request': resource.to_binding(),
    }, command_id='prepare-1')

    assert result.result['preparation_id'] == 'prepare-1'
    assert transport.post.call_count == 2
    call = transport.post.call_args
    assert call.args[0].endswith('/workflow-preparations')
    assert call.kwargs['json']['input_bindings'] == {
        'user_request': {'resource_id': 'text-1', 'revision': 2, 'content_hash': 'sha256:request'},
    }
