from __future__ import annotations

import asyncio
import json
import os
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
from lazymind.chat.engine.tools.workspace_context import WorkspaceContext


def test_deduplicate_tool_groups_by_name_and_keep_first_duplicate():
    from lazyllm.tools.agent.toolsManager import ToolGroup

    first = ToolGroup([], name='search', discoverable=True)
    duplicate = ToolGroup([], name='search', discoverable=True)
    notion = ToolGroup([], name='notion', discoverable=True)
    assert executor_mod._deduplicate_tools([first, duplicate, notion]) == [first, notion]
    assert executor_mod._deduplicate_tools([{'name': 'search', 'tools': []}, first, notion]) == [
        {'name': 'search', 'tools': []}, notion,
    ]


def test_deduplicate_preserves_callable_and_mcp_identity_rules():
    from lazyllm.tools.agent import fc_register

    def ordinary():
        return 'ok'

    @fc_register(tool_source='mcp', tool_identity='mcp:v1:' + 'a' * 64,
                 tool_origin='First MCP', execute_in_sandbox=False)
    def first_mcp():
        '''First MCP tool.'''
        return 'first'

    @fc_register(tool_source='mcp', tool_identity='mcp:v1:' + 'b' * 64,
                 tool_origin='Second MCP', execute_in_sandbox=False)
    def second_mcp():
        '''Second MCP tool.'''
        return 'second'

    second_mcp.__name__ = first_mcp.__name__
    assert executor_mod._deduplicate_tools([
        ordinary, ordinary, first_mcp, first_mcp, second_mcp,
    ]) == [ordinary, first_mcp, second_mcp]


def _plan(**options) -> AgentRunPlan:
    options.setdefault('workspace_permission', WorkspaceContext(local_runtime=False))
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
    assert callable(kwargs['model_context_provider'])
    assert isinstance(agent._tools_manager, executor_mod.ToolExecutionMiddleware)
    assert agent._tools_manager._manager._manager is original_manager
    assert isinstance(agent._exact_repeat_monitor, executor_mod.ExactRepeatMonitor)
    assert isinstance(agent._runtime_notice_buffer, executor_mod.OneShotNoticeBuffer)
    agent._prepare_tool_context.assert_called_once_with(
        '### User Instruction\n\nhello', [],
    )
    agent.set_stop_tools.assert_called_once_with(['stop'])


@pytest.mark.parametrize(('configured', 'expected'), [(65536, 32768), (8192, 8192)])
def test_executor_applies_the_named_models_output_limit_to_a_private_copy(monkeypatch, configured, expected):
    from lazyllm.module.llms.onlinemodule.chat import OnlineChatModule

    constructor = MagicMock(return_value=MagicMock())
    monkeypatch.setattr(executor_mod._agent_mod, 'ReactAgent', constructor)
    original = OnlineChatModule(source='dynamic', name='llm', dynamic_auth=True,
                                static_params={'max_tokens': configured})
    AgentExecutor().create_agent(original, _plan(llm_config={'llm': {'max_output_tokens': '32K'}}))
    selected = constructor.call_args.kwargs['llm']
    assert selected is not original
    assert selected._static_params['max_tokens'] == expected
    assert original._static_params['max_tokens'] == configured


def test_executor_passes_authorization_gate_to_middleware(monkeypatch) -> None:
    agent = MagicMock()
    constructor = MagicMock(return_value=agent)
    monkeypatch.setattr(executor_mod._agent_mod, 'ReactAgent', constructor)
    gate = MagicMock(return_value='allow')

    AgentExecutor().create_agent('llm', _plan(authorization_gate=gate))

    assert agent._tools_manager._authorization_gate is gate


