"""Ordinary task recovery through the real API, Executor and external HTTP boundary."""
import asyncio
import copy
import json
import threading
from contextlib import ExitStack
from types import SimpleNamespace

import httpx
import lazyllm
import pytest
from fastapi import FastAPI

from fallback_test_support import ScriptedProvider, EFFECT_RECEIPTS, record_fallback_effect
from fallback_test_support import broken_output
from lazymind.chat.api.subagent_routes import router
from lazymind.chat.engine.subagent import runner
from lazymind.config import config


def message(content):
    return {'message': {'role': 'assistant', 'content': content}}


def failure():
    return {'error': 'model_not_found', 'status': 404, 'type': 'invalid_request_error'}


@pytest.fixture
def ordinary_runtime(monkeypatch):
    provider = ScriptedProvider()
    stores = []
    original_store = runner.MemorySubAgentStore

    def store(*args):
        value = original_store(*args)
        stores.append(value)
        return value

    monkeypatch.setattr(runner, 'MemorySubAgentStore', store)
    monkeypatch.setattr(runner, '_resolve_runtime_tools', lambda *_, **_kwargs: [record_fallback_effect])
    monkeypatch.setattr(runner, '_build_subagent_tools', lambda tools, *_args, **_kwargs: tools)
    monkeypatch.setattr(runner, 'AutoModel', lambda **_: lazyllm.OnlineChatModule(
        source='openai', model='model-a', type='llm', url=provider.url,
        api_key='fake-fallback-key', stream=False,
    ))
    EFFECT_RECEIPTS.clear()
    with ExitStack() as stack:
        for key, value in {'trusted_local_mode': False, 'context_compression_enabled': False}.items():
            stack.enter_context(config.temp(key, value))
        yield provider, stores
    provider.close()


def candidate(provider, model):
    return {'candidate_id': 'own:' + model, 'source': 'own', 'model_id': model,
            'provider_group_id': model, 'credential_revision': 1, 'max_input_tokens': 32768,
            'llm_config': provider.config(model)}


async def execute(provider, tmp_path, *, marker='once', models=('model-a', 'model-b', 'model-c'),
                  execution='execution-1', params=None):
    app = FastAPI()
    app.include_router(router)
    task = {'id': 'task-' + execution, 'execution_id': execution,
            'conversation_id': 'conversation-' + execution, 'create_user_id': 'owner-' + execution,
            'agent_type': 'research', 'objective': 'Analyze ' + marker,
            'params': params or {}, 'workspace_path': str(tmp_path / execution),
            'input_slots': [], 'output_slots': [], 'mode': 'auto'}
    async with httpx.AsyncClient(transport=httpx.ASGITransport(app=app), base_url='http://test') as client:
        response = await client.post('/api/subagent/run', json={
            'task_id': task['id'], 'task_spec': task,
            'llm_config': provider.config(models[0]),
            'model_fallback_current_id': 'own:' + models[0],
            'model_fallback_candidates': [candidate(provider, model) for model in models],
        })
    assert response.status_code == 200
    events = [json.loads(line[6:]) for line in response.text.splitlines()
              if line.startswith('data: ') and line != 'data: [DONE]']
    assert response.text.endswith('data: [DONE]\n\n')
    return events, task


def scenario(provider, *, evaluation_failure=False, partial=False, marker='once', target='model-b',
             empty_tool_content=False):
    calls = []
    counts = {}

    def respond(request):
        body = json.dumps(request['messages'], ensure_ascii=False)
        if 'Create 3 to 5 short user-facing milestones' in body:
            phase = 'plan'
            result = message('["Read inputs", "Analyze evidence", "Deliver findings"]')
        elif 'Review whether an autonomous SubAgent completed' in body:
            phase = 'evaluation'
            result = failure() if evaluation_failure else message(json.dumps({
                'completed': True, 'requires_artifact': False, 'artifact_keys': [],
                'reason': 'Analysis delivered for ' + marker,
            }))
        else:
            phase = 'main'
            model = request['model']
            counts[model] = counts.get(model, 0) + 1
            if model == 'model-a' and counts[model] == 1:
                result = {'message': {'role': 'assistant',
                          'content': '' if empty_tool_content else 'COMMITTED ' + marker,
                          'tool_calls': [{'id': 'effect-' + marker, 'type': 'function',
                                          'function': {'name': 'record_fallback_effect',
                                                       'arguments': json.dumps({'value': marker})}}]},
                          'finish': 'tool_calls'}
            elif model == 'model-a':
                result = broken_output() if partial else failure()
            else:
                assert model == target
                result = message('Completed analysis ' + marker)
        calls.append((phase, copy.deepcopy(request)))
        return result

    provider.on_request = respond
    return calls


