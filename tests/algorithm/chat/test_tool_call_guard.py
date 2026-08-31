from lazymind.chat.engine.agent_runtime.executor import ToolCallGuard


def _call(name, arguments):
    return {'function': {'name': name, 'arguments': arguments}}


def _failure(tool='search'):
    return {'ok': False, 'value': f'{tool} failed'}


class _RecordingToolManager:
    def __init__(self, result_factory=None):
        self.calls = []
        self.allowed_tool_names = []
        self.result_factory = result_factory or (
            lambda call: {'ok': True, 'value': call['function']['arguments']}
        )

    def __call__(self, calls, verbose=False, allowed_tool_names=None):
        self.calls.extend(calls)
        self.allowed_tool_names.append(allowed_tool_names)
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


def test_same_batch_duplicates_do_not_consume_repeated_call_budget():
    manager = _RecordingToolManager()
    guard = ToolCallGuard(manager, {'url_fetch': 2}, repeated_call_limit=2)
    call = _call('url_fetch', {'url': 'https://example.com'})

    first = guard([call, call, call])
    second = guard([call])

    assert first[0] == first[1] == first[2]
    assert second[0]['ok'] is True
    assert len(manager.calls) == 2


def test_allowed_tool_names_are_forwarded_to_pending_calls():
    manager = _RecordingToolManager()
    guard = ToolCallGuard(manager)
    allowed = {'url_fetch'}

    guard([_call('url_fetch', {'url': 'https://example.com'})], allowed_tool_names=allowed)

    assert manager.allowed_tool_names == [allowed]


def test_repeated_exact_failure_respects_consecutive_failure_limit():
    manager = _RecordingToolManager(
        lambda _: _failure('url_fetch'),
    )
    guard = ToolCallGuard(manager, {'url_fetch': 2})

    guard([_call('url_fetch', {'url': 'https://one.example'})])
    blocked = guard([_call('url_fetch', {'url': 'https://one.example'})])

    assert len(manager.calls) == 1
    assert blocked[0]['ok'] is False
    assert '[Repeated Tool Failure]' in blocked[0]['value']
    assert 'already failed' in blocked[0]['value']


def test_different_parameter_guesses_are_blocked_after_consecutive_failures():
    manager = _RecordingToolManager(
        lambda _: _failure('url_fetch'),
    )
    guard = ToolCallGuard(manager, {'url_fetch': 2})

    guard([_call('url_fetch', {'url': 'https://one.example'})])
    guard([_call('url_fetch', {'url': 'https://two.example'})])
    blocked = guard([_call('url_fetch', {'url': 'https://three.example'})])

    assert len(manager.calls) == 2
    assert '[Repeated Tool Failure]' in blocked[0]['value']


def test_success_resets_consecutive_failure_count():
    outcomes = iter([False, True, False, False])
    manager = _RecordingToolManager(
        lambda _: {'ok': next(outcomes), 'value': None},
    )
    guard = ToolCallGuard(manager, {'url_fetch': 2})

    for index in range(4):
        guard([_call('url_fetch', {'url': f'https://example.com/{index}'})])

    assert len(manager.calls) == 4


def test_success_for_other_arguments_resets_consecutive_failure_count():
    def result(call):
        if call['function']['arguments']['url'].endswith('/failed'):
            return _failure('url_fetch')
        return {'ok': True, 'value': 'loaded'}

    manager = _RecordingToolManager(result)
    guard = ToolCallGuard(manager, {'url_fetch': 3})
    failed_call = _call('url_fetch', {'url': 'https://example.com/failed'})

    guard([failed_call])
    guard([_call('url_fetch', {'url': 'https://example.com/success'})])
    result = guard([failed_call])

    assert len(manager.calls) == 3
    assert result == [_failure('url_fetch')]


def test_failure_can_be_retried_by_agent_until_failure_limit():
    failure = _failure('url_fetch')
    manager = _RecordingToolManager(lambda _: failure)
    guard = ToolCallGuard(manager, {'url_fetch': 2})
    call = _call('url_fetch', {'url': 'https://example.com'})

    first = guard([call])

    assert first == [failure]
    assert len(manager.calls) == 1

    blocked = guard([call])

    assert '[Repeated Tool Failure]' in blocked[0]['value']
    assert 'already failed' in blocked[0]['value']
    assert len(manager.calls) == 1


def test_guard_preserves_failure_message():
    cases = (
        (_call('search', {'limit': 'many'}), _call('search', {'limit': []})),
        (_call('seach', {}), _call('serch', {})),
    )
    for first_call, second_call in cases:
        manager = _RecordingToolManager(lambda _: _failure())
        guard = ToolCallGuard(manager)

        first = guard([first_call])[0]
        second = guard([second_call])[0]

        assert first == _failure()
        assert second == _failure()


def test_unconfigured_stateful_tool_is_not_deduplicated():
    manager = _RecordingToolManager()
    guard = ToolCallGuard(manager, {'url_fetch': 2})

    guard([_call('get_task_status', {'task_id': 'task-1'})])
    guard([_call('get_task_status', {'task_id': 'task-1'})])

    assert len(manager.calls) == 2