def test_executor_trusts_only_the_framework_skill_run_script_identity(monkeypatch, tmp_path) -> None:
    from lazyllm.tools.agent import SkillManager, fc_register

    @fc_register(host_file='OPAQUE')
    def arbitrary_executable(command: str):
        '''An opaque executable that is not a SkillManager capability.

        Args:
            command: Command to execute.
        '''
        return command

    skill_manager = SkillManager(dir=str(tmp_path))
    skill_tools = skill_manager.get_skill_tools()
    manager = ToolManager([*skill_tools, arbitrary_executable])
    agent = MagicMock()
    agent._tools_manager = manager
    agent._skill_tool_names = {tool.__name__ for tool in skill_tools}
    monkeypatch.setattr(executor_mod._agent_mod, 'ReactAgent', MagicMock(return_value=agent))

    created = AgentExecutor().create_agent('llm', _plan())

    assert created._tools_manager._opaque_tool_is_trusted('run_skill_script')
    assert not created._tools_manager._opaque_tool_is_trusted('arbitrary_executable')


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

    manager.execute_with_records.assert_not_called()


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

        def __init__(self, agent, init_sid, on_cancel):
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


@pytest.mark.parametrize('mode', ['success', 'exception', 'cancellation'])
def test_stream_agent_clears_repeat_state_on_every_exit(monkeypatch, mode) -> None:
    monitor = MagicMock()
    buffer = MagicMock()

    class Agent:
        _agent_lab_run_id = 'run-id'
        _exact_repeat_monitor = monitor
        _runtime_notice_buffer = buffer

    class Future:
        def result(self):
            if mode == 'exception':
                raise RuntimeError('failed')
            return 'done'

    class Helper:
        future = Future()

        def __init__(self, _agent, init_sid, on_cancel):
            assert init_sid is False

        async def astream(self, _query, **_kwargs):
            if mode == 'cancellation':
                raise CancelledError('cancelled')
            if False:
                yield None

    monkeypatch.setattr(executor_mod._sh, 'StreamCallHelper', Helper)

    async def collect():
        return [item async for item in AgentExecutor().stream_agent(Agent(), _plan())]

    if mode == 'exception':
        with pytest.raises(RuntimeError, match='failed'):
            asyncio.run(collect())
    elif mode == 'cancellation':
        with pytest.raises(CancelledError, match='cancelled'):
            asyncio.run(collect())
    else:
        assert asyncio.run(collect()) == [('final', 'done')]
    assert monitor.reset.call_count == 2
    assert buffer.clear.call_count == 2


def test_executor_keeps_the_configured_fs_for_skill_indexing(monkeypatch, tmp_path):
    import lazyllm
    from lazyllm.tools.agent.skill_manager import SkillManager
    from lazyllm.tools.fs.client import FS
    skill_dir = tmp_path / 'skills' / 'visible'
    skill_dir.mkdir(parents=True)
    (skill_dir / 'SKILL.md').write_text('---\nname: visible\ndescription: Executor fixture\n---\n# Visible')
    lazyllm.globals['agentic_config'] = lazyllm.globals.get('agentic_config') or {}
    monkeypatch.setitem(lazyllm.globals, 'agentic_config', {
        'user_id': 'u', 'conversation_id': 'c', '_core_workspace_context': {'workspace_id': 'bound'},
    })

    def construct(**kwargs):
        if os.name == 'nt':
            from fsspec.implementations.local import LocalFileSystem
            assert isinstance(kwargs['fs'], LocalFileSystem)
        else:
            assert kwargs['fs'] is FS
        skills = SkillManager(dir=kwargs['skills_dir'], skills=kwargs['skills'], fs=kwargs['fs'])
        assert 'visible' in skills.build_prompt()
        agent = MagicMock()
        agent._skill_manager = skills
        agent._tools_manager = ToolManager(skills.get_skill_tools())
        return agent
    monkeypatch.setattr(executor_mod._agent_mod, 'ReactAgent', construct)
    agent = AgentExecutor().create_agent('llm', _plan(skills=['visible'], fs=FS, skills_dir=str(skill_dir.parent)))
    access = {name: tool.runtime_metadata.host_file_access.value
              for name, tool in agent._tools_manager.tools_info.items()}
    assert access == {
        'search_skill': 'NONE',
        'get_skill': 'NONE',
        'read_skill_resource': 'NONE',
        'run_skill_script': 'OPAQUE',
    }