@pytest.mark.parametrize('evaluation_failure', [False, True])
def test_ordinary_fallback_uses_committed_raw_for_evaluation_without_replaying_tools(
    ordinary_runtime, tmp_path, evaluation_failure,
):
    provider, stores = ordinary_runtime
    calls = scenario(provider, evaluation_failure=evaluation_failure)
    events, task = asyncio.run(execute(provider, tmp_path))
    terminal = [event for event in events if event['type'] in {'done', 'error'}]
    assert len(terminal) == 1
    assert terminal[0]['status'] == ('failed' if evaluation_failure else 'succeeded')
    if evaluation_failure:
        assert terminal[0]['current_phase'] == 'completion_evaluation_failed'
    assert EFFECT_RECEIPTS == ['once']
    assert [request['model'] for phase, request in calls if phase == 'main'] == [
        'model-a', 'model-a', 'model-b',
    ]
    reviews = [request for phase, request in calls if phase == 'evaluation']
    assert len(reviews) == 1 and reviews[0]['model'] == 'model-b'
    assert not reviews[0].get('tools')
    assert 'You are an autonomous SubAgent' not in json.dumps(reviews[0]['messages'])
    assert not [request for _phase, request in calls if request['model'] == 'model-c']
    switches = [event for event in events if event.get('runtime_event')]
    assert len(switches) == 1 and switches[0]['runtime_event']['type'] == 'model_switched'
    assert switches[0]['model_output']['phase'] == 'commit'
    assert all(event['model_output']['run_id'] == task['execution_id']
               and event['execution_id'] == task['execution_id']
               for event in events if event.get('model_output'))
    assert len([step for step in stores[0].load_steps(task['id']) if step['role'] == 'tool']) == 1
    for _phase, request in calls:
        body = json.dumps(request['messages'])
        assert 'model_fallback_candidates' not in body and 'fake-fallback-key' not in body


def test_fallback_completion_preserves_runtime_requirements(ordinary_runtime, tmp_path):
    provider, _stores = ordinary_runtime
    calls = scenario(provider)
    events, _task = asyncio.run(execute(provider, tmp_path, params={
        'launch_user_input': 'Write 30-45 Chinese characters using sea breeze, shells and lighthouse.',
        'runtime_instruction': 'Preserve the requested length after approval.',
    }))
    assert [event['status'] for event in events if event['type'] in {'done', 'error'}] == ['succeeded']
    reviews = [request for phase, request in calls if phase == 'evaluation']
    assert len(reviews) == 1 and reviews[0]['model'] == 'model-b'
    objective = json.dumps(reviews[0]['messages'])
    assert '30-45 Chinese characters' in objective
    assert 'Preserve the requested length after approval.' in objective
    assert EFFECT_RECEIPTS == ['once']


def test_ordinary_partial_failure_retracts_only_failed_call(ordinary_runtime, tmp_path):
    provider, stores = ordinary_runtime
    calls = scenario(provider, partial=True)
    events, task = asyncio.run(execute(provider, tmp_path))
    assert next(event for event in events if event['type'] in {'done', 'error'})['status'] == 'succeeded'
    boundaries = [(event['model_output']['call_index'], event['model_output'].get('phase'))
                  for event in events if (event.get('model_output') or {}).get('phase')]
    assert boundaries == [(1, 'begin'), (1, 'commit'), (2, 'begin'), (2, 'rollback'),
                          (3, 'begin'), (3, 'commit')]
    assert any('BAD-' in event.get('text', '') + event.get('think', '') for event in events)
    assert 'BAD-' not in json.dumps(stores[0].load_steps(task['id']))
    assert EFFECT_RECEIPTS == ['once']
    assert [request['model'] for phase, request in calls if phase == 'evaluation'] == ['model-b']


def test_background_plan_failure_keeps_starting_raw_and_does_not_consume_candidates(
    ordinary_runtime, tmp_path,
):
    provider, _stores = ordinary_runtime
    calls = scenario(provider, empty_tool_content=True)
    original = provider.on_request
    plan_started, release_plan, plan_failed = threading.Event(), threading.Event(), threading.Event()

    def respond(request):
        body = json.dumps(request['messages'])
        if 'Create 3 to 5 short user-facing milestones' in body:
            original(request)
            plan_started.set()
            assert release_plan.wait(3)
            plan_failed.set()
            return failure()
        assert plan_started.wait(3)
        if request['model'] == 'model-b':
            release_plan.set()
        return original(request)

    provider.on_request = respond
    try:
        events, _task = asyncio.run(execute(provider, tmp_path))
    finally:
        release_plan.set()
    assert plan_failed.wait(3)
    assert [request['model'] for phase, request in calls if phase == 'plan'] == ['model-a']
    assert [request['model'] for phase, request in calls if phase == 'evaluation'] == ['model-b']
    assert next(event for event in events if event['type'] in {'done', 'error'})['status'] == 'succeeded'
    assert len([event for event in events if event.get('runtime_event')]) == 1
    assert any(event.get('model_output') == {'run_id': 'execution-1', 'call_index': 1, 'phase': 'commit'}
               for event in events)
    assert not [event for event in events if event['type'] == 'plan']
    assert EFFECT_RECEIPTS == ['once']


