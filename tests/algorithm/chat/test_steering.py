from __future__ import annotations

import asyncio
from copy import deepcopy
from dataclasses import replace
import uuid

import lazyllm
import pytest

from lazymind.chat.engine.agent_runtime import (
    AgentExecutionOptions,
    AgentExecutor,
    AgentRole,
    AgentRunPlan,
)
from lazymind.chat.engine.agent_runtime.models import PromptBundle
from lazymind.chat.engine.agent_runtime.executor import AgentInvocation
from lazymind.chat.engine.agent_runtime.steering import (
    continue_in_workspace,
    SteeringInput,
    SteeringInterruptedError,
    SteeringRegistry,
    install_stream_interrupt_hook,
    render_steering_context,
    steering_registry,
)


def travel_plan():
    question = '从北京出发，比较天津、济南、青岛等城市的周末旅游。'
    return AgentRunPlan(
        role=AgentRole.CHAT,
        prompt=PromptBundle((), 'system', question, 'Input', question),
        history=[{'role': 'user', 'content': '我喜欢当地小吃，不自驾。'},
                 {'role': 'assistant', 'content': '好的，优先考虑高铁。'}],
        force_summarize_context=question,
        execution_options=AgentExecutionOptions(enable_builtin_tools=False),
    )


@pytest.fixture(autouse=True)
def isolated_workspace():
    previous = lazyllm.locals._sid
    lazyllm.locals._init_sid(uuid.uuid4().hex)
    yield
    lazyllm.locals.clear()
    lazyllm.locals._init_sid(previous)


def begin_fake_model_call(plan):
    workspace = lazyllm.locals['_lazyllm_agent'].setdefault('workspace', {})
    history = workspace.setdefault('history', list(plan.history))
    history.append({'role': 'user', 'content': plan.prompt.current_input})
    return history


@pytest.mark.parametrize('count', [2, 3, 10])
def test_all_guides_and_partial_answers_survive_every_continuation(count):
    plan = travel_plan()
    expected = deepcopy(plan.history)
    expected.append({'role': 'user', 'content': plan.prompt.current_input})
    for index in range(count):
        workspace_history = begin_fake_model_call(plan)
        answer = f'已生成的回复片段 {index}'
        guide = SteeringInput(str(index), f'补充要求 {index}', index)
        plan = continue_in_workspace(plan, [guide], answer)
        assert plan.history is workspace_history
        expected.append({'role': 'assistant', 'content': answer})
        assert plan.history == expected
        expected.append({'role': 'user', 'content': plan.prompt.current_input})
        assert f'补充要求 {index}' in plan.prompt.current_input
        assert '从北京出发' in plan.force_summarize_context


def test_burst_guidance_without_output_is_ordered_and_not_overwritten():
    plan = travel_plan()
    begin_fake_model_call(plan)
    guides = [SteeringInput(str(i), text, i) for i, text in enumerate([
        '只比较天津和济南', '两人两天一夜，不超过2000元', '最终只推荐一个城市',
    ])]
    plan = continue_in_workspace(plan, guides, '')
    assert len(plan.history) == 5  # two prior messages + original + first two guides
    assert guides[0].content in plan.history[-2]['content']
    assert guides[1].content in plan.history[-1]['content']
    assert guides[2].content in plan.prompt.current_input
    begin_fake_model_call(plan)
    next_plan = continue_in_workspace(plan, [SteeringInput('next', '不要自驾', 4)], '')
    for guide in guides:
        assert any(guide.content in message['content'] for message in next_plan.history)


def test_explicit_steering_guidance_is_applied_without_reconfirmation():
    guidance = render_steering_context([
        SteeringInput('guide', '只比较天津和济南，最终只推荐天津。', 1),
    ])

    assert '只比较天津和济南，最终只推荐天津。' in guidance
    assert 'apply it immediately' in guidance
    assert 'Do not call ask_user merely to confirm' in guidance
    assert 'essential information is still genuinely missing' in guidance


def test_workspace_keeps_tool_pairs_in_place():
    plan = travel_plan()
    workspace_history = [*plan.history, {'role': 'user', 'content': plan.prompt.current_input},
                         {'role': 'assistant', 'content': '正在查交通', 'tool_calls': [{
                             'id': 'train-1', 'type': 'function', 'function': {
                                 'name': 'search_train', 'arguments': '{}'}}]},
                         {'role': 'tool', 'tool_call_id': 'train-1', 'content': '天津高铁票54.5元'}]
    lazyllm.locals['_lazyllm_agent']['workspace'] = {'history': workspace_history}
    saved = deepcopy(workspace_history)
    continued = continue_in_workspace(plan, [SteeringInput('1', '只比较天津和济南', 1)], '天津交通方便')
    assert continued.history is workspace_history
    assert continued.history == [*saved, {'role': 'assistant', 'content': '天津交通方便'}]