def test_executor_composes_host_context_and_disables_hooks_for_preview(monkeypatch):
    constructor = MagicMock(side_effect=lambda **_: MagicMock())
    monkeypatch.setattr(executor_mod._agent_mod, 'ReactAgent', constructor)
    prepare = MagicMock()
    context = MagicMock(return_value='host update')
    agent = AgentExecutor().create_agent('llm', _plan(
        before_model_request=prepare, model_context_provider=context,
    ))
    agent._runtime_notice_buffer.publish('repeat notice')
    kwargs = constructor.call_args.kwargs
    assert kwargs['before_model_request'] is prepare
    assert kwargs['model_context_provider']() == 'repeat notice\n\nhost update'
    context.reset_mock()
    AgentExecutor().create_agent('llm', _plan(
        before_model_request=prepare, model_context_provider=context, context_preview=True,
    ))
    kwargs = constructor.call_args.kwargs
    assert 'before_model_request' not in kwargs
    assert kwargs['model_context_provider']() is None
    context.assert_not_called()
    prepare.assert_not_called()


@pytest.mark.parametrize('enabled', [False, True])
def test_repeat_convergence_is_opt_in_and_cancel_takes_priority(monkeypatch, enabled):
    agent = MagicMock()
    constructor = MagicMock(return_value=agent)
    monkeypatch.setattr(executor_mod._agent_mod, 'ReactAgent', constructor)
    check = MagicMock(return_value=False)
    created = AgentExecutor().create_agent('llm', _plan(
        stop_on_repeated_results=enabled, extra_stop_condition=check))
    created._exact_repeat_monitor.stop_requested = True
    stop = constructor.call_args.kwargs['extra_stop_condition']
    assert bool(stop({})) is enabled
    check.assert_called_once()
    check.side_effect = CancelledError('user cancelled')
    with pytest.raises(CancelledError):
        stop({})


def test_executor_compression_resets_stale_batch_streak(monkeypatch):
    agent = MagicMock()
    constructor = MagicMock(return_value=agent)
    monkeypatch.setattr(executor_mod._agent_mod, 'ReactAgent', constructor)

    def compress(history, keep, **kwargs):
        state = kwargs['runtime_state']
        state['compression_revision'] = state.get('compression_revision', 0) + 1
        return history, []

    created = AgentExecutor().create_agent('llm', _plan(history_compactor=compress))
    created._exact_repeat_monitor.stop_requested = True
    compact = constructor.call_args.kwargs['history_compactor']
    compact([], 2, runtime_state={})
    assert not created._exact_repeat_monitor.stop_requested


def test_repeat_notice_survives_actual_function_call_context_bound(monkeypatch):
    from lazyllm.tools.agent.functionCall import FunctionCall
    constructor = MagicMock(side_effect=lambda **_: MagicMock())
    monkeypatch.setattr(executor_mod._agent_mod, 'ReactAgent', constructor)
    directory = MagicMock()
    directory.model_context.return_value = 'large evidence context ' * 300
    agent = AgentExecutor().create_agent('llm', _plan(directory_runtime=directory))
    agent._runtime_notice_buffer.publish('Already seen this window; next_offset=41, EOF=false.')
    provider = constructor.call_args.kwargs['model_context_provider']
    fc = object.__new__(FunctionCall)
    fc._model_context_provider = provider
    fc._runtime_observer = None
    delivered = fc._consume_model_context()[0]['content']
    assert delivered.startswith('Already seen this window; next_offset=41, EOF=false.')
    assert len(delivered) <= 2048
    assert agent._runtime_notice_buffer.take() is None


