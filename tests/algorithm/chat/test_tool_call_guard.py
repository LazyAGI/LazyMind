import lazyllm

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


def test_tool_calls_log_name_args_and_window_result():
    manager = _RecordingToolManager(
        lambda _: {
            'ok': True,
            'value': {
                'success': True,
                'tool': 'read_file',
                'result': {
                    'text': 'should-not-appear-in-log',
                    'target': 'paper_text.txt',
                    'offset': 1,
                    'end_line': 80,
                    'total_lines': 4374,
                    'eof': False,
                    'kind': 'attachment_text',
                    'footer': 'Showing lines 1-80 of 4374.\nUse offset=81 to continue.',
                },
            },
        },
    )
    guard = ToolCallGuard(manager)
    recorded: list[str] = []
    original_info = lazyllm.LOG.info
    lazyllm.LOG.info = lambda message, *args, **kwargs: recorded.append(str(message))
    try:
        guard([_call('read_file', {'target': 'paper_text.txt', 'offset': 1, 'limit': 80})])
    finally:
        lazyllm.LOG.info = original_info

    joined = '\n'.join(recorded)
    assert '[event=start]' in joined
    assert '[name=read_file]' in joined
    assert 'paper_text.txt' in joined
    assert '[event=done]' in joined
    assert '[offset=1]' in joined
    assert '[total_lines=4374]' in joined
    assert '[eof=False]' in joined
    assert 'should-not-appear-in-log' not in joined


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