def test_late_guidance_moves_completed_history_back_without_copying():
    plan = travel_plan()
    history = begin_fake_model_call(plan)
    state = lazyllm.locals['_lazyllm_agent']
    state['history'] = state.pop('workspace')['history']
    continued = continue_in_workspace(plan, [SteeringInput('late', '改成济南', 1)], '天津更近')
    assert continued.history is history
    assert state['workspace']['history'] is history
    assert 'history' not in state
    assert history[-1] == {'role': 'assistant', 'content': '天津更近'}


def test_missing_workspace_never_silently_continues_with_only_latest_guide():
    with pytest.raises(RuntimeError, match='workspace is unavailable'):
        continue_in_workspace(travel_plan(), [SteeringInput('1', '预算2000元', 1)], '')


@pytest.mark.parametrize('cancelled', [False, True])
def test_real_cancellation_and_provider_error_still_clear_workspace(tmp_path, cancelled):
    from lazyllm.tools.agent import fc_register

    @fc_register(host_file='NONE')
    def unused_cleanup_probe(topic: str):
        '''Offline cleanup probe.

        Args:
            topic: Input topic.
        '''
        raise AssertionError('tool must not run')

    class BrokenModel:
        _module_id = 'broken-model'

        def share(self, **kwargs):
            return self

        def used_by(self, _):
            return self

        def __call__(self, *args, **kwargs):
            if cancelled:
                raise asyncio.CancelledError('user stop')
            raise ValueError('provider failure')

    plan = travel_plan()
    plan = replace(plan, tools=[unused_cleanup_probe], execution_options=replace(plan.execution_options, workspace=str(tmp_path)))
    agent = AgentExecutor().create_agent(BrokenModel(), plan)
    with pytest.raises(asyncio.CancelledError if cancelled else Exception):
        agent(plan.prompt.current_input, llm_chat_history=plan.history)
    assert 'workspace' not in lazyllm.locals['_lazyllm_agent']
    assert 'broken-model' not in lazyllm.locals['chat_history']


@pytest.mark.parametrize('with_tools', [False, True])
def test_real_agent_requests_keep_prior_guides_after_repeated_interrupts(tmp_path, with_tools):
    """Exercise the actual executor/ReactAgent with an in-process fake model.

    Inspect model-visible messages, not just the queue or UI; no provider calls.
    """
    from lazyllm.tools.agent import fc_register

    @fc_register(host_file='NONE')
    def local_evidence(topic: str):
        '''Return local test evidence.

        Args:
            topic: Topic to inspect.
        '''
        return topic

    guides = ['只比较天津和济南', '两人两天一夜，不超过2000元', '最后只推荐一个城市']
    run_id, session_id = uuid.uuid4().hex, uuid.uuid4().hex
    requests = []

    class Model:
        _stream_output = lazyllm.ModuleBase._stream_output

        def __init__(self):
            self._module_id = uuid.uuid4().hex
            self.replies = 0

        def share(self, **kwargs):
            return self

        def used_by(self, _):
            return self

        def __call__(self, input, **kwargs):
            history = deepcopy(lazyllm.locals['chat_history'].get(self._module_id, []))
            model_input = input.get('input', input) if isinstance(input, dict) else input
            current = model_input if isinstance(model_input, list) else [{'role': 'user', 'content': model_input}]
            requests.append(([*history, *current], self.replies))
            if with_tools and isinstance(input, str):
                return {'content': '已查到交通资料', 'tool_calls': [{
                    'id': f'train-{self.replies}', 'type': 'function', 'function': {
                        'name': 'local_evidence', 'arguments': '{"topic":"天津高铁54.5元"}'}}]}
            index = self.replies
            self.replies += 1
            self._stream_output(f'旅游回复片段 {index}')
            if index < len(guides):
                steering_registry.accept(run_id, 'travel', str(index), guides[index])
                self._stream_output('must not appear')
            return {'content': '最终推荐天津', 'tool_calls': []}

    async def exercise():
        plan = travel_plan()
        plan = replace(plan, tools=[local_evidence], execution_options=replace(
            plan.execution_options, workspace=str(tmp_path),
        ))
        executor = AgentExecutor()
        model = Model()
        agent = executor.create_agent(model, plan)
        workspace_history = None
        while True:
            assert install_stream_interrupt_hook(executor.runtime_llm(agent))
            text = []
            try:
                async for kind, payload in lazyllm.enable_trace(
                    AgentInvocation(executor, agent, plan), trace_id=run_id,
                    session_id=session_id, debug_capture_payload=False,
                ):
                    if kind == 'event' and payload.get('tag') == 'text':
                        text.append(payload['delta'])
            except SteeringInterruptedError:
                plan = continue_in_workspace(
                    plan, steering_registry.drain_or_close(run_id), ''.join(text),
                )
                if workspace_history is not None:
                    assert workspace_history is plan.history
                workspace_history = plan.history
            else:
                break

    previous_global_sid, previous_local_sid = lazyllm.globals._sid, lazyllm.locals._sid
    steering_registry.register(run_id, 'travel', session_id)
    try:
        lazyllm.globals._init_sid(session_id)
        lazyllm.locals._init_sid(session_id)
        asyncio.run(exercise())
        assert len(requests) == (8 if with_tools else 4)
        for request, index in requests:
            contents = '\n'.join(message['content'] for message in request)
            assert '从北京出发' in contents and '不自驾' in contents
            for earlier in range(index):
                assert guides[earlier] in contents
                assert f'旅游回复片段 {earlier}' in contents
                if with_tools:
                    assert any(message.get('tool_call_id') == f'train-{earlier}' for message in request)
            assert 'must not appear' not in contents
    finally:
        steering_registry.unregister(run_id)
        lazyllm.globals._init_sid(previous_global_sid)
        lazyllm.locals._init_sid(previous_local_sid)


