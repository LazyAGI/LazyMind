import lazyllm

from lazymind.chat.engine.agent_runtime.executor import ToolCallGuard


def _call(name, arguments):
    return {'function': {'name': name, 'arguments': arguments}}


def _failure(category, tool='search'):
    return {
        'ok': False,
        'value': None,
        'error': {
            'category': category,
            'code': f'{category}_CODE',
            'tool': tool,
            'message': 'tool failed',
            'retryable': True,
            'recovery_attempts_remaining': 1,
            'details': {},
        },
    }


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
    assert blocked[0]['error']['category'] == 'DOMAIN_FAILURE'
    assert blocked[0]['error']['code'] == 'REPEATED_TOOL_FAILURE'


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


def test_transient_failure_can_be_retried_by_agent_but_is_not_auto_retried():
    transient = _failure('TRANSIENT_ERROR', 'url_fetch')
    manager = _RecordingToolManager(lambda _: transient)
    guard = ToolCallGuard(manager, {'url_fetch': 2})
    call = _call('url_fetch', {'url': 'https://example.com'})

    first = guard([call])

    assert first == [transient]
    assert len(manager.calls) == 1

    second = guard([call])

    assert second == [transient]
    assert len(manager.calls) == 2


def test_invalid_args_and_unknown_tool_each_allow_one_recovery_attempt():
    cases = (
        ('INVALID_ARGS', _call('search', {'limit': 'many'}), _call('search', {'limit': []})),
        ('UNKNOWN_TOOL', _call('seach', {}), _call('serch', {})),
    )
    for category, first_call, second_call in cases:
        manager = _RecordingToolManager(lambda _, category=category: _failure(category))
        guard = ToolCallGuard(manager)

        first = guard([first_call])[0]
        second = guard([second_call])[0]

        assert first['error']['recovery_attempts_remaining'] == 1
        assert second['error']['recovery_attempts_remaining'] == 0


def test_unconfigured_stateful_tool_is_not_deduplicated():
    manager = _RecordingToolManager()
    guard = ToolCallGuard(manager, {'url_fetch': 2})

    guard([_call('get_task_status', {'task_id': 'task-1'})])
    guard([_call('get_task_status', {'task_id': 'task-1'})])

    assert len(manager.calls) == 2


def test_plugin_or_subagent_tool_immediately_updates_runtime_round_limit():
    previous = lazyllm.locals.get('_lazyllm_agent')
    workspace = {}
    lazyllm.locals['_lazyllm_agent'] = {'workspace': workspace}
    try:
        guard = ToolCallGuard(_RecordingToolManager(), expanded_round_limit=200)

        guard([_call('trigger_writer', {})])

        assert workspace['_react_round_limit'] == 200
    finally:
        if previous is None:
            lazyllm.locals.pop('_lazyllm_agent', None)
        else:
            lazyllm.locals['_lazyllm_agent'] = previous


def test_ordinary_tools_do_not_enable_expanded_budget():
    previous = lazyllm.locals.get('_lazyllm_agent')
    workspace = {}
    lazyllm.locals['_lazyllm_agent'] = {'workspace': workspace}
    try:
        guard = ToolCallGuard(_RecordingToolManager(), expanded_round_limit=200)

        guard([_call('url_fetch', {'url': 'https://example.com'})])

        assert '_react_round_limit' not in workspace
    finally:
        if previous is None:
            lazyllm.locals.pop('_lazyllm_agent', None)
        else:
            lazyllm.locals['_lazyllm_agent'] = previous
