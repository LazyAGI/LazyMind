import time
import uuid

import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient

from lazymind.conversation.api import grouping_routes, title_routes
from lazymind.chat.api import llm_task_routes


@pytest.fixture
def client():
    app = FastAPI()
    app.include_router(title_routes.router)
    app.include_router(grouping_routes.router)
    app.include_router(llm_task_routes.router)
    with TestClient(app) as client:
        yield client


def test_title_and_batch_use_explicit_provider(client, provider):
    config, calls = provider
    response = client.post('/api/conversation/title:generate', json={
        'input': {'text': '处理邮件'}, 'llm_config': config,
    })
    assert response.status_code == 200
    assert response.json()['status'] == 'succeeded', response.json()
    assert response.json()['output']['intent_status'] == 'ready'
    response = client.post('/api/conversation/titles:generate', json={
        'items': [{'id': str(i), 'input': {'text': '处理邮件'}} for i in range(2)],
        'llm_config': config,
    })
    assert response.status_code == 200
    assert response.json()['status'] == 'succeeded', response.json()
    assert {item['id'] for item in response.json()['output']['items']} == {'0', '1'}
    assert len(calls) == 2
    assert all(call['model'] == 'controlled-conversation' for call in calls)


def test_grouping_sync_and_worker_transport(client, provider):
    config, calls = provider
    payload = {'input': {'snapshot_hash': 'frozen', 'phase': 'batch', 'directory': [],
                         'conversations': [{'id': 'c1', 'summary': '处理邮件'}]},
               'llm_config': config}
    sync = client.post('/api/conversation/grouping:run', json=payload).json()
    assert sync['status'] == 'succeeded', sync
    execution_id = str(uuid.uuid4())
    payload['options'] = {'execution_issued_at': time.time()}
    response = client.post(f'/api/conversation/grouping-executions/{execution_id}:stream', json=payload)
    assert response.status_code == 200
    import json
    events = [json.loads(line) for line in response.text.splitlines()]
    assert events[-1]['type'] == 'result'
    assert events[-1]['result']['status'] == 'succeeded', events
    assert events[-1]['result']['output'] == sync['output']
    assert client.post(f'/api/conversation/grouping-executions/{execution_id}:cancel').json() == {'settled': True}
    assert len(calls) == 2


@pytest.mark.parametrize('config', [{}, {'llm': {}}, {'llm': {'model': 'test'}}, {'llm': 'invalid'}])
def test_configuration_failure_is_explicit(client, config):
    response = client.post('/api/conversation/title:generate', json={'llm_config': config})
    assert response.status_code == 200
    result = response.json()
    assert result['error_code'] == 'model_configuration'
    assert result['usage']['model_calls'] == 0


def test_legacy_task_types_and_routes_are_rejected(client):
    for task in ('conversation.describe_opening', 'conversation.describe_opening_batch', 'conversation.organize_step'):
        response = client.post('/api/chat/llm-task:run', json={'task_type': task})
        assert response.status_code == 502
        assert response.json()['detail'] == 'unsupported_task_type'
    assert client.post('/api/chat/organizer-executions/old:cancel').status_code == 404
    assert client.post('/api/chat/organizer-executions/old:stream', json={}).status_code == 404
    assert client.post('/api/conversation/title:generate', json={'task_type': 'general'}).status_code == 422


@pytest.mark.parametrize('router,child,expected', [('false', 'false', 200),
                                                   ('true', 'false', 200),
                                                   ('true', 'true', 404)])
def test_conversation_routes_are_owned_by_parent_process(router, child, expected):
    import os
    from pathlib import Path
    import subprocess
    import sys

    root = Path(__file__).resolve().parents[3]
    script = """
from unittest.mock import patch
from fastapi.testclient import TestClient
with patch('lazymind.chat.workflow.remote_executor.start_remote_workflow_executor'):
    from lazymind.router.app import create_app
    app = create_app()
client = TestClient(app)
response = client.post('/api/conversation/title:generate', json={})
assert response.status_code == EXPECTED, (response.status_code, response.text)
if EXPECTED == 200:
    assert response.json()['error_code'] == 'model_configuration'
assert client.post('/api/chat/organizer-executions/old:cancel').status_code == 404
"""
    env = {**os.environ, 'PYTHONPATH': os.pathsep.join((str(root / 'algorithm' / 'lazyllm'), str(root / 'algorithm'))),
           'LAZYMIND_ENABLE_ROUTER': router, 'LAZYMIND_ROUTER_CHILD_PROXIED_ONLY': child,
           'LAZYMIND_BACKGROUND_JOBS_ENABLED': 'false', 'LAZYMIND_RUNTIME_MODE': 'test',
           'LAZYMIND_ROUTER_CHILD_PROCESSES_ENABLED': 'false'}
    result = subprocess.run([sys.executable, '-c', script.replace('EXPECTED', str(expected))],
                            cwd=root, env=env, capture_output=True, text=True, timeout=40)
    assert result.returncode == 0, result.stdout + result.stderr
