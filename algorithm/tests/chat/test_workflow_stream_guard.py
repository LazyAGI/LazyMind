from __future__ import annotations

import asyncio

from lazymind.chat.workflow.workflow_manager import guard_workflow_agent_stream


def _tool(name, callback):
    callback.__name__ = name
    return callback


async def _stream(*items):
    for item in items:
        yield item


async def _collect(stream):
    return [item async for item in stream]


def test_guard_recovers_omitted_auto_steps_and_discards_ungrounded_success_text():
    state = {'ready': 'prepare'}
    calls = []

    def get_ready_steps():
        ready = [state['ready']] if state['ready'] else []
        return {
            'ready_steps': ready,
            'ready_step_details': [{
                'step_id': state['ready'],
                'mode': 'auto',
                'requires_approval': False,
                'execution_tool': 'advance_step',
            }] if ready else [],
            'projection': {'completed': not ready},
        }

    def advance_step(step_ids):
        calls.append(step_ids)
        state['ready'] = {'prepare': 'outline', 'outline': ''}[step_ids[0]]
        return {
            'status': 'completed' if not state['ready'] else 'active',
            'outcome': 'workflow_completed' if not state['ready'] else 'step_succeeded',
        }

    initial = _stream(
        ('event', {'tag': 'tool_results', 'tool_results': [{
            'name': 'trigger_writer_workflow',
            'result': {'ok': True, 'value': {'session_id': 'session-1'}},
        }]}),
        ('event', {'tag': 'think', 'delta': '{"name":"advance_step"} 已调用成功。'}),
        ('event', {'tag': 'text', 'delta': 'prepare 已完成，正文也已经生成。'}),
        ('event', {'tag': 'tool_results', 'tool_results': [{
            'name': 'advance_step',
            'result': {'ok': False, 'value': 'outline is not Ready'},
        }]}),
        ('final', '伪造的最终文档'),
    )

    received = asyncio.run(_collect(guard_workflow_agent_stream(
        initial,
        all_tools=[
            _tool('get_ready_steps', get_ready_steps),
            _tool('advance_step', advance_step),
        ],
        runtime_prompt='## Explicit Workflow Selection [AUTHORITATIVE]',
        query='请启动 writer 工作流并连续执行。',
    )))

    assert calls == [['prepare'], ['outline']]
    assert not any(
        item[1].get('delta') in {
            '{"name":"advance_step"} 已调用成功。',
            'prepare 已完成，正文也已经生成。',
        }
        for item in received if item[0] == 'event' and isinstance(item[1], dict)
    )
    assert [
        item[1]['tool_calls'][0]['function']['arguments']['step_ids']
        for item in received
        if item[0] == 'event' and item[1].get('tag') == 'tool_calls'
    ] == [['prepare'], ['outline']]
    assert received[-2] == ('event', {'tag': 'text', 'delta': '工作流已通过真实工具调用完成。'})
    assert received[-1] == ('final', '工作流已通过真实工具调用完成。')


def test_guard_does_not_auto_dispatch_human_or_ambiguous_ready_frontier():
    calls = []

    def get_ready_steps():
        return {
            'ready_steps': ['review'],
            'ready_step_details': [{
                'step_id': 'review',
                'mode': 'human',
                'requires_approval': True,
                'execution_tool': 'advance_step_and_hand_off',
            }],
            'projection': {'completed': False},
        }

    def advance_step(step_ids):
        calls.append(step_ids)

    received = asyncio.run(_collect(guard_workflow_agent_stream(
        _stream(
            ('event', {'tag': 'text', 'delta': '全部完成。'}),
            ('final', '全部完成。'),
        ),
        all_tools=[
            _tool('get_ready_steps', get_ready_steps),
            _tool('advance_step', advance_step),
        ],
        runtime_prompt='## Workflow Runtime [AUTHORITATIVE]',
        query='请继续执行工作流',
    )))

    assert calls == []
    assert received == [
        ('event', {
            'tag': 'text',
            'delta': '工作流当前停在可执行步骤 review；需要按当前审批策略继续。',
        }),
        ('final', '工作流当前停在可执行步骤 review；需要按当前审批策略继续。'),
    ]


