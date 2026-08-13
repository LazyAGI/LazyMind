from __future__ import annotations

import json

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
