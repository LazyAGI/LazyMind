import copy
import json

import lazyllm
import pytest
from lazyllm.tools.agent import (
    PreparedToolCall,
    ResolvedToolAccess,
    ToolExecutionDisposition,
    ToolExecutionRecord,
)
from lazymind.chat.engine.agent_runtime.tool_call_guard import (
    ExactRepeatMonitor,
    FailureRetryPolicy,
    ToolExecutionMiddleware,
)


def _prepared(name='search', arguments=None, call_id='call-1', access=None):
    arguments = {'query': 'same'} if arguments is None else arguments
    return PreparedToolCall(
        tool_call={
            'id': call_id,
            'function': {'name': name, 'arguments': json.dumps(arguments)},
        },
        call_id=call_id,
        tool_name=name,
        arguments=arguments,
        validated_arguments=arguments,
        access=access or ResolvedToolAccess(),
    )


def _record(*, result=None, disposition=ToolExecutionDisposition.EXECUTED, **kwargs):
    return ToolExecutionRecord(
        _prepared(**kwargs),
        result if result is not None else {'ok': True, 'value': {'items': ['same']}},
        disposition=disposition,
    )


def _notice(delta):
    return [item.content for item in delta.model_context]


@pytest.mark.parametrize('result', [
    {'ok': True, 'value': {'items': ['same']}},
    {'ok': False, 'msg': 'same failure'},
])
def test_third_identical_observation_emits_soft_notice_without_mutating_result(result):
    monitor = ExactRepeatMonitor()
    monitor.begin_run({})
    notices = []
    for index in range(5):
        record = _record(call_id=f'call-{index}', result=result)
        assert record.result is result
        notices.append(_notice(monitor.after_tool_batch([record])))

    assert notices[:2] == [[], []]
    assert all(
        f'{count} consecutive times' in notices[count - 1][0]
        for count in (3, 4, 5)
    )


def test_ordered_multi_tool_batch_repeats_and_order_change_resets_streak():
    monitor = ExactRepeatMonitor()
    batch = [
        _record(name='a', arguments={'value': 1}, call_id='a'),
        _record(name='b', arguments={'value': 2}, call_id='b'),
    ]

    assert not _notice(monitor.after_tool_batch(batch))
    assert not _notice(monitor.after_tool_batch(batch))
    assert _notice(monitor.after_tool_batch(batch))
    assert not _notice(monitor.after_tool_batch(list(reversed(batch))))


def test_three_identical_calls_in_one_batch_emit_one_notice():
    monitor = ExactRepeatMonitor()
    records = [_record(call_id=f'call-{index}') for index in range(3)]

    notices = _notice(monitor.after_tool_batch(records))

    assert len(notices) == 1
    assert '3 consecutive times' in notices[0]


@pytest.mark.parametrize('change', ['arguments', 'result'])
def test_arguments_or_result_change_resets_streak(change):
    monitor = ExactRepeatMonitor()
    base = _record(call_id='one')
    monitor.after_tool_batch([base])
    monitor.after_tool_batch([base])
    changed = _record(
        call_id='changed',
        arguments={'query': 'changed'} if change == 'arguments' else None,
        result={'ok': True, 'value': {'items': ['changed']}} if change == 'result' else None,
    )

    assert not _notice(monitor.after_tool_batch([changed]))
    assert not _notice(monitor.after_tool_batch([changed]))


@pytest.mark.parametrize('access', [
    ResolvedToolAccess(write_keys=frozenset({('exact', 'shared')})),
    ResolvedToolAccess(exclusive=True),
])
def test_write_and_exclusive_failures_are_not_exempt(access):
    monitor = ExactRepeatMonitor()
    failure = {'ok': False, 'msg': 'approval_required'}

    for index in range(2):
        assert not _notice(monitor.after_tool_batch([
            _record(call_id=f'call-{index}', access=access, result=failure),
        ]))
    assert _notice(monitor.after_tool_batch([
        _record(call_id='call-3', access=access, result=failure),
    ]))