def test_provider_http_messages_keep_full_context_across_consecutive_guides(
    monkeypatch, tmp_path,
):
    """Assert the final provider request body, not an intermediate history list."""
    from lazyllm.module.llms.onlinemodule.base.onlineChatModuleBase import (
        LazyLLMOnlineChatModuleBase,
    )
    from lazyllm.module.llms.onlinemodule.chat import dynamic_chat_config
    from lazyllm.tools.agent import fc_register

    @fc_register(host_file='NONE')
    def steering_http_probe(topic: str):
        '''Return local evidence for an offline request-construction test.

        Args:
            topic: Topic to inspect.
        '''
        return topic

    requests: list[list[dict]] = []

    def capture_provider_request(_self, data, **_kwargs):
        requests.append(deepcopy(data['messages']))
        # Stop immediately before transport; this test never contacts a model.
        raise SteeringInterruptedError('captured provider request before HTTP')

    monkeypatch.setattr(
        LazyLLMOnlineChatModuleBase,
        '_forward_with_retry',
        capture_provider_request,
    )

    plan = travel_plan()
    plan = replace(
        plan,
        tools=[steering_http_probe],
        execution_options=replace(
            plan.execution_options,
            workspace=str(tmp_path),
        ),
    )
    guides = ['只比较天津和济南', '两人两天一夜，不超过2000元']
    replies = ['已开始比较天津和济南', '天津的两人预算更低']
    executor = AgentExecutor()
    model = lazyllm.OnlineChatModule(source='dynamic', api_key='dynamic')
    previous_global_sid, previous_local_sid = lazyllm.globals._sid, lazyllm.locals._sid
    session_id = f'provider-http-steering-{uuid.uuid4().hex}'

    try:
        lazyllm.globals._init_sid(session_id)
        lazyllm.locals._init_sid(session_id)
        with dynamic_chat_config(
            source='siliconflow',
            model='deepseek-ai/DeepSeek-V4-Flash',
            url='http://127.0.0.1:1',
        ):
            agent = executor.create_agent(model, plan)
            for index in range(3):
                assert install_stream_interrupt_hook(executor.runtime_llm(agent))
                with pytest.raises(SteeringInterruptedError):
                    agent(
                        plan.prompt.current_input,
                        llm_chat_history=plan.history,
                    )
                if index < len(guides):
                    plan = continue_in_workspace(
                        plan,
                        [SteeringInput(str(index), guides[index], index)],
                        replies[index],
                    )

        assert len(requests) == 3
        for index, messages in enumerate(requests):
            contents = '\n'.join(str(message.get('content', '')) for message in messages)
            assert '从北京出发' in contents
            assert '我喜欢当地小吃，不自驾。' in contents
            for previous in range(index):
                assert guides[previous] in contents
                assert replies[previous] in contents
            assert messages[-1]['role'] == 'user'
            expected_input = (
                travel_plan().prompt.current_input if index == 0 else guides[index - 1]
            )
            assert expected_input in str(messages[-1]['content'])
    finally:
        lazyllm.globals._init_sid(previous_global_sid)
        lazyllm.locals._init_sid(previous_local_sid)


