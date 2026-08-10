from unittest.mock import MagicMock

from lazymind.chat.engine.agent_runtime.executor import ToolCallGuard


def _call(name: str = 'search', query: str = 'same'):
    return {'function': {'name': name, 'arguments': {'query': query}}}


def test_exact_successful_call_is_blocked_after_limit_even_when_interleaved():
    manager = MagicMock(side_effect=lambda calls, verbose=False: [
        {'ok': True, 'value': {'results': ['grounded']}} for _ in calls
    ])
    guard = ToolCallGuard(manager, repeated_call_limit=3)

    for _ in range(3):
        assert guard([_call()])[0]['ok'] is True
        assert guard([_call(query='different')])[0]['ok'] is True

    blocked = guard([_call()])[0]
    assert blocked['ok'] is False
    assert 'exact same call was already made 3 times' in blocked['msg']
    assert manager.call_count == 6


def test_distinct_arguments_remain_allowed():
    manager = MagicMock(side_effect=lambda calls, verbose=False: [
        {'ok': True, 'value': {}} for _ in calls
    ])
    guard = ToolCallGuard(manager, repeated_call_limit=3)

    for index in range(10):
        assert guard([_call(query=f'query-{index}')])[0]['ok'] is True
