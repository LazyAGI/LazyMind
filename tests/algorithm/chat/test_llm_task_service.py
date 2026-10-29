from __future__ import annotations

import json
from concurrent.futures import ThreadPoolExecutor

import pytest

from lazymind.chat.service import llm_task
from lazymind.chat.service.llm_task import LLMTaskFile, LLMTaskInput, LLMTaskRequest, run_llm_task


def test_workflow_model_routing_uses_request_llm_and_isolates_users(monkeypatch):
    import lazyllm
    from lazyllm.module.llms.onlinemodule.chat import OnlineChatModule

    class Supplier:
        def forward(self, prompt, **options):
            return json.dumps({
                'model': options['model'], 'url': options['url'],
                'key': lazyllm.globals.config['openai_api_key'],
            })

    def build_supplier(self, source, skip_auth):
        assert source == 'openai'
        return Supplier()

    monkeypatch.setattr(OnlineChatModule, '_build_supplier', build_supplier)

    def run(user):
        return run_llm_task(LLMTaskRequest(
            task_type='workflow.design_brief',
            llm_config={
                'vlm': {'source': 'siliconflow', 'model': 'other-role', 'api_key': 'other-key'},
                'llm': {'source': 'openai', 'model': f'Qwen/user-{user}',
                        'base_url': f'https://user-{user}.example/v1/', 'api_key': f'test-key-{user}'},
            },
        ))

    with ThreadPoolExecutor(max_workers=2) as pool:
        results = list(pool.map(run, [1, 2]))
    for user, result in enumerate(results, 1):
        assert result.status == 'succeeded', result.error
        assert result.output == {
            'model': f'Qwen/user-{user}', 'url': f'https://user-{user}.example/v1/',
            'key': f'test-key-{user}',
        }


class _JSONModel:
    def __init__(self, payload):
        self.payload = payload
        self.prompts = []

    def __call__(self, prompt, **kwargs):
        self.prompts.append(prompt)
        return json.dumps(self.payload)


@pytest.mark.parametrize('target', ['node_integration', 'statemachine', 'full', 'ui', 'scenario', 'scripts'])
@pytest.mark.parametrize('code', ['transport_error', 'request_timeout', 'authentication_failed', 'rate_limited'])
def test_repair_provider_failure_is_not_retried_as_invalid_json(monkeypatch, target, code):
    from lazyllm.module.llms.onlinemodule.base.model_outcome import (
        ModelCallError, ModelCallTerminal, ModelFailure, ModelFailureCode, ModelFailureOrigin,
    )

    calls = []

    def model(prompt, **options):
        calls.append(prompt)
        raise ModelCallError('private provider response with secret-key', ModelCallTerminal(
            model_call_id='test-call', attempt_count=1, kind='failure', has_semantic_output=False,
            failure=ModelFailure(origin=ModelFailureOrigin.TRANSPORT, code=ModelFailureCode(code),
                                 diagnostic_id='diag-test', response_started=True),
        ))

    monkeypatch.setattr(llm_task, 'AutoModel', lambda **kwargs: model)
    result = run_llm_task(LLMTaskRequest(task_type='workflow.repair', input=LLMTaskInput(data={'target': target})))
    assert result.status == 'failed'
    assert result.error_code == code
    assert result.diagnostic_id == 'diag-test'
    assert result.retryable is (code != 'authentication_failed')
    assert len(calls) == 1
    assert 'secret-key' not in result.model_dump_json()


def test_repair_json_retries_share_remaining_budget(monkeypatch):
    clock = [100.0]
    calls = []

    def model(prompt, **options):
        calls.append(options)
        if len(calls) == 1:
            clock[0] += 40
            return '[]'
        return '{"remaining_warnings": []}'

    monkeypatch.setattr(llm_task.time, 'monotonic', lambda: clock[0])
    monkeypatch.setattr(llm_task, 'AutoModel', lambda **kwargs: model)
    result = run_llm_task(LLMTaskRequest(task_type='workflow.repair', options={'total_timeout_seconds': 60}))
    assert result.status == 'succeeded'
    assert [call['timeout'] for call in calls] == [60, 20]
    assert all(call['max_retries'] == 1 for call in calls)


def test_repair_fifteen_minute_budget_is_not_capped_by_general_model_timeout(monkeypatch):
    clock = [100.0]
    calls = []

    def model(prompt, **options):
        calls.append(options)
        if len(calls) == 1:
            clock[0] += 650
            return '[]'
        return '{"remaining_warnings": []}'

    monkeypatch.setattr(llm_task.time, 'monotonic', lambda: clock[0])
    monkeypatch.setattr(llm_task, 'AutoModel', lambda **kwargs: model)
    result = run_llm_task(LLMTaskRequest(task_type='workflow.repair'))
    assert result.status == 'succeeded'
    assert [call['timeout'] for call in calls] == [870, 220]
    assert all(call['max_retries'] == 1 for call in calls)