def test_guard_respects_explicit_user_stop_boundary():
    queries = [
        '只执行 prepare 后停下',
        '执行完 prepare 就停',
        '运行到 prepare 为止',
        '先只做准备阶段，后续不要跑',
        '执行 prepare 后不要再继续',
        '只执行第一步',
        '只执行 prepare，给我完整的分析结果后停下',
        '执行完 prepare 就停，给我完整的分析结果',
        '只执行 prepare，保留全部材料',
    ]
    for query in queries:
        state = {'ready': 'prepare'}
        calls = []

        def get_ready_steps():
            return {
                'ready_steps': [state['ready']],
                'ready_step_details': [{
                    'step_id': state['ready'],
                    'mode': 'auto',
                    'requires_approval': False,
                    'execution_tool': 'advance_step',
                }],
                'projection': {'completed': False},
            }

        def advance_step(step_ids):
            calls.append(step_ids)
            state['ready'] = 'outline'
            return {'status': 'active', 'outcome': 'step_succeeded'}

        received = asyncio.run(_collect(guard_workflow_agent_stream(
            _stream(
                ('event', {'tag': 'tool_results', 'tool_results': [{
                    'name': 'trigger_writer_workflow',
                    'result': {'ok': True, 'value': {'session_id': 'session-1'}},
                }]}),
                ('event', {'tag': 'text', 'delta': 'prepare 和后续步骤都已完成。'}),
                ('final', '全部完成。'),
            ),
            all_tools=[
                _tool('get_ready_steps', get_ready_steps),
                _tool('advance_step', advance_step),
            ],
            runtime_prompt='## Explicit Workflow Selection [AUTHORITATIVE]',
            query=query,
        )))

        assert calls == [['prepare']]
        assert received[-1] == (
            'final',
            '工作流当前停在可执行步骤 outline，已实际执行：prepare；需要按当前审批策略继续。',
        )


def test_guard_honors_direct_negative_execution_requests():
    queries = [
        '不要继续',
        '先别继续',
        '不要启动工作流',
        '先不要执行',
        'stop',
        'pause the workflow',
    ]
    for query in queries:
        def get_ready_steps():
            raise AssertionError('a negative request must not inspect or advance Workflow state')

        received = asyncio.run(_collect(guard_workflow_agent_stream(
            _stream(('event', {'tag': 'text', 'delta': '已完成。'}), ('final', '已完成。')),
            all_tools=[
                _tool('get_ready_steps', get_ready_steps),
                _tool('advance_step', lambda _step_ids: None),
            ],
            runtime_prompt='## Explicit Workflow Selection [AUTHORITATIVE]',
            query=query,
        )))

        assert received == [
            ('event', {'tag': 'text', 'delta': '已遵循当前请求，未自动启动或推进工作流。'}),
            ('final', '已遵循当前请求，未自动启动或推进工作流。'),
        ]


def test_guard_does_not_lazy_initialize_from_selection_marker_alone():
    def get_ready_steps():
        raise AssertionError('selection marker alone must not initialize a Workflow')

    received = asyncio.run(_collect(guard_workflow_agent_stream(
        _stream(('event', {'tag': 'text', 'delta': '工作流已启动。'}), ('final', '已启动。')),
        all_tools=[
            _tool('get_ready_steps', get_ready_steps),
            _tool('advance_step', lambda _step_ids: None),
        ],
        runtime_prompt='## Explicit Workflow Selection [AUTHORITATIVE]',
        query='使用 writer 工作流写一篇短文',
    )))

    assert received == [
        ('event', {'tag': 'text', 'delta': '工作流尚未通过真实触发器初始化，未自动启动。'}),
        ('final', '工作流尚未通过真实触发器初始化，未自动启动。'),
    ]


def test_guard_never_advances_past_ask_pending():
    def get_ready_steps():
        raise AssertionError('a clarification boundary must not initialize or advance a Workflow')

    ask_event = ('event', {
        'tag': 'ask_pending',
        'ask_id': 'ask-1',
        'questions': [{'text': '需要多少字？', 'type': 'text'}],
    })
    received = asyncio.run(_collect(guard_workflow_agent_stream(
        _stream(
            ('event', {'tag': 'text', 'delta': '工作流已启动。'}),
            ask_event,
            ('final', 'Waiting for user input.'),
        ),
        all_tools=[
            _tool('get_ready_steps', get_ready_steps),
            _tool('advance_step', lambda _step_ids: None),
        ],
        runtime_prompt='## Explicit Workflow Selection [AUTHORITATIVE]',
        query='启动写作工作流',
    )))

    assert received == [ask_event, ('final', 'Waiting for user input.')]


def test_guard_never_advances_past_handoff_tool():
    def get_ready_steps():
        raise AssertionError('handoff must end the turn without inspecting the next frontier')

    handoff_result = ('event', {'tag': 'tool_results', 'tool_results': [{
        'name': 'advance_step_and_hand_off',
        'result': {
            'ok': True,
            'value': '{"accepted":true,"task_id":"task-1","step_state":"pending"}',
        },
    }]})
    received = asyncio.run(_collect(guard_workflow_agent_stream(
        _stream(handoff_result, ('final', 'review handoff')),
        all_tools=[
            _tool('get_ready_steps', get_ready_steps),
            _tool('advance_step', lambda _step_ids: None),
        ],
        runtime_prompt='## Explicit Workflow Selection [AUTHORITATIVE]',
        query='执行到审批点后等待我确认',
    )))

    assert received == [
        handoff_result,
        ('event', {'tag': 'text', 'delta': '工作流已在人工交接边界停止，未自动推进后续步骤。'}),
        ('final', '工作流已在人工交接边界停止，未自动推进后续步骤。'),
    ]


