from dataclasses import dataclass

import pytest

from lazymind.chat.engine.agent_runtime.executor import ToolCallGuard
from lazymind.chat.engine.agent_runtime.tool_call_guard import ToolCallGuard as ExtractedToolCallGuard


@dataclass(frozen=True)
class _Access:
    counts_as_progress: bool = False
    polling: bool = False


def _call(name='search', arguments=None, call_id='call-1'):
    return {
        'id': call_id,
        'function': {'name': name, 'arguments': arguments or {'query': 'same'}},
    }


class _RecordingToolManager:
    def __init__(self, result=None, accesses=None):
        self.calls = []
        self.result = result if result is not None else {'ok': True, 'value': {'items': ['same']}}
        self.accesses = accesses

    def normalize_tool_calls(self, calls):
        return calls

    def resolve_tool_accesses(self, calls, allowed_tool_names=None):
        if self.accesses is not None:
            return self.accesses[:len(calls)]
        return [_Access() for _ in calls]

    def __call__(self, calls, verbose=False, allowed_tool_names=None):
        self.calls.extend(calls)
        return [self.result for _ in calls]


@pytest.mark.parametrize('result', [
    {'ok': True, 'value': {'items': ['same']}},
    {'ok': False, 'msg': 'same failure'},
])
def test_third_identical_result_queues_soft_notice_without_changing_results(result):
    assert ToolCallGuard is ExtractedToolCallGuard
    manager = _RecordingToolManager(result=result)
    guard = ToolCallGuard(manager)
    call = _call()

    returned = [guard([call]) for _ in range(3)]

    assert returned == [[result], [result], [result]]
    assert len(manager.calls) == 3
    notice = guard.consume_internal_runtime_notices(['call-1'])
    assert len(notice) == 1
    assert '3 consecutive times' in notice[0]


def test_all_batch_duplicates_execute_and_notice_binds_to_ordered_batch_ids():
    manager = _RecordingToolManager()
    guard = ToolCallGuard(manager)
    calls = [
        _call(call_id='first'),
        _call(call_id='second'),
        _call(call_id='third'),
    ]

    results = guard(calls)

    assert len(manager.calls) == 3
    assert len(results) == 3
    assert guard.consume_internal_runtime_notices(['first', 'second', 'third'])


@pytest.mark.parametrize('change_result', [False, True])
def test_result_or_arguments_change_resets_consecutive_state(change_result):
    manager = _RecordingToolManager()
    guard = ToolCallGuard(manager)

    guard([_call(call_id='one')])
    guard([_call(call_id='two')])
    arguments = {'query': 'same'}
    if change_result:
        manager.result = {'ok': True, 'value': {'items': ['changed']}}
    else:
        arguments = {'query': 'changed'}
    guard([_call(arguments=arguments, call_id='changed-1')])
    guard([_call(arguments=arguments, call_id='changed-2')])

    assert guard.consume_internal_runtime_notices(['changed-2']) == []


@pytest.mark.parametrize('access', [
    _Access(counts_as_progress=True),
    _Access(polling=True),
])
def test_progress_and_polling_calls_are_exempt(access):
    manager = _RecordingToolManager(accesses=[access])
    guard = ToolCallGuard(manager)

    for index in range(4):
        guard([_call(call_id=f'call-{index}')])

    assert len(manager.calls) == 4
    assert guard.consume_internal_runtime_notices(['call-3']) == []