@pytest.mark.parametrize('streaming', [False, True])
def test_repair_budget_exhaustion_stops_before_another_model_attempt(monkeypatch, streaming):
    clock = [100.0]
    calls = []

    def model(prompt, **options):
        calls.append(options)
        clock[0] += 61
        if streaming:
            options['call_control'].check()
        return '{"remaining_warnings": []}'

    monkeypatch.setattr(llm_task.time, 'monotonic', lambda: clock[0])
    monkeypatch.setattr(llm_task, 'AutoModel', lambda **kwargs: model)
    result = run_llm_task(LLMTaskRequest(task_type='workflow.repair', options={'total_timeout_seconds': 60}))
    assert result.status == 'failed'
    assert result.error_code == 'request_timeout'
    assert result.retryable
    assert len(calls) == 1


@pytest.mark.parametrize('budget', [0, -1, 'invalid', float('nan'), float('inf')])
def test_repair_rejects_invalid_budget_without_calling_model(monkeypatch, budget):
    calls = []
    monkeypatch.setattr(llm_task, 'AutoModel', lambda **kwargs: calls.append(kwargs))
    result = run_llm_task(LLMTaskRequest(task_type='workflow.repair', options={'total_timeout_seconds': budget}))
    assert result.status == 'failed'
    assert result.error_code == 'invalid_task_config'
    assert calls == []


@pytest.mark.parametrize('scenario,expected', [
    ({'error': 'invalid_api_key', 'status': 401}, 'authentication_failed'),
    ({'error': 'request_timeout', 'status': 408}, 'request_timeout'),
    ({'message': {'role': 'assistant', 'content': ''}, 'disconnect': True,
      'frames': [{'choices': [{'index': 0, 'delta': {'content': '{"remaining'}, 'finish_reason': None}]}]},
     'transport_error'),
    # A complete semantic terminal is still valid if the connection closes afterwards.
    ({'message': {'role': 'assistant', 'content': '{"remaining_warnings": []}'}, 'disconnect': True}, None),
    ({'message': {'role': 'assistant', 'content': '{"remaining_warnings": []}'}}, None),
])
def test_repair_uses_real_provider_transport_and_does_not_replay_failed_calls(scenario, expected):
    from fallback_test_support import ScriptedProvider

    provider = ScriptedProvider()
    try:
        provider.script['repair-test'] = [scenario]
        result = run_llm_task(LLMTaskRequest(task_type='workflow.repair', llm_config=provider.config('repair-test')))
        assert len(provider.requests) == 1
        assert result.error_code == expected
        assert result.status == ('failed' if expected else 'succeeded')
        if expected:
            assert result.diagnostic_id
        else:
            assert result.output['remaining_warnings'] == []
    finally:
        provider.close()


@pytest.mark.parametrize('configured', [{}, {'temperature': None}, {'temperature': 0}, {'temperature': 1}])
def test_temperature_is_only_sent_when_explicitly_configured(monkeypatch, configured):
    calls = []

    def model(prompt, **options):
        calls.append(options)
        return '{"targets":[]}'

    monkeypatch.setattr(llm_task, 'AutoModel', lambda **kwargs: model)
    llm_task._call_model(LLMTaskRequest(options=configured), 'organize references')
    if configured.get('temperature') is None:
        assert 'temperature' not in calls[0]
    else:
        assert calls[0]['temperature'] == configured['temperature']


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


@pytest.mark.parametrize('target,preserve', [('node_integration', False), ('statemachine', True), ('full', True)])
def test_node_integration_repair_preserves_intent_and_reports_missing_information(monkeypatch, target, preserve):
    model = _JSONModel({'needs_input': 'Where should rejected reviews go?', 'edits': []})
    monkeypatch.setattr(llm_task, 'inject_model_config', lambda _config: None)
    monkeypatch.setattr(llm_task, 'AutoModel', lambda **_kwargs: model)
    result = run_llm_task(LLMTaskRequest(
        task_type='workflow.repair',
        input=LLMTaskInput(data={
            'target': target, 'preserve_current_workflow': preserve, 'edit_context': {'node_ids': ['review']},
            'repair_hint': 'Review before export',
        }),
    ))
    assert result.status == 'succeeded'
    assert result.output['needs_input'] == 'Where should rejected reviews go?'
    assert 'current saved files and user edit intent take precedence' in model.prompts[0]
    assert 'Never delete or skip a node' in model.prompts[0]
    assert 'Prefer precise local edits against the supplied input.files' in model.prompts[0]
    assert 'branch conditions VERBATIM' in model.prompts[0]
    assert 'exactly one unconditional successor' in model.prompts[0]
    assert 'Only edit_context.input_corrections explicitly authorizes' in model.prompts[0]
    assert 'Return final full content for changed files only' not in model.prompts[0]
    assert 'Review before export' in model.prompts[0]
    if target == 'full':
        assert 'Scope: full repair. Inspect ALL current workflow files' in model.prompts[0]
        assert 'not only problems involving newly added nodes' in model.prompts[0]
        assert 'Scope: execution flow repair' not in model.prompts[0]
    else:
        assert 'Scope: execution flow repair' in model.prompts[0]
        assert 'scripts ONLY where needed by these flow changes' in model.prompts[0]
        assert 'Scope: full repair' not in model.prompts[0]


