from lazymind.chat.engine.agent_runtime.executor import ToolCallGuard


def _call(name, arguments):
    return {'function': {'name': name, 'arguments': arguments}}


class _RecordingToolManager:
    def __init__(self):
        self.calls = []

    def __call__(self, calls, verbose=False):
        self.calls.extend(calls)
        return [
            {'ok': True, 'value': call['function']['arguments']}
            for call in calls
        ]


def test_exact_duplicate_tool_calls_are_reused_across_batches():
    manager = _RecordingToolManager()
    guard = ToolCallGuard(manager, {'url_fetch': 2})

    first = guard([
        _call('url_fetch', '{"url":"https://example.com","mode":"text"}'),
    ])
    second = guard([
        _call('url_fetch', '{"mode":"text","url":"https://example.com"}'),
    ])

    assert second == first
    assert len(manager.calls) == 1


def test_exact_duplicate_tool_calls_in_one_batch_are_merged():
    manager = _RecordingToolManager()
    guard = ToolCallGuard(manager, {'url_fetch': 2})

    results = guard([
        _call('url_fetch', {'url': 'https://example.com'}),
        _call('url_fetch', {'url': 'https://example.com'}),
    ])

    assert results[0] == results[1]
    assert len(manager.calls) == 1


def test_tool_call_limit_blocks_different_guesses_after_budget():
    manager = _RecordingToolManager()
    guard = ToolCallGuard(manager, {'url_fetch': 2})

    guard([_call('url_fetch', {'url': 'https://one.example'})])
    guard([_call('url_fetch', {'url': 'https://two.example'})])
    blocked = guard([_call('url_fetch', {'url': 'https://three.example'})])

    assert len(manager.calls) == 2
    assert blocked[0]['ok'] is False
    assert '[Tool Call Limit]' in blocked[0]['msg']


def test_unconfigured_stateful_tool_is_not_deduplicated():
    manager = _RecordingToolManager()
    guard = ToolCallGuard(manager, {'url_fetch': 2})

    guard([_call('get_task_status', {'task_id': 'task-1'})])
    guard([_call('get_task_status', {'task_id': 'task-1'})])

    assert len(manager.calls) == 2
