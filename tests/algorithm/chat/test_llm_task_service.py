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
