from copy import deepcopy
from unittest.mock import MagicMock

import pytest

from lazymind.workflow_sdk import WorkflowClient


@pytest.mark.parametrize('entrypoint', ['prepare', 'external'])
def test_resource_descriptions_are_preprocessed_at_sdk_boundary(entrypoint):
    transport = MagicMock()
    transport.post.return_value = MagicMock(
        status_code=200, json=lambda: {'ok': True, 'data': {'session_id': 'session-1'}},
    )
    client = WorkflowClient('http://core.test', 'user-1', transport=transport)
    reference = {'resource_id': 'r1', 'revision': 2, 'content_hash': 'sha256:test'}
    resource = {**reference, 'name': 'brief.txt', 'mime_type': 'text/plain', 'size': 12}
    bindings = {'topic': resource, 'files': [resource, {'resource_id': 'r2'}, resource]}
    original = deepcopy(bindings)
    if entrypoint == 'prepare':
        client.prepare_workflow('ppt', input_bindings=bindings)
    else:
        client.start_skill_workflow_task(
            'codex', {'name': 'demo', 'url': 'https://example.com/demo.zip'}, 'Run',
            input_bindings=bindings,
        )
    assert transport.post.call_args.kwargs['json']['input_bindings'] == {
        'topic': reference, 'files': [reference, {'resource_id': 'r2'}, reference],
    }
    assert bindings == original


def test_preprocessing_preserves_invalid_references_for_core_validation():
    transport = MagicMock()
    transport.post.return_value = MagicMock(
        status_code=400, json=lambda: {'ok': False, 'error': {'code': 'INVALID_INPUT', 'message': 'invalid'}},
    )
    from lazymind.workflow_sdk import WorkflowClientError
    client = WorkflowClient('http://core.test', 'user-1', transport=transport)
    bindings = {'empty': [], 'bad': {'resource_id': '', 'revision': 0, 'unexpected': True}}
    with pytest.raises(WorkflowClientError):
        client.prepare_workflow('ppt', input_bindings=bindings)
    assert transport.post.call_args.kwargs['json']['input_bindings'] == bindings