def test_guard_does_not_claim_a_failed_handoff_succeeded():
    def get_ready_steps():
        raise AssertionError('failed handoff must not advance the next frontier')

    handoff_result = ('event', {'tag': 'tool_results', 'tool_results': [{
        'name': 'advance_step_and_hand_off',
        'result': {'ok': True, 'value': '{"outcome":"workflow_state_changed"}'},
    }]})
    received = asyncio.run(_collect(guard_workflow_agent_stream(
        _stream(handoff_result, ('final', 'handoff failed')),
        all_tools=[
            _tool('get_ready_steps', get_ready_steps),
            _tool('advance_step', lambda _step_ids: None),
        ],
        runtime_prompt='## Explicit Workflow Selection [AUTHORITATIVE]',
        query='执行到审批点后等待我确认',
    )))

    assert received[-1] == ('final', '人工交接调用未成功，未自动推进后续步骤。')


def test_guard_preserves_model_output_when_runtime_is_already_completed():
    def get_ready_steps():
        return {'ready_steps': [], 'projection': {'completed': True}}

    def advance_step(_step_ids):
        raise AssertionError('completed Workflow must not advance')

    initial_items = [
        ('event', {'tag': 'text', 'delta': '工作流已完成，产物如下。'}),
        ('final', '工作流已完成，产物如下。'),
    ]
    received = asyncio.run(_collect(guard_workflow_agent_stream(
        _stream(*initial_items),
        all_tools=[
            _tool('get_ready_steps', get_ready_steps),
            _tool('advance_step', advance_step),
        ],
        runtime_prompt='## Workflow Runtime [AUTHORITATIVE]',
        query='继续',
    )))

    assert received == initial_items


def test_guard_does_not_dispatch_without_ready_metadata():
    calls = []

    def get_ready_steps():
        return {'ready_steps': ['prepare'], 'projection': {'completed': False}}

    def advance_step(step_ids):
        calls.append(step_ids)

    received = asyncio.run(_collect(guard_workflow_agent_stream(
        _stream(('event', {'tag': 'text', 'delta': '已完成。'}), ('final', '已完成。')),
        all_tools=[
            _tool('get_ready_steps', get_ready_steps),
            _tool('advance_step', advance_step),
        ],
        runtime_prompt='## Workflow Runtime [AUTHORITATIVE]',
        query='继续',
    )))

    assert calls == []
    assert received[-1][1].startswith('工作流当前停在可执行步骤 prepare')


def test_guard_does_not_advance_existing_workflow_for_unrelated_query():
    calls = []

    def get_ready_steps():
        raise AssertionError('unrelated turn must not inspect or mutate Workflow state')

    def advance_step(step_ids):
        calls.append(step_ids)

    initial_items = [
        ('event', {'tag': 'text', 'delta': '北京今天晴。'}),
        ('final', '北京今天晴。'),
    ]
    received = asyncio.run(_collect(guard_workflow_agent_stream(
        _stream(*initial_items),
        all_tools=[
            _tool('get_ready_steps', get_ready_steps),
            _tool('advance_step', advance_step),
        ],
        runtime_prompt='## Workflow Runtime [AUTHORITATIVE]',
        query='北京今天天气怎么样？',
    )))

    assert calls == []
    assert received == initial_items


def test_guard_does_not_treat_question_containing_continue_as_recovery_command():
    def get_ready_steps():
        raise AssertionError('a question about continuation must not dispatch the Workflow')

    initial_items = [
        ('event', {'tag': 'text', 'delta': '原因如下。'}),
        ('final', '原因如下。'),
    ]
    received = asyncio.run(_collect(guard_workflow_agent_stream(
        _stream(*initial_items),
        all_tools=[
            _tool('get_ready_steps', get_ready_steps),
            _tool('advance_step', lambda _step_ids: None),
        ],
        runtime_prompt='## Workflow Runtime [AUTHORITATIVE]',
        query='为什么不能继续？',
    )))

    assert received == initial_items