def test_repeat_replay_stops_exploration_and_returns_final_answer(tmp_path):
    import copy
    import uuid
    import lazyllm
    from lazyllm.tools import ReactAgent
    from lazyllm.tools.agent import fc_register

    calls = []

    @fc_register(host_file='NONE')
    def read_evidence(query: str):
        '''Read stable evidence.

        Args:
            query: Evidence topic.
        '''
        calls.append(query)
        return 'early critical evidence'

    class Model:
        def __init__(self):
            self._module_id = uuid.uuid4().hex
            self.requests = []

        def share(self, **kwargs):
            return self

        def used_by(self, _):
            return self

        def __call__(self, *args, **kwargs):
            self.requests.append(copy.deepcopy((args, kwargs)))
            if len(self.requests) == 5:
                return 'Final answer with the verified evidence and gaps.'
            return {'content': '', 'tool_calls': [{
                'id': f'call-{len(self.requests)}', 'type': 'function',
                'function': {'name': 'read_evidence', 'arguments': '{"query":"topic"}'}}]}

    monitor = executor_mod.ExactRepeatMonitor()
    buffer = executor_mod.OneShotNoticeBuffer()

    def context():
        notice = buffer.take()
        if notice:
            monitor.notice_delivered()
        return notice

    with lazyllm.locals._scope():
        model = Model()
        agent = ReactAgent(model, [read_evidence], max_retries=20, force_summarize=True,
                           enable_builtin_tools=False, workspace=str(tmp_path),
                           model_context_provider=context,
                           extra_stop_condition=lambda _: monitor.stop_requested)
        agent._tools_manager = executor_mod.ToolExecutionMiddleware(
            agent._tools_manager, repeat_monitor=monitor, notice_buffer=buffer,
            workspace_permission=WorkspaceContext(local_runtime=False))
        result = agent('Original question')
        assert result == 'Final answer with the verified evidence and gaps.'
        assert calls == ['topic'] * 4  # Results were really executed, not cached/replaced.
        assert len(model.requests) == 5


@pytest.mark.parametrize('compressed', [False, True])
def test_executor_compression_resets_stale_batch_streak_and_notice(monkeypatch, compressed):
    agent = MagicMock()
    constructor = MagicMock(return_value=agent)
    monkeypatch.setattr(executor_mod._agent_mod, 'ReactAgent', constructor)

    def compress(history, keep, **kwargs):
        state = kwargs['runtime_state']
        if compressed:
            state['compression_revision'] = state.get('compression_revision', 0) + 1
        return history, []

    created = AgentExecutor().create_agent('llm', _plan(history_compactor=compress))
    created._exact_repeat_monitor.stop_requested = True
    created._runtime_notice_buffer.publish('Old repeat warning')
    compact = constructor.call_args.kwargs['history_compactor']
    compact([], 2, runtime_state={})
    assert created._exact_repeat_monitor.stop_requested is not compressed
    assert created._runtime_notice_buffer.take() == (None if compressed else 'Old repeat warning')


def test_actual_agent_recovers_evidence_after_compaction_and_reaches_new_document(tmp_path):
    import uuid
    import lazyllm
    from lazyllm.tools.agent import fc_register
    from lazyllm.tools.agent.reactAgent import _FORCE_SUMMARIZE_MSG

    calls, compressions = [], []
    sequence = ['A', 'B', 'A', 'C', 'A', 'B', 'D']

    @fc_register(host_file='NONE')
    def read_evidence(topic: str):
        '''Read an evidence document.

        Args:
            topic: Document identity.
        '''
        calls.append(topic)
        return 'Stable body for ' + topic

    class Model:
        def __init__(self):
            self._module_id = uuid.uuid4().hex

        def share(self, **kwargs):
            return self

        def used_by(self, _):
            return self

        def __call__(self, *args, **kwargs):
            if args and isinstance(args[0], str) and _FORCE_SUMMARIZE_MSG in args[0]:
                return 'Forced summary before finishing the evidence'
            if len(calls) == len(sequence):
                return {'content': 'Final answer including D'}
            return {'content': '', 'tool_calls': [{
                'id': f'call-{len(calls)}', 'type': 'function',
                'function': {'name': 'read_evidence', 'arguments': json.dumps({'topic': sequence[len(calls)]})}}]}

    def compact(history, keep, **kwargs):
        current = kwargs.get('current_round_messages', [])
        if len(calls) == 4 and not compressions:
            state = kwargs['runtime_state']
            state['compression_revision'] = state.get('compression_revision', 0) + 1
            compressions.append(state['compression_revision'])
            return [{'role': 'user', 'content': 'Summary: re-read A and B, then read D.'}], current
        return history, current

    plan = _plan(enable_builtin_tools=False, history_compactor=compact, stop_on_repeated_results=True,
                 workspace=str(tmp_path), max_retries=20)
    plan.tools = [read_evidence]
    with lazyllm.locals._scope():
        result = AgentExecutor().create_agent(Model(), plan)('Read the evidence including D.')
    assert calls == sequence
    assert compressions == [1]
    assert result == 'Final answer including D'