def test_two_ordinary_executions_keep_inputs_candidates_and_outputs_separate(ordinary_runtime, tmp_path):
    provider, stores = ordinary_runtime
    first = scenario(provider, marker='first-isolated', target='model-b')
    respond_first = provider.on_request
    second = scenario(provider, marker='second-isolated', target='model-c')
    respond_second = provider.on_request

    def respond(request):
        body = json.dumps(request['messages'])
        assert not ('first-isolated' in body and 'second-isolated' in body)
        return respond_first(request) if 'first-isolated' in body else respond_second(request)

    provider.on_request = respond

    async def drive():
        return await asyncio.gather(
            execute(provider, tmp_path, marker='first-isolated', models=('model-a', 'model-b'), execution='first'),
            execute(provider, tmp_path, marker='second-isolated', models=('model-a', 'model-c'), execution='second'),
        )

    results = asyncio.run(drive())
    assert sorted(EFFECT_RECEIPTS) == ['first-isolated', 'second-isolated']
    for (events, task), marker, target, calls in zip(
        results, ['first-isolated', 'second-isolated'], ['model-b', 'model-c'], [first, second],
    ):
        terminal = next(event for event in events if event['type'] in {'done', 'error'})
        assert terminal['status'] == 'succeeded' and marker in terminal['summary']
        assert [request['model'] for phase, request in calls if phase == 'main'] == ['model-a', 'model-a', target]
        assert [request['model'] for phase, request in calls if phase == 'evaluation'] == [target]
        assert all(event['model_output']['run_id'] == task['execution_id']
                   for event in events if event.get('model_output'))
    assert len(stores) == 2


@pytest.mark.parametrize('cancel', [False, True])
def test_local_failure_closes_open_task_call_before_original_terminal(
    ordinary_runtime, tmp_path, monkeypatch, cancel,
):
    provider, stores = ordinary_runtime
    monkeypatch.setattr(runner, '_generate_display_plan', lambda *_args: [])

    class Executor:
        def create_agent(self, _llm, _plan):
            return SimpleNamespace()

        async def stream_agent(self, _agent, _plan):
            yield 'event', {'tag': 'model_output', 'model_output': {'call_index': 1, 'phase': 'begin'}}
            yield 'event', {'tag': 'text', 'delta': 'UNCOMMITTED ' * 50,
                            'model_output': {'call_index': 1}}
            raise runner.UserCancelledError('stopped by user') if cancel else RuntimeError('local failure')

    monkeypatch.setattr(runner, 'AgentExecutor', Executor)
    events, task = asyncio.run(execute(provider, tmp_path))
    rollback = next(index for index, event in enumerate(events)
                    if (event.get('model_output') or {}).get('phase') == 'rollback')
    terminal = next(index for index, event in enumerate(events) if event['type'] in {'done', 'error'})
    assert rollback < terminal
    assert events[terminal]['status'] == ('interrupted' if cancel else 'failed')
    assert not events[terminal].get('model_output')
    assert all((event.get('runtime_event') or {}).get('type') != 'run_finished' for event in events)
    assert 'UNCOMMITTED' not in json.dumps(stores[0].load_steps(task['id']))


def test_public_model_records_are_not_replayed_as_private_agent_history():
    store = runner.MemorySubAgentStore({'id': 'task'}, [
        {'seq': 0, 'role': 'model_runtime', 'content': {'model_output': {
            'run_id': 'previous', 'call_index': 1, 'phase': 'commit'}, 'current_model': {'model_id': 'B'}}},
        {'seq': 1, 'role': 'process_step', 'content': {'step': {'title': '模型自动切换'}}},
        {'seq': 2, 'role': 'assistant', 'content': {'tool_calls': [{
            'id': 'completed', 'name': 'read_file', 'args': {'path': 'prior.txt'}}]}},
        {'seq': 3, 'role': 'tool', 'content': {'tool_results': [{
            'tool_call_id': 'completed', 'name': 'read_file', 'result': 'prior evidence'}]}},
    ])
    history = runner._rebuild_history_from_steps(store, 'task')
    assert [item['role'] for item in history] == ['assistant', 'tool']
    assert history[1]['content'] == 'prior evidence'
    assert 'current_model' not in json.dumps(history)


def test_ordinary_exhaustion_preserves_one_task_terminal_and_stable_phase(ordinary_runtime, tmp_path):
    provider, stores = ordinary_runtime
    calls = scenario(provider)
    original = provider.on_request

    def respond(request):
        return failure() if request['model'] != 'model-a' else original(request)

    provider.on_request = respond
    events, task = asyncio.run(execute(provider, tmp_path))
    terminal = [event for event in events if event['type'] in {'done', 'error'}]
    assert len(terminal) == 1 and terminal[0]['status'] == 'failed'
    assert terminal[0]['current_phase'] == 'model_fallback_exhausted'
    assert not [request for phase, request in calls if phase == 'evaluation']
    assert [request['model'] for request in provider.requests
            if 'Create 3 to 5 short user-facing milestones' not in json.dumps(request['messages'])] == [
                'model-a', 'model-a', 'model-b', 'model-c',
            ]
    assert EFFECT_RECEIPTS == ['once']
    assert len([step for step in stores[0].load_steps(task['id']) if step['role'] == 'tool']) == 1
    assert not [event for event in events if event.get('runtime_event')]