def test_guard_recovers_existing_ready_session_when_user_says_continue():
    state = {'completed': False}
    calls = []

    def get_ready_steps():
        return {
            'ready_steps': [] if state['completed'] else ['prepare'],
            'ready_step_details': [] if state['completed'] else [{
                'step_id': 'prepare',
                'mode': 'auto',
                'requires_approval': False,
                'default_approval': 'not_required',
                'execution_tool': 'advance_step',
            }],
            'projection': {'completed': state['completed']},
        }

    def advance_step(step_ids):
        calls.append(step_ids)
        state['completed'] = True
        return {'status': 'completed', 'outcome': 'workflow_completed'}

    received = asyncio.run(_collect(guard_workflow_agent_stream(
        _stream(('event', {'tag': 'text', 'delta': '稍后继续。'}), ('final', '稍后继续。')),
        all_tools=[
            _tool('get_ready_steps', get_ready_steps),
            _tool('advance_step', advance_step),
        ],
        runtime_prompt='## Workflow Runtime [AUTHORITATIVE]',
        query='继续',
    )))

    assert calls == [['prepare']]
    assert received[-1] == ('final', '工作流已通过真实工具调用完成。')


def test_guard_does_not_misread_do_not_stop_as_a_stop_request():
    for query in ['不要停止，继续执行', '不要暂停，继续执行']:
        state = {'completed': False}
        calls = []

        def get_ready_steps():
            return {
                'ready_steps': [] if state['completed'] else ['prepare'],
                'ready_step_details': [] if state['completed'] else [{
                    'step_id': 'prepare',
                    'mode': 'auto',
                    'requires_approval': False,
                    'execution_tool': 'advance_step',
                }],
                'projection': {'completed': state['completed']},
            }

        def advance_step(step_ids):
            calls.append(step_ids)
            state['completed'] = True
            return {'status': 'completed', 'outcome': 'workflow_completed'}

        received = asyncio.run(_collect(guard_workflow_agent_stream(
            _stream(('event', {'tag': 'tool_results', 'tool_results': [{
                'name': 'trigger_writer_workflow',
                'result': {'ok': True, 'value': {'session_id': 'session-1'}},
            }]}), ('final', '稍后继续。')),
            all_tools=[
                _tool('get_ready_steps', get_ready_steps),
                _tool('advance_step', advance_step),
            ],
            runtime_prompt='## Explicit Workflow Selection [AUTHORITATIVE]',
            query=query,
        )))

        assert calls == [['prepare']]
        assert received[-1] == ('final', '工作流已通过真实工具调用完成。')


def test_guard_does_not_claim_waiting_state_change_was_executed():
    calls = []

    def get_ready_steps():
        return {
            'ready_steps': ['prepare'],
            'ready_step_details': [{
                'step_id': 'prepare',
                'mode': 'auto',
                'requires_approval': False,
                'default_approval': 'not_required',
                'execution_tool': 'advance_step',
            }],
            'projection': {'completed': False},
        }

    def advance_step(step_ids):
        calls.append(step_ids)
        return {
            'status': 'waiting',
            'outcome': 'workflow_state_changed',
            'user_notice': '状态已变化，本次没有提交步骤。',
        }

    received = asyncio.run(_collect(guard_workflow_agent_stream(
        _stream(('final', 'prepare 已执行。')),
        all_tools=[
            _tool('get_ready_steps', get_ready_steps),
            _tool('advance_step', advance_step),
        ],
        runtime_prompt='## Workflow Runtime [AUTHORITATIVE]',
        query='继续',
    )))

    assert calls == [['prepare']]
    assert received[-1] == ('final', '工作流未能继续执行：状态已变化，本次没有提交步骤。')
    assert all('已实际执行' not in str(item) for item in received)
    synthetic_result = next(
        item[1]['tool_results'][0]['result']
        for item in received
        if item[0] == 'event' and item[1].get('tag') == 'tool_results'
    )
    assert synthetic_result['ok'] is False


def test_guard_does_not_turn_retry_into_a_forward_ready_step():
    def get_ready_steps():
        raise AssertionError('retry must not be replaced with a normal Ready advance')

    received = asyncio.run(_collect(guard_workflow_agent_stream(
        _stream(('event', {'tag': 'text', 'delta': '重试完成。'}), ('final', '重试完成。')),
        all_tools=[
            _tool('get_ready_steps', get_ready_steps),
            _tool('advance_step', lambda _step_ids: None),
        ],
        runtime_prompt='## Workflow Runtime [AUTHORITATIVE]',
        query='重试工作流',
    )))

    assert received == [
        ('event', {
            'tag': 'text',
            'delta': '重试必须使用 Runtime 返回的可重试目标；未自动推进普通 Ready 步骤。',
        }),
        ('final', '重试必须使用 Runtime 返回的可重试目标；未自动推进普通 Ready 步骤。'),
    ]
