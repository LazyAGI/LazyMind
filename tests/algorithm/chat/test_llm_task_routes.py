from fastapi import FastAPI
from fastapi.testclient import TestClient

from lazymind.chat.api import llm_task_routes
from lazymind.chat.service.llm_task import LLMTaskResult


def test_repair_failure_endpoint_preserves_safe_classification(monkeypatch):
    monkeypatch.setattr(llm_task_routes, 'run_llm_task', lambda request: LLMTaskResult(
        status='failed', task_id='test-task', error='private provider body: secret-key',
        error_code='transport_error', diagnostic_id='diag-123', retryable=True,
    ))
    app = FastAPI()
    app.include_router(llm_task_routes.router)
    with TestClient(app) as client:
        response = client.post('/api/chat/llm-task:run', json={'task_type': 'workflow.repair'})
        assert response.status_code == 502
        assert response.json()['detail'] == {
            'error_code': 'transport_error', 'diagnostic_id': 'diag-123', 'retryable': True,
        }
        assert 'secret-key' not in response.text


def test_non_repair_task_failure_keeps_existing_response_contract(monkeypatch):
    monkeypatch.setattr(llm_task_routes, 'run_llm_task', lambda request: LLMTaskResult(
        status='failed', task_id='test-task', error='model_configuration', error_code='model_configuration',
    ))
    app = FastAPI()
    app.include_router(llm_task_routes.router)
    with TestClient(app) as client:
        response = client.post('/api/chat/llm-task:run', json={'task_type': 'general'})
        assert response.status_code == 502
        assert response.json()['detail'] == 'model_configuration'