def test_polling_records_are_excluded_and_hard_blocked_records_are_ignored():
    monitor = ExactRepeatMonitor()
    polling = ResolvedToolAccess(polling=True)
    assert not _notice(monitor.after_tool_batch([
        _record(access=polling),
        _record(name='blocked', disposition=ToolExecutionDisposition.POLICY_BLOCKED),
    ]))

    for index in range(3):
        delta = monitor.after_tool_batch([
            _record(name='stable', call_id=f'stable-{index}'),
            _record(name='poll', call_id=f'poll-{index}', access=polling),
        ])
    assert _notice(delta)


def _failing_manager(calls):
    from lazyllm.tools import ToolManager

    def search(query: str):
        '''Fail a search.

        Args:
            query: Search query.
        '''
        calls.append(query)
        raise RuntimeError('failed')

    return ToolManager([search])


def _call(query, call_id):
    return {
        'id': call_id,
        'function': {'name': 'search', 'arguments': {'query': query}},
    }


def test_failure_policy_blocks_same_failed_signature_without_monitoring_block():
    calls = []
    manager = _failing_manager(calls)
    middleware = ToolExecutionMiddleware(
        manager,
        failure_policy=FailureRetryPolicy({'search': 2}),
    )

    first = middleware.execute_with_records(_call('same', 'first'))
    second = middleware.execute_with_records(_call('same', 'second'))

    assert first.records[0].executed is True
    assert second.records[0].executed is False
    assert second.records[0].disposition is ToolExecutionDisposition.POLICY_BLOCKED
    assert second.results[0]['ok'] is False
    assert calls == ['same']
    assert not _notice(ExactRepeatMonitor().after_tool_batch(second.records))


def test_failure_policy_preserves_consecutive_budget_and_batch_merge():
    calls = []
    manager = _failing_manager(calls)
    middleware = ToolExecutionMiddleware(
        manager,
        failure_policy=FailureRetryPolicy({'search': 2}),
    )

    first = middleware.execute_with_records([
        _call('one', 'one'),
        _call('one', 'duplicate'),
    ])
    second = middleware.execute_with_records([
        _call('two', 'two'),
    ])
    third = middleware.execute_with_records([
        _call('three', 'three'),
    ])

    assert [record.executed for record in first.records] == [True, False]
    assert first.records[1].disposition is ToolExecutionDisposition.DEDUPLICATED
    assert first.results[0] == first.results[1]
    assert second.records[0].executed is True
    assert third.records[0].executed is False
    assert calls == ['one', 'two']


@pytest.mark.parametrize(('function', 'allowed_names'), [
    ({'name': 'search', 'arguments': {}}, None),
    ({'name': 'search', 'arguments': '{"query":'}, None),
    ({'name': 'missing', 'arguments': {}}, None),
    ({'name': 'search', 'arguments': {'query': 'hidden'}}, set()),
])
def test_identical_preparation_failures_trigger_repeat_notice(function, allowed_names):
    calls = []
    middleware = ToolExecutionMiddleware(_failing_manager(calls))
    monitor = ExactRepeatMonitor()
    notices = []

    for index in range(4):
        batch = middleware.execute_with_records({
            'id': f'bad-{index}',
            'function': copy.deepcopy(function),
        }, allowed_tool_names=allowed_names)
        assert batch.records[0].disposition is ToolExecutionDisposition.PREPARATION_FAILED
        notices.append(_notice(monitor.after_tool_batch(batch.records)))

    assert notices[:2] == [[], []]
    assert len(notices[2]) == len(notices[3]) == 1
    assert calls == []


def test_round_expansion_only_applies_to_ready_scheduled_calls(monkeypatch):
    from lazyllm.tools import ToolManager

    def create_subagent(task: str):
        '''Create a subagent.

        Args:
            task: Task description.
        '''
        return task

    workspace = {}
    monkeypatch.setitem(lazyllm.locals, '_lazyllm_agent', {'workspace': workspace})
    middleware = ToolExecutionMiddleware(
        ToolManager([create_subagent]),
        expanded_round_limit=200,
    )

    invalid = middleware.execute_with_records({
        'id': 'invalid',
        'function': {'name': 'create_subagent', 'arguments': {}},
    })
    assert invalid.records[0].disposition is ToolExecutionDisposition.PREPARATION_FAILED
    assert '_react_round_limit' not in workspace

    middleware.execute_with_records({
        'id': 'ready',
        'function': {'name': 'create_subagent', 'arguments': {'task': 'inspect'}},
    })
    assert workspace['_react_round_limit'] == 200
