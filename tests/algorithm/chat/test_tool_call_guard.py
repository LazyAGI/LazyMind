from lazymind.chat.engine.agent_runtime.executor import ToolCallGuard


def _call(name, arguments):
    return {'function': {'name': name, 'arguments': arguments}}


class _RecordingToolManager:
    def __init__(self, result_factory=None):
        self.calls = []
        self.result_factory = result_factory or (
            lambda call: {'ok': True, 'value': call['function']['arguments']}
        )

    def __call__(self, calls, verbose=False):
        self.calls.extend(calls)
        return [self.result_factory(call) for call in calls]


def test_successful_calls_are_never_limited_or_cached():
    manager = _RecordingToolManager()
    guard = ToolCallGuard(manager, {'url_fetch': 2})

    for index in range(20):
        guard([_call('url_fetch', {'url': f'https://example.com/{index}'})])
    guard([_call('url_fetch', {'url': 'https://example.com/0'})])

    assert len(manager.calls) == 21


def test_exact_duplicate_tool_calls_in_one_batch_are_merged():
    manager = _RecordingToolManager()
    guard = ToolCallGuard(manager, {'url_fetch': 2})

    results = guard([
        _call('url_fetch', {'url': 'https://example.com'}),
        _call('url_fetch', {'url': 'https://example.com'}),
    ])

    assert results[0] == results[1]
    assert len(manager.calls) == 1


def test_repeated_exact_failure_is_blocked_without_reexecution():
    manager = _RecordingToolManager(
        lambda _: {'ok': False, 'value': None, 'msg': 'network error'},
    )
    guard = ToolCallGuard(manager, {'url_fetch': 2})

    guard([_call('url_fetch', {'url': 'https://one.example'})])
    blocked = guard([_call('url_fetch', {'url': 'https://one.example'})])

    assert len(manager.calls) == 1
    assert blocked[0]['ok'] is False
    assert '[Repeated Tool Failure]' in blocked[0]['msg']


def test_different_parameter_guesses_are_blocked_after_consecutive_failures():
    manager = _RecordingToolManager(
        lambda _: {'ok': False, 'value': None, 'msg': 'network error'},
    )
    guard = ToolCallGuard(manager, {'url_fetch': 2})

    guard([_call('url_fetch', {'url': 'https://one.example'})])
    guard([_call('url_fetch', {'url': 'https://two.example'})])
    blocked = guard([_call('url_fetch', {'url': 'https://three.example'})])

    assert len(manager.calls) == 2
    assert '[Repeated Tool Failure]' in blocked[0]['msg']


def test_success_resets_consecutive_failure_count():
    outcomes = iter([False, True, False, False])
    manager = _RecordingToolManager(
        lambda _: {'ok': next(outcomes), 'value': None},
    )
    guard = ToolCallGuard(manager, {'url_fetch': 2})

    for index in range(4):
        guard([_call('url_fetch', {'url': f'https://example.com/{index}'})])

    assert len(manager.calls) == 4


def test_unconfigured_stateful_tool_is_not_deduplicated():
    manager = _RecordingToolManager()
    guard = ToolCallGuard(manager, {'url_fetch': 2})

    guard([_call('get_task_status', {'task_id': 'task-1'})])
    guard([_call('get_task_status', {'task_id': 'task-1'})])

    assert len(manager.calls) == 2
