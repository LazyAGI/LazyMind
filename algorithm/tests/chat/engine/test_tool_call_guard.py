from dataclasses import dataclass

from lazymind.chat.engine.agent_runtime.tool_call_guard import ToolCallGuard


@dataclass(frozen=True)
class _Access:
    counts_as_progress: bool = False
    polling: bool = False


class _Manager:
    def __init__(self):
        self.call_count = 0

    def resolve_tool_accesses(self, calls, allowed_tool_names=None):
        return [_Access() for _ in calls]

    def __call__(self, calls, verbose=False, allowed_tool_names=None):
        self.call_count += len(calls)
        return [{'ok': True, 'value': {'results': ['grounded']}} for _ in calls]


def _call(call_id: str):
    return {'id': call_id, 'function': {'name': 'search', 'arguments': {'query': 'same'}}}


def test_identical_calls_are_executed_and_notice_is_consumed_once():
    manager = _Manager()
    guard = ToolCallGuard(manager)

    for index in range(3):
        result = guard([_call(f'call-{index}')])
        assert result[0]['ok'] is True

    assert manager.call_count == 3
    assert guard.consume_internal_runtime_notices(['call-2'])
    assert guard.consume_internal_runtime_notices(['call-2']) == []