@pytest.mark.parametrize('target,preserve', [('node_integration', False), ('statemachine', True), ('full', True)])
@pytest.mark.parametrize('path,content,invalid', [
    ('scripts/review.py', 'def run():\n    return True\n', False),
    ('scripts/review.py', 'raise AssertionError("must not execute")', False),
    ('scripts/review.py', 'def run(:\n', True),
    ('scripts/../review.py', 'print(1)', True),
])
def test_node_integration_checks_scripts_without_running_them(monkeypatch, path, content, invalid, target, preserve):
    model = _JSONModel({'scripts': {path: content}})
    monkeypatch.setattr(llm_task, 'inject_model_config', lambda _config: None)
    monkeypatch.setattr(llm_task, 'AutoModel', lambda **_kwargs: model)
    result = run_llm_task(LLMTaskRequest(
        task_type='workflow.repair', input=LLMTaskInput(data={'target': target, 'preserve_current_workflow': preserve}),
    ))
    assert result.status == 'succeeded'
    assert bool(result.output['script_errors']) is invalid
    if invalid:
        assert result.output['script_errors'][0]['path'] == path


@pytest.mark.parametrize('target', ['ui', 'scenario', 'scripts', 'statemachine', 'full'])
def test_other_repair_scopes_do_not_implicitly_enable_current_graph_policy(monkeypatch, target):
    model = _JSONModel({'remaining_warnings': []})
    monkeypatch.setattr(llm_task, 'inject_model_config', lambda _config: None)
    monkeypatch.setattr(llm_task, 'AutoModel', lambda **_kwargs: model)
    result = run_llm_task(LLMTaskRequest(
        task_type='workflow.repair', input=LLMTaskInput(data={'target': target}),
    ))
    assert result.status == 'succeeded'
    assert 'Scope: full repair' not in model.prompts[0]
    assert 'Scope: execution flow repair' not in model.prompts[0]
    assert 'script_errors' not in result.output


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


@pytest.mark.parametrize('source,full,old', [
    ('outputs: []', 'outputs: []', 'outputs: []'),
    ('outputs: []', '', 'not present'),
    ('outputs: []\noutputs: []', '', 'outputs: []'),
])
def test_workflow_repair_rejects_conflicting_or_unapplied_edits(monkeypatch, source, full, old):
    model = _JSONModel({'state_yaml': full, 'edits': [
        {'file': 'scenario/state.yml', 'old': old, 'new': 'outputs: [review_result]'},
    ]})
    monkeypatch.setattr(llm_task, 'AutoModel', lambda **_: model)
    result = run_llm_task(LLMTaskRequest(
        task_type='workflow.repair', input=LLMTaskInput(
            files=[LLMTaskFile(path='scenario/state.yml', content=source)],
        ),
    ))
    assert result.status == 'failed'
    assert not result.files
    assert len(model.prompts) == 2
    assert 'conflicting' in result.error or 'match exactly once' in result.error


def test_workflow_repair_retries_edit_contract_then_returns_connected_output(monkeypatch):
    source = 'steps:\n  review:\n    outputs: []\n'
    fixed = 'steps:\n  review:\n    outputs: [{material: review_result}]\n'
    responses = iter([
        {'edits': [{'file': 'scenario/state.yml', 'old': 'missing', 'new': 'replacement'}]},
        {'state_yaml': fixed},
    ])
    prompts = []

    def model(prompt, **kwargs):
        prompts.append(prompt)
        return json.dumps(next(responses))

    monkeypatch.setattr(llm_task, 'AutoModel', lambda **_: model)
    result = run_llm_task(LLMTaskRequest(
        task_type='workflow.repair', skills=['create-workflow'], input=LLMTaskInput(
            data={'target': 'node_integration'},
            files=[LLMTaskFile(path='scenario/state.yml', content=source)],
        ),
    ))
    assert result.status == 'succeeded'
    assert result.output['state_yaml'] == fixed
    assert result.files[0].content == fixed
    assert 'match exactly once' in prompts[1]
    assert 'outputs[].material' in prompts[0]
    assert "A source Skill's tools are NOT automatically available" in prompts[0]


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
