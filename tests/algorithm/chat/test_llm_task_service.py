from __future__ import annotations

import json

import pytest
from lazyllm.module.llms.onlinemodule.base import (
    ModelCallError, ModelCallTerminal, ModelFailure, ModelFailureCode,
    ModelFailureOrigin, ModelFinish,
)

from lazymind.chat.service import llm_task
from lazymind.chat.service.llm_task import LLMTaskFile, LLMTaskInput, LLMTaskRequest, run_llm_task


class _JSONModel:
    def __init__(self, payload):
        self.payload = payload
        self.prompts = []

    def __call__(self, prompt, **kwargs):
        self.prompts.append(prompt)
        return json.dumps(self.payload)


def test_workflow_task_loads_builtin_skill_and_returns_json(monkeypatch):
    model = _JSONModel({'design_brief': 'Slots: input_text. Steps: summarize.'})
    monkeypatch.setattr(llm_task, 'inject_model_config', lambda _config: None)
    monkeypatch.setattr(llm_task, 'AutoModel', lambda **_kwargs: model)

    result = run_llm_task(LLMTaskRequest(
        mode='agent',
        task_type='workflow.design_brief',
        skills=['create-workflow'],
        input=LLMTaskInput(data={'name': 'demo'}),
    ))

    assert result.status == 'succeeded'
    assert result.output['design_brief'] == 'Slots: input_text. Steps: summarize.'
    assert 'Skill: create-workflow' in model.prompts[0]
    assert 'Platform Task Mode' in model.prompts[0]
    assert 'Return schema: {"design_brief":"markdown"}' in model.prompts[0]


def test_workflow_repair_applies_unique_str_replace_edit(monkeypatch):
    payload = {
        'state_yaml': '',
        'edits': [{
            'file': 'scenario/state.yml',
            'old': 'outputs: []',
            'new': 'outputs: [summary]',
        }],
        'remaining_warnings': [],
    }
    model = _JSONModel(payload)
    monkeypatch.setattr(llm_task, 'inject_model_config', lambda _config: None)
    monkeypatch.setattr(llm_task, 'AutoModel', lambda **_kwargs: model)

    result = run_llm_task(LLMTaskRequest(
        mode='agent',
        task_type='workflow.repair',
        skills=['create-workflow'],
        tools=['str_replace'],
        input=LLMTaskInput(
            data={'target': 'statemachine'},
            files=[LLMTaskFile(path='scenario/state.yml', content='steps:\n  a: {outputs: []}\n')],
        ),
    ))

    assert result.status == 'succeeded'
    assert result.output['state_yaml'] == 'steps:\n  a: {outputs: [summary]}\n'
    assert result.files[0].path == 'scenario/state.yml'


@pytest.mark.parametrize('code,status,retryable,expected', [
    (ModelFailureCode.REQUEST_TIMEOUT, 408, True, 'request_timeout'),
    (ModelFailureCode.RATE_LIMITED, 429, True, 'rate_limited'),
    (ModelFailureCode.SERVICE_UNAVAILABLE, 503, True, 'service_unavailable'),
    (ModelFailureCode.AUTHENTICATION_FAILED, 401, False, 'authentication_failed'),
    (ModelFailureCode.INVALID_REQUEST, 400, False, 'invalid_request'),
])
def test_provider_failure_classification(code, status, retryable, expected):
    error = ModelCallError('provider failed', ModelCallTerminal(
        'call', 1, 'failed', False,
        failure=ModelFailure(ModelFailureOrigin.HTTP, code, provider_http_status=status)))
    classified = llm_task._task_call_error(error)
    assert classified.code == expected
    assert classified.retryable == retryable
    assert classified.calls == 1


def test_length_finish_is_failure_even_when_partial_json_looks_valid():
    error = ModelCallError('length', ModelCallTerminal('call', 1, 'incomplete', True, finish=ModelFinish.LENGTH))
    assert llm_task._task_call_error(error).code == 'output_too_large'
    error = ModelCallError('capacity', ModelCallTerminal(
        'call', 1, 'failed', False,
        failure=ModelFailure(ModelFailureOrigin.HTTP, ModelFailureCode.TOKEN_LIMIT,
                             provider_error_code='context_length_exceeded', provider_http_status=400)))
    assert llm_task._task_call_error(error).code == 'token_limit'


@pytest.mark.parametrize('json_output', [False, True])
def test_general_task_uses_common_model_call(monkeypatch, json_output):
    calls = []

    def model(prompt, **options):
        calls.append(options)
        return '{"answer":"ok"}' if json_output else 'ok'

    monkeypatch.setattr(llm_task, 'AutoModel', lambda **_: model)
    result = run_llm_task(LLMTaskRequest(response_format={'type': 'json_object'} if json_output else {}))
    assert result.status == 'succeeded'
    assert result.output == ({'answer': 'ok'} if json_output else {})
    assert len(calls) == 1
    assert calls[0]['stream_output'] is True
    assert calls[0]['timeout'] == 600


def test_workflow_invalid_json_still_retries_and_repairs(monkeypatch):
    responses = iter(['not JSON', "```json\n{'answer': 'ok'}\n```"])
    calls = []

    def model(prompt, **options):
        calls.append(prompt)
        return next(responses)

    monkeypatch.setattr(llm_task, 'AutoModel', lambda **_: model)
    result = run_llm_task(LLMTaskRequest(task_type='workflow.design_brief'))
    assert result.status == 'succeeded'
    assert result.output == {'answer': 'ok'}
    assert len(calls) == 2


def test_model_configuration_failure_does_not_count_as_a_model_call(monkeypatch):
    def create_model(**_):
        raise ValueError('model is not configured')

    monkeypatch.setattr(llm_task, 'AutoModel', create_model)
    monkeypatch.setattr(llm_task, 'get_model_role_runtime_identity', lambda _: {})
    result = run_llm_task(LLMTaskRequest(task_type='conversation.describe_opening', input={'text': '解释倒排索引'}))
    assert result.status == 'failed'
    assert result.error_code == 'model_configuration'
    assert not result.retryable
    assert result.usage['model_calls'] == 0