def test_atomic_drain_or_close_does_not_accept_an_unconsumable_guide():
    registry = SteeringRegistry()
    registry.register('run', 'chat', 'session')
    registry.accept('run', 'chat', 'one', 'first')
    assert [item.content for item in registry.drain_or_close('run')] == ['first']
    assert registry.accept('run', 'chat', 'two', 'second') == (True, '')
    assert [item.content for item in registry.drain_or_close('run')] == ['second']
    assert registry.drain_or_close('run') == []
    assert registry.accept('run', 'chat', 'three', 'third') == (False, 'response_not_active')


def test_guidance_is_not_truncated_to_ephemeral_context_limit():
    plan = travel_plan()
    begin_fake_model_call(plan)
    long_guide = '旅行预算细则' * 1000 + '最终仍只推荐天津或济南'
    plan = continue_in_workspace(plan, [SteeringInput('long', long_guide, 1)], '')
    assert long_guide in plan.prompt.current_input
    begin_fake_model_call(plan)
    plan = continue_in_workspace(plan, [SteeringInput('next', '改成两晚', 2)], '')
    assert long_guide in plan.history[-1]['content']


def test_steering_registry_accepts_in_order_and_deduplicates():
    registry = SteeringRegistry()
    registry.register('run-1', 'conversation-1', 'session-1')

    assert registry.accept('run-1', 'conversation-1', 'steer-1', 'first') == (True, '')
    assert registry.accept('run-1', 'conversation-1', 'steer-1', 'first') == (True, '')
    assert registry.accept('run-1', 'conversation-1', 'steer-2', 'second') == (True, '')

    drained = registry.drain_or_close('run-1')
    assert [(item.steer_id, item.content) for item in drained] == [
        ('steer-1', 'first'),
        ('steer-2', 'second'),
    ]
    assert registry.drain_or_close('run-1') == []


def test_steering_registry_closes_atomically_after_last_safe_boundary():
    registry = SteeringRegistry()
    registry.register('run-1', 'conversation-1', 'session-1')

    assert registry.drain_or_close('run-1') == []
    assert registry.accept('run-1', 'conversation-1', 'late', 'too late') == (
        False,
        'response_not_active',
    )


def test_steering_registry_keeps_late_input_for_continuation():
    registry = SteeringRegistry()
    registry.register('run-1', 'conversation-1', 'session-1')
    registry.accept('run-1', 'conversation-1', 'steer-1', 'change direction')

    context = render_steering_context(registry.drain_or_close('run-1'))
    assert 'change direction' in context
    assert 'next safe boundary' in context
    assert registry.drain_or_close('run-1') == []


def test_steering_registry_rejects_wrong_conversation():
    registry = SteeringRegistry()
    registry.register('run-1', 'conversation-1', 'session-1')
    assert registry.accept('run-1', 'conversation-2', 'steer-1', 'wrong owner') == (
        False,
        'response_not_active',
    )


def test_repeated_registration_preserves_early_guidance():
    registry = SteeringRegistry()
    registry.register('run-1', 'conversation-1', 'session-1')
    registry.accept('run-1', 'conversation-1', 'steer-1', 'arrived during setup')

    registry.register('run-1', 'conversation-1', 'session-1')

    assert [item.content for item in registry.drain_or_close('run-1')] == ['arrived during setup']


def test_cancellation_stops_accepting_before_stream_cleanup():
    registry = SteeringRegistry()
    registry.register('run-1', 'conversation-1', 'session-1')

    assert registry.stop_accepting_for_session('conversation-1', 'session-1') is True
    assert registry.accept('run-1', 'conversation-1', 'late', 'too late') == (
        False,
        'response_not_active',
    )


