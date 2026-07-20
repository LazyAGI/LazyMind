from __future__ import annotations

import asyncio
from unittest.mock import MagicMock

from lazymind.chat.engine.agent_runtime import (
    AgentExecutionOptions,
    AgentExecutor,
    AgentRole,
    AgentRunPlan,
    PromptBuilder,
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
    constructor = MagicMock(return_value=agent)
    monkeypatch.setattr(executor_mod._agent_mod, 'ReactAgent', constructor)

    created = AgentExecutor().create_agent('llm', _plan(workspace='/tmp/work'))

    assert created is agent
    kwargs = constructor.call_args.kwargs
    assert kwargs['stream'] is True
    assert kwargs['force_summarize'] is True
    assert kwargs['enable_builtin_tools'] is False
    assert kwargs['workspace'] == '/tmp/work'
    agent._prepare_tool_context.assert_called_once_with(
        '### User Instruction\n\nhello', [],
    )
    agent.set_stop_tools.assert_called_once_with(['stop'])


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
