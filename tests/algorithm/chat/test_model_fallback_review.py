import time

from lazymind.config import config
import pytest

from fallback_test_support import candidate, fail, succeed, wrapper
from lazymind.chat.engine.agent_runtime.budget import build_context_budget


@pytest.mark.parametrize('previous_window', [32768, 131072])
def test_unknown_candidate_has_independent_default_budget(provider, previous_window):
    current = candidate(provider, 'model-a', window=previous_window)
    unknown = candidate(provider, 'model-b', window=None)
    fail(provider, 'model-a')
    succeed(provider, 'model-b')
    model = wrapper(current, [unknown])
    assert model('continue') == 'done:model-b'
    assert model.state.current.max_input_tokens is None
    assert model.state.budget == build_context_budget(None)


@pytest.mark.parametrize('limit', [1, 4])
def test_candidate_limit_includes_initial_model(provider, limit):
    candidates = [candidate(provider, 'model-' + str(i)) for i in range(6)]
    for item in candidates:
        fail(provider, item.model_id)
    model = wrapper(candidates[0], candidates[1:])
    with config.temp('model_fallback_max_candidates', limit):
        with pytest.raises(Exception) as failure:
            model('continue')
    assert [request['model'] for request in provider.requests] == [item.model_id for item in candidates[:limit]]
    assert failure.value.fallback_code == 'model_fallback_exhausted'


def test_all_candidates_share_one_deadline(provider):
    candidates = [candidate(provider, 'model-' + str(i)) for i in range(5)]

    def slow_failure(request):
        time.sleep(0.08)
        return {'error': 'model_not_found', 'status': 404}

    provider.on_request = slow_failure
    model = wrapper(candidates[0], candidates[1:])
    with config.temp('model_fallback_timeout_seconds', 0.12), config.temp('model_fallback_max_candidates', 5):
        started = time.monotonic()
        with pytest.raises(Exception) as failure:
            model('continue')
        elapsed = time.monotonic() - started
    assert elapsed < 1.5
    assert 1 <= len(provider.requests) <= 2
    assert failure.value.fallback_code == 'model_fallback_exhausted'


def test_local_cancellation_check_failure_does_not_try_another_model(provider):
    from lazyllm.module.module import ModuleExecutionError

    model = wrapper(candidate(provider, 'model-a'), [candidate(provider, 'model-b')])
    fail(provider, 'model-a')
    succeed(provider, 'model-b')

    def broken_check(_):
        if provider.requests:
            raise RuntimeError('fixture control failure')
        return False

    model._cancel_check = broken_check
    with pytest.raises(ModuleExecutionError, match='cancellation check failed') as failure:
        model('continue')
    assert failure.value._model_fallback_local_failure is True
    assert [request['model'] for request in provider.requests] == ['model-a']


@pytest.mark.parametrize('cancelled', [False, True])
def test_final_summary_after_switch_uses_confirmed_model_and_keeps_candidates(provider, tmp_path, cancelled):
    import threading
    import lazyllm
    from fallback_test_support import EFFECT_RECEIPTS, record_fallback_effect
    from lazymind.chat.engine.agent_runtime import (
        AgentExecutionOptions, AgentExecutor, AgentRole, AgentRunPlan, PromptBuilder,
    )
    from lazymind.chat.engine.agent_runtime.cancellation import UserCancelledError
    from lazymind.chat.engine.tools.workspace_context import WorkspaceContext

    a, b, c = [candidate(provider, name) for name in ('model-a', 'model-b', 'model-c')]
    fail(provider, 'model-a')
    provider.script['model-b'] = [{
        'message': {'role': 'assistant', 'content': '', 'tool_calls': [{
            'id': f'step-{index}', 'index': 0, 'type': 'function',
            'function': {'name': 'record_fallback_effect', 'arguments': f'{{"value":"step-{index}"}}'},
        }]}, 'finish': 'tool_calls',
    } for index in range(2)] + [
        {'message': {'role': 'assistant', 'content': 'final summary by B'}},
    ]
    stop = threading.Event()

    def observe(request):
        if cancelled and len(provider.requests) == 4:
            stop.set()

    provider.on_request = observe
    options = AgentExecutionOptions(
        llm_config=a.llm_config, model_candidates=(a, b, c), current_model_candidate_id=a.candidate_id,
        max_retries=1, workspace=str(tmp_path), enable_builtin_tools=False, skills=False,
        workspace_permission=WorkspaceContext(local_runtime=False),
        extra_stop_condition=lambda _: stop.is_set(),
    )
    plan = AgentRunPlan(
        role=AgentRole.CHAT, prompt=PromptBuilder.for_role(AgentRole.CHAT).input('Do two steps.', source='user').build(),
        tools=[record_fallback_effect], execution_options=options)
    raw = lazyllm.OnlineChatModule(source='openai', model=a.model_id, url=provider.url, api_key='fixture-key')
    with config.temp('context_compression_enabled', False), config.temp('agentic_workspace', str(tmp_path)), \
            config.temp('agentic_tool_limit_wait_timeout', 0.0):
        agent = AgentExecutor().create_agent(raw, plan)
        if cancelled:
            with pytest.raises(UserCancelledError):
                agent(plan.prompt.current_input)
        else:
            assert agent(plan.prompt.current_input) == 'final summary by B'
    assert [request['model'] for request in provider.requests] == ['model-a', 'model-b', 'model-b', 'model-b']
    assert EFFECT_RECEIPTS == ['step-0', 'step-1']
    assert not provider.requests[-1].get('tools')
    assert 'effect-receipt' in str(provider.requests[-1]['messages'])
    assert agent._model_fallback_state.failed_candidates == {a.candidate_id}
    assert agent._model_fallback_state.current.candidate_id == b.candidate_id