def test_cancellation_does_not_close_successor_session():
    registry = SteeringRegistry()
    registry.register('run-1', 'conversation-1', 'session-1')
    registry.register('run-2', 'conversation-1', 'session-2')

    assert registry.stop_accepting_for_session('conversation-1', 'session-1') is True
    assert registry.accept('run-2', 'conversation-1', 'steer-2', 'still active') == (True, '')


def test_pending_guidance_is_scoped_to_its_session():
    registry = SteeringRegistry()
    registry.register('run-1', 'conversation-1', 'session-1')
    registry.register('run-2', 'conversation-2', 'session-2')

    assert registry.has_pending_for_session('session-1') is False
    registry.accept('run-1', 'conversation-1', 'steer-1', 'change direction')
    assert registry.has_pending_for_session('session-1') is True
    assert registry.has_pending_for_session('session-2') is False
    registry.drain_or_close('run-1')
    assert registry.has_pending_for_session('session-1') is False


def test_stream_hook_interrupts_only_while_guidance_is_pending():
    class FakeModel:
        def __init__(self):
            self.output: list[tuple[str, str | None]] = []

        def _stream_output(self, text, color=None, *, cls='text'):
            self.output.append((text, cls))
            return ''

    run_id = 'stream-hook-run'
    session_id = 'stream-hook-session'
    model = FakeModel()
    previous_sid = lazyllm.globals._sid
    steering_registry.register(run_id, 'stream-hook-conversation', session_id)
    try:
        lazyllm.globals._init_sid(session_id)
        assert install_stream_interrupt_hook(model) is True
        model._stream_output('before')
        steering_registry.accept(
            run_id,
            'stream-hook-conversation',
            'steer-1',
            'change direction',
        )
        with pytest.raises(SteeringInterruptedError):
            model._stream_output('discarded')
        steering_registry.drain_or_close(run_id)
        model._stream_output('after', cls='think')
        assert model.output == [('before', 'text'), ('after', 'think')]
    finally:
        steering_registry.unregister(run_id)
        lazyllm.globals._init_sid(previous_sid)


def test_dynamic_stream_hook_resolves_supplier_lazily():
    class Supplier:
        def _stream_output(self, text, color=None, *, cls='text'):
            return text

    class Router:
        def __init__(self):
            self.supplier = Supplier()
            self.resolved = 0

        def _get_supplier(self):
            self.resolved += 1
            return self.supplier

        def forward(self, text):
            return self._get_supplier()._stream_output(text)

    router = Router()
    assert install_stream_interrupt_hook(router) is True
    assert router.resolved == 0
    assert router.forward('hello') == 'hello'
    assert router.resolved == 2
    assert router.supplier._lazymind_steering_interrupt_installed is True


def test_stream_interrupt_propagates_through_agent_executor():
    class FakeModel:
        _stream_output = lazyllm.ModuleBase._stream_output

    class FakeAgent:
        def __init__(self, model):
            self.model = model

        def __call__(self, *args, **kwargs):
            self.model._stream_output('before')
            steering_registry.accept(
                'propagation-run',
                'propagation-conversation',
                'guide',
                'change direction now',
            )
            self.model._stream_output('discarded')

    async def exercise() -> list[tuple[str, object]]:
        seen: list[tuple[str, object]] = []
        with pytest.raises(SteeringInterruptedError):
            async for item in AgentExecutor().stream_agent(agent, plan):
                seen.append(item)
        return seen

    model = FakeModel()
    agent = FakeAgent(model)
    plan = AgentRunPlan(
        role=AgentRole.CHAT,
        prompt=PromptBundle((), '', 'prompt', 'Input', 'prompt'),
        execution_options=AgentExecutionOptions(),
    )
    previous_global_sid = lazyllm.globals._sid
    previous_local_sid = lazyllm.locals._sid
    steering_registry.register(
        'propagation-run',
        'propagation-conversation',
        'propagation-session',
    )
    try:
        lazyllm.globals._init_sid('propagation-session')
        lazyllm.locals._init_sid('propagation-session')
        assert install_stream_interrupt_hook(model) is True
        seen = asyncio.run(exercise())
        assert any(
            kind == 'event' and payload.get('delta') == 'before'
            for kind, payload in seen
        )
        assert all(
            payload.get('delta') != 'discarded'
            for kind, payload in seen
            if kind == 'event'
        )
    finally:
        steering_registry.unregister('propagation-run')
        lazyllm.globals._init_sid(previous_global_sid)
        lazyllm.locals._init_sid(previous_local_sid)
