from __future__ import annotations

import asyncio
import json
from asyncio import CancelledError
from unittest.mock import MagicMock

import pytest
from lazyllm.tools.agent import ToolManager
from lazymind.chat.engine.agent_runtime import (
    AgentExecutionOptions,
    AgentExecutor,
    AgentRole,
    AgentRunPlan,
    PromptBuilder,
    make_cancel_stop_condition,
)
from lazymind.chat.engine.agent_runtime import executor as executor_mod


def _plan(**options) -> AgentRunPlan:
    prompt = PromptBuilder.for_role(AgentRole.CHAT).input('hello', source='user').build()
    return AgentRunPlan(
        role=AgentRole.CHAT,
        prompt=prompt,
        tools=[],
        stop_tools=['stop'],
        execution_options=AgentExecutionOptions(**options),
    )


def test_executor_creates_agent_with_shared_defaults(monkeypatch) -> None:
    agent = MagicMock()
    original_manager = agent._tools_manager
    constructor = MagicMock(return_value=agent)
    monkeypatch.setattr(executor_mod._agent_mod, 'ReactAgent', constructor)

    created = AgentExecutor().create_agent('llm', _plan(workspace='/tmp/work'))

    assert created is agent
    kwargs = constructor.call_args.kwargs
    assert kwargs['stream'] is True
    assert kwargs['force_summarize'] is True
    assert kwargs['enable_builtin_tools'] is False
    assert callable(kwargs['on_max_retries'])
    assert kwargs['workspace'] == '/tmp/work'
    assert len(kwargs['runtime_extensions']) == 1
    assert isinstance(kwargs['runtime_extensions'][0], executor_mod.ExactRepeatMonitor)
    assert isinstance(agent._tools_manager, executor_mod.ToolExecutionMiddleware)
    assert agent._tools_manager._manager._manager is original_manager
    agent._prepare_tool_context.assert_called_once_with(
        '### User Instruction\n\nhello', [],
    )
    agent.set_stop_tools.assert_called_once_with(['stop'])


def test_executor_passes_cancel_condition_to_chat_agent(monkeypatch) -> None:
    agent = MagicMock()
    constructor = MagicMock(return_value=agent)
    monkeypatch.setattr(executor_mod._agent_mod, 'ReactAgent', constructor)
    stop_condition = make_cancel_stop_condition()

    AgentExecutor().create_agent('llm', _plan(extra_stop_condition=stop_condition))

    assert constructor.call_args.kwargs['extra_stop_condition'] is stop_condition


def test_tool_guard_checks_cancellation_before_dispatch(monkeypatch) -> None:
    manager = MagicMock()
    cancel_check = MagicMock(side_effect=CancelledError('stopped by user'))
    middleware = executor_mod.ToolExecutionMiddleware(manager, cancel_check=cancel_check)

    with pytest.raises(CancelledError, match='stopped by user'):
        middleware.execute_with_records([])

    manager.execute_prepared_calls.assert_not_called()


def test_cancel_condition_stops_the_sid_scoped_agent(monkeypatch) -> None:
    queue = MagicMock()
    queue.dequeue.return_value = [json.dumps({'tag': 'cancel'})]
    monkeypatch.setattr('lazyllm.common.queue.FileSystemQueue', lambda **_kwargs: queue)

    with pytest.raises(CancelledError, match='stopped by user'):
        make_cancel_stop_condition()(None)


def test_executor_does_not_pause_subagent_on_round_limit(monkeypatch) -> None:
    agent = MagicMock()
    constructor = MagicMock(return_value=agent)
    monkeypatch.setattr(executor_mod._agent_mod, 'ReactAgent', constructor)
    plan = _plan()
    plan.role = AgentRole.SUBAGENT

    AgentExecutor().create_agent('llm', plan)

    assert constructor.call_args.kwargs['on_max_retries'] is None


def test_executor_enables_builtin_tools_in_trusted_local_mode(monkeypatch) -> None:
    agent = MagicMock()
    constructor = MagicMock(return_value=agent)
    monkeypatch.setattr(executor_mod._agent_mod, 'ReactAgent', constructor)

    with executor_mod._cfg.temp('trusted_local_mode', True):
        AgentExecutor().create_agent('llm', _plan())

    assert constructor.call_args.kwargs['enable_builtin_tools'] is True


def test_executor_auto_expands_after_plugin_or_subagent_tool(monkeypatch) -> None:
    agent = MagicMock()

    def create_subagent():
        '''Create a subagent.'''
        return 'created'

    manager = ToolManager([create_subagent])
    agent._tools_manager = manager
    constructor = MagicMock(return_value=agent)
    monkeypatch.setattr(executor_mod._agent_mod, 'ReactAgent', constructor)
    workspace = {}
    monkeypatch.setitem(executor_mod.lazyllm.locals, '_lazyllm_agent', {'workspace': workspace})

    created = AgentExecutor().create_agent('llm', _plan(max_retries=20))
    created._tools_manager([{
        'function': {'name': 'create_subagent', 'arguments': '{}'},
    }])

    with executor_mod._cfg.temp('agentic_expanded_max_rounds', 200):
        assert workspace['_react_round_limit'] == 200
        assert constructor.call_args.kwargs['on_max_retries'](
            None, 21, 21,
        ) == 200


def test_executor_restores_toolkit_activation_from_history(monkeypatch) -> None:
    agent = MagicMock()
    constructor = MagicMock(return_value=agent)
    monkeypatch.setattr(executor_mod._agent_mod, 'ReactAgent', constructor)
    plan = _plan()
    plan.history = [{
        'role': 'assistant',
        'tool_calls': [{
            'function': {'name': 'get_ScheduleToolkit_methods', 'arguments': '{}'},
        }],
    }]

    AgentExecutor().create_agent('llm', plan)

    agent._prepare_tool_context.assert_called_once_with(
        plan.prompt.current_input, plan.history,
    )


def test_executor_stream_passes_history_and_returns_final(monkeypatch) -> None:
    class Future:
        def result(self):
            return 'done'

    class Helper:
        future = Future()

        def __init__(self, agent, init_sid):
            pass

        async def astream(self, query, **kwargs):
            assert query == '### User Instruction\n\nhello'
            assert kwargs['llm_chat_history'][0]['content'] == 'prior'
            yield {'tag': 'text', 'delta': 'working'}

    monkeypatch.setattr(executor_mod._sh, 'StreamCallHelper', Helper)
    plan = _plan()
    plan.history = [{'role': 'user', 'content': 'prior'}]

    async def collect():
        return [item async for item in AgentExecutor().stream_agent('agent', plan)]

    assert asyncio.run(collect()) == [
        ('event', {'tag': 'text', 'delta': 'working'}),
        ('final', 'done'),
    ]
