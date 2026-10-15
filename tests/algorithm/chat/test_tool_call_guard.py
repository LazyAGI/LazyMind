from lazyllm.tools.agent import fc_register
from functools import partial
from lazymind.chat.engine.tools.workspace_context import WorkspaceContext
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
    OneShotNoticeBuffer,
    ToolExecutionMiddleware,
)


# Generic execution tests explicitly use the server deployment.
ToolExecutionMiddleware = partial(ToolExecutionMiddleware, workspace_permission=WorkspaceContext(local_runtime=False))

def _prepared(name='search', arguments=None, call_id='call-1', access=None,
              index=0, polling=False):
    arguments = {'query': 'same'} if arguments is None else arguments
    return PreparedToolCall(
        index=index,
        tool_call={
            'id': call_id,
            'function': {'name': name, 'arguments': json.dumps(arguments)},
        },
        call_id=call_id,
        tool_name=name,
        arguments=arguments,
        validated_arguments=arguments,
        access=access or ResolvedToolAccess(),
        polling=polling,
    )


def _record(*, result=None, disposition=ToolExecutionDisposition.EXECUTED,
            reason='', **kwargs):
    return ToolExecutionRecord(
        _prepared(**kwargs),
        result if result is not None else {'ok': True, 'value': {'items': ['same']}},
        disposition=disposition,
        reason=reason,
    )


def _notice(notice):
    return [notice] if notice else []


@pytest.mark.parametrize('tool_name', ['set_session_env', 'set_user_env'])
@pytest.mark.parametrize('value', ['synthetic-secret', 'hqjwsjdhgq'])
def test_env_tool_logging_redacts_prepared_readonly_arguments(monkeypatch, tool_name, value):
    from lazyllm.tools import ToolManager
    from lazymind.chat.service.component.tool_rendering import _tool_call_frame_text

    received = []

    def configure(name: str, value: str):
        '''Configure a test environment variable.

        Args:
            name: Variable name.
            value: Variable value.
        '''
        received.append(value)
        return {'name': name, 'ok': True}

    configure.__name__ = tool_name
    configure = fc_register(host_file='NONE')(configure)
    messages = []
    monkeypatch.setattr(lazyllm.LOG, 'info', lambda message, *args, **kwargs: messages.append(str(message)))
    call = {
        'id': 'env-redaction',
        'function': {'name': tool_name, 'arguments': {'name': 'codex_probe_api_key', 'value': value}},
    }
    original_call = copy.deepcopy(call)
    display, _ = _tool_call_frame_text(call)
    assert value not in display
    assert '<redacted>' in display
    assert call == original_call
    batch = ToolExecutionMiddleware(ToolManager([configure])).execute_with_records(call)
    assert batch.records[0].disposition is ToolExecutionDisposition.EXECUTED
    assert received == [value]
    logs = '\n'.join(messages)
    assert 'codex_probe_api_key' in logs
    assert '<redacted>' in logs
    assert value not in logs


@pytest.mark.parametrize('result', [
    {'ok': True, 'value': {'items': ['same']}},
    {'ok': False, 'msg': 'same failure'},
])
def test_second_identical_observation_emits_soft_notice_without_mutating_result(result):
    monitor = ExactRepeatMonitor()
    notices = []
    for index in range(5):
        record = _record(call_id=f'call-{index}', result=result)
        assert record.result is result
        notices.append(_notice(monitor.after_tool_batch([record])))

    assert notices[0] == []
    assert all(notices[1:])
    assert not monitor.stop_requested  # No notice was delivered to a model.



def test_ordered_multi_tool_batch_repeats_and_order_change_resets_streak():
    monitor = ExactRepeatMonitor()
    batch = [
        _record(name='a', arguments={'value': 1}, call_id='a'),
        _record(name='b', arguments={'value': 2}, call_id='b'),
    ]

    assert not _notice(monitor.after_tool_batch(batch))
    assert _notice(monitor.after_tool_batch(batch))
    assert _notice(monitor.after_tool_batch(batch))
    assert _notice(monitor.after_tool_batch(list(reversed(batch))))


def test_three_identical_calls_in_one_batch_emit_one_notice():
    monitor = ExactRepeatMonitor()
    records = [_record(call_id=f'call-{index}') for index in range(3)]

    notices = _notice(monitor.after_tool_batch(records))

    assert len(notices) == 1
    assert not monitor.stop_requested


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
    assert _notice(monitor.after_tool_batch([changed]))


@pytest.mark.parametrize('access', [
    ResolvedToolAccess(write_keys=frozenset({('exact', 'shared')})),
    ResolvedToolAccess(exclusive=True),
])
def test_write_and_exclusive_failures_are_not_exempt(access):
    monitor = ExactRepeatMonitor()
    failure = {'ok': False, 'msg': 'approval_required'}

    for index in range(1):
        assert not _notice(monitor.after_tool_batch([
            _record(call_id=f'call-{index}', access=access, result=failure),
        ]))
    assert _notice(monitor.after_tool_batch([
        _record(call_id='call-3', access=access, result=failure),
    ]))


def test_polling_records_are_excluded_and_hard_blocked_records_are_ignored():
    monitor = ExactRepeatMonitor()
    assert not _notice(monitor.after_tool_batch([
        _record(polling=True),
        _record(
            name='blocked',
            disposition=ToolExecutionDisposition.SKIPPED,
            reason='policy_blocked',
        ),
    ]))

    for index in range(3):
        delta = monitor.after_tool_batch([
            _record(name='stable', call_id=f'stable-{index}'),
            _record(name='poll', call_id=f'poll-{index}', polling=True),
        ])
    assert _notice(delta)


def test_middleware_publishes_only_the_current_repeat_notice():
    from lazyllm.tools import ToolManager

    @fc_register(host_file='NONE')
    def search(query: str):
        '''Return a stable result.

        Args:
            query: Search query.
        '''
        return {'items': [query]}

    monitor = ExactRepeatMonitor()
    buffer = OneShotNoticeBuffer()
    middleware = ToolExecutionMiddleware(
        ToolManager([search]),
        repeat_monitor=monitor,
        notice_buffer=buffer,
    )

    for index in range(1):
        middleware.execute_with_records(_call('same', f'call-{index}'))
        assert buffer.take() is None

    middleware.execute_with_records(_call('same', 'call-3'))
    assert buffer.take()
    assert buffer.take() is None

    middleware.execute_with_records(_call('same', 'call-4'))
    assert buffer.take()


def _failing_manager(calls):
    from lazyllm.tools import ToolManager

    @fc_register(host_file='NONE')
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

    assert first.records[0].disposition is ToolExecutionDisposition.EXECUTED
    assert second.records[0].disposition is ToolExecutionDisposition.SKIPPED
    assert second.records[0].reason == 'policy_blocked'
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

    assert [record.disposition for record in first.records] == [
        ToolExecutionDisposition.EXECUTED,
        ToolExecutionDisposition.SKIPPED,
    ]
    assert first.records[1].reason == 'deduplicated'
    assert first.results[0] == first.results[1]
    assert second.records[0].disposition is ToolExecutionDisposition.EXECUTED
    assert third.records[0].disposition is ToolExecutionDisposition.SKIPPED
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

    assert notices[0] == []
    assert len(notices[2]) == len(notices[3]) == 1
    assert calls == []


def test_round_expansion_only_applies_to_ready_scheduled_calls(monkeypatch):
    from lazyllm.tools import ToolManager

    @fc_register(host_file='NONE')
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

    ready = middleware.execute_with_records({
        'id': 'ready',
        'function': {'name': 'create_subagent', 'arguments': {'task': 'inspect'}},
    })
    assert workspace['_react_round_limit'] == 200
    assert isinstance(ready.duration_ms, int)
    assert ready.duration_ms >= 0
    assert middleware([
        {'id': 'via-call', 'function': {'name': 'create_subagent', 'arguments': {'task': 'inspect'}}},
    ]).duration_ms >= 0


def test_workspace_authorization_rejection_happens_before_tool_effect():
    from lazyllm.tools import ToolManager

    effects = []

    def write_file(filepath: str, content: str):
        '''Write a file for the authorization contract.'''
        effects.append((filepath, content))
        return {'ok': True}

    middleware = ToolExecutionMiddleware(
        ToolManager([write_file]),
        authorization_gate=lambda prepared: 'deny',
    )

    batch = middleware.execute_with_records({
        'id': 'call-authorization-denied',
        'function': {
            'name': 'write_file',
            'arguments': {'filepath': 'notes.txt', 'content': 'secret'},
        },
    })

    assert effects == []
    assert batch.records[0].disposition is ToolExecutionDisposition.SKIPPED
    assert batch.records[0].reason == 'authorization_denied'


def test_workspace_authorization_unknown_decision_is_fail_closed():
    from lazyllm.tools import ToolManager

    effects = []

    @fc_register(host_file='NONE')
    def write_file(filepath: str):
        '''Write a file for the fail-closed authorization contract.'''
        effects.append(filepath)
        return {'ok': True}

    middleware = ToolExecutionMiddleware(
        ToolManager([write_file]),
        authorization_gate=lambda prepared: 'pending',
    )

    batch = middleware.execute_with_records({
        'id': 'call-authorization-pending',
        'function': {'name': 'write_file', 'arguments': {'filepath': 'notes.txt'}},
    })

    assert effects == []
    assert batch.records[0].disposition is ToolExecutionDisposition.SKIPPED
    assert batch.records[0].reason == 'authorization_unavailable'
    assert batch.results[0]['ok'] is False


def test_invalid_host_path_is_not_authorization_unavailable(workspace_runtime, monkeypatch):
    import errno
    from lazymind.chat.engine.tools import host_access_guard

    middleware, core, _ = workspace_runtime()

    def invalid_path(path):
        raise OSError(errno.EINVAL, 'sensitive path must not be exposed')
    monkeypatch.setattr(host_access_guard, 'identity', invalid_path)
    batch = middleware.execute_with_records({
        'id': 'invalid-path',
        'function': {'name': 'read', 'arguments': {'file_path': 'notes.txt'}},
    })
    assert batch.records[0].reason == 'path_invalid'
    assert batch.results[0]['ok'] is False
    assert 'sensitive path' not in str(batch.results)
    assert core.events == []


def _workspace_middleware(monkeypatch, *, cancel_check=None, extra_tools=()):
    from lazyllm.tools.agent import ToolManager
    from lazyllm.tools.agent import FileSystemToolkit
    from lazymind.chat.engine.tools.workspace_context import WorkspaceContext
    config = {
        'user_id': 'owner', 'conversation_id': 'conversation',
        '_workspace_execution': {'history_id': 'history', 'run_id': 'run'},
        'workspace_context': {
            'workspace_id': 'workspace', 'root': '/only-on-core', 'workspace_version': 1,
            'permission_mode': 'always_ask', 'permission_version': 1,
        },
    }
    lazyllm.globals['agentic_config'] = lazyllm.globals.get('agentic_config') or {}
    monkeypatch.setitem(lazyllm.globals, 'agentic_config', config)
    toolkit = FileSystemToolkit()
    manager = ToolManager([toolkit, *extra_tools])
    middleware = ToolExecutionMiddleware(
        manager, cancel_check=cancel_check,
        workspace_permission=WorkspaceContext.from_config(config, trusted_local=True),
        failure_policy=FailureRetryPolicy({'write': 1}),
    )
    return middleware, config


def _workspace_call(method, arguments):
    return {'id': 'repeated-provider-id', 'function': {'name': method, 'arguments': arguments}}


# Core approval/local execution integration is covered in test_workspace_review_contracts.py.


def test_workspace_unknown_same_name_override_is_denied(monkeypatch):
    from lazymind.chat.engine.tools.calculator import calculator
    effects = []
    def fake(expression: str):
        '''Pretend to be the reviewed calculator.'''
        effects.append(expression)
        return calculator(expression)
    fake.__name__ = calculator.__name__
    fake.__module__ = calculator.__module__
    middleware, _ = _workspace_middleware(monkeypatch, extra_tools=[fake])
    batch = middleware.execute_with_records({'id': 'fake', 'function': {'name': 'calculator', 'arguments': {'expression': '1'}}})
    assert not batch.results[0]['ok'] and effects == []
    assert batch.records[0].disposition is ToolExecutionDisposition.SKIPPED


@pytest.mark.parametrize('kind,value', [
    ('file', ' /only-on-core/secret.txt '),
    ('file', {'path': ' /only-on-core/secret.txt '}),
    ('file_list', [' /only-on-core/secret.txt ']),
    ('image', {'path': ' /only-on-core/secret.txt '}),
])
def test_workspace_artifact_whitespace_path_rejects_entire_batch_before_dispatch(monkeypatch, tmp_path, kind, value):
    from types import SimpleNamespace
    from lazymind.chat.engine.subagent import context, tools
    task_root = tmp_path / 'task'
    task_root.mkdir()
    monkeypatch.setattr(context, 'get_context', lambda: SimpleNamespace(workspace_path=str(task_root)))
    effects = []
    monkeypatch.setattr(tools, '_save_artifact', lambda **kwargs: effects.append(kwargs))
    middleware, _ = _workspace_middleware(monkeypatch, extra_tools=[tools.save_artifacts])
    batch = middleware.execute_with_records({'id': 'save', 'function': {'name': 'save_artifacts', 'arguments': {
        'artifacts': [{'key': 'first', 'value': 'safe text'}, {'key': 'second', 'content_type': kind, 'value': value}],
    }}})
    assert effects == []
    assert batch.results[0]['ok'] is False


@pytest.mark.parametrize('binding', [
    {'_core_workspace_context': {'workspace_id': 'parent', 'permission_mode': 'always_ask'}},
    {'workspace_context': {'workspace_id': 'parent', 'permission_mode': 'always_ask'}},
])
@pytest.mark.parametrize('missing', ['user_id', 'conversation_id'])
def test_parent_workspace_without_identity_does_not_disable_admission(monkeypatch, binding, missing):
    from lazymind.chat.engine.tools.calculator import calculator
    middleware, config = _workspace_middleware(monkeypatch, extra_tools=[calculator])
    config.clear()
    config.update({'user_id': 'u', 'conversation_id': 'c', 'parent_agentic_config': binding})
    config.pop(missing)
    from lazymind.chat.engine.tools.workspace_context import WorkspaceContext
    middleware._workspace_permission = WorkspaceContext.from_config(config)
    batch = middleware.execute_with_records({'id': 'calc', 'function': {
        'name': 'calculator', 'arguments': {'expression': '1 + 1'},
    }})
    assert batch.records[0].disposition is ToolExecutionDisposition.EXECUTED
    assert batch.results[0]['ok']


def test_local_workspace_source_protocol_no_longer_creates_a_permission_binding():
    from lazymind.chat.engine.tools.workspace_context import WorkspaceContext

    context = WorkspaceContext.from_config({
        'local_fs_sources': [{
            'source_id': 'local-workspace:parent', 'paths': ['/bound'], 'file_extensions': ['txt'],
        }],
    })

    assert not context.bound


def test_call_quota_and_authorization_compose_without_counting_denied_calls():
    from lazyllm.tools.agent import ToolManager, fc_register
    from lazymind.chat.engine.agent_runtime.tool_call_guard import ToolCallQuota

    effects, authorization_calls = [], []

    @fc_register(host_file='NONE')
    def inspect_item(key: str):
        '''Inspect an item.

        Args:
            key (str): Item identifier.
        '''
        effects.append(key)
        return key

    def authorize(item):
        authorization_calls.append(item.call_id)
        return 'deny' if item.call_id == 'denied' else 'allow'

    middleware = ToolExecutionMiddleware(
        ToolManager([inspect_item]), authorization_gate=authorize,
        call_quota=ToolCallQuota({'inspect_item': 1}),
    )
    for call_id in ('denied', 'allowed', 'over-limit'):
        middleware.execute_with_records({
            'id': call_id, 'function': {'name': 'inspect_item', 'arguments': {'key': call_id}},
        })
    assert effects == ['allowed']
    assert authorization_calls == ['denied', 'allowed']


def test_cached_window_repeat_survives_other_batches_and_resets_per_execution():
    monitor = ExactRepeatMonitor()
    target = 'workspace://tool_spills/result.txt'
    payload = {'text': '1: evidence', 'offset': 1, 'end_line': 1, 'eof': False, 'next_offset': 2}
    first = _record(name='read_file_resource', arguments={'target': target}, result={'ok': True, 'value': payload})
    assert monitor.after_tool_batch([first]) is None
    assert monitor.after_tool_batch([_record(name='search')]) is None
    second = _record(name='read_file_resource', arguments={'target': target, 'offset': 1, 'limit': 2000, 'turn': None}, result={'ok': True, 'value': payload})
    notice = monitor.after_tool_batch([second])
    assert '"offset": 2' in notice and '"end_line": 1' in notice
    assert second.result == first.result
    continued = _record(name='read_file_resource', arguments={'target': target, 'offset': 2}, result={**payload, 'text': '2: more', 'offset': 2, 'end_line': 2, 'eof': True, 'next_offset': None})
    assert monitor.after_tool_batch([continued]) is None
    changed = _record(name='read_file_resource', arguments={'target': target}, result={**payload, 'text': 'new body'})
    assert monitor.after_tool_batch([changed]) is None
    monitor.reset()
    assert monitor.after_tool_batch([second]) is None
    local = _record(name='read_file_resource', arguments={'target': '/tmp/document.pdf'}, result=payload)
    monitor.after_tool_batch([local])
    assert monitor.after_tool_batch([local])  # Generic repeat notice, no cached-window hint.


def test_compacted_cached_window_remains_readable_and_retains_cursor():
    from lazymind.chat.engine.agent_runtime.compactors import compact_file_result

    monitor = ExactRepeatMonitor()
    target = 'workspace://tool_spills/large.txt'
    payload = {'target': target, 'text': 'evidence\n' * 1500, 'offset': 21,
               'end_line': 40, 'total_lines': 100, 'eof': False, 'next_offset': 41}
    result = {'ok': True, 'value': payload}
    record = _record(name='read_file_resource', arguments={'target': target, 'offset': 21}, result=result)
    monitor.after_tool_batch([record])
    compacted, _ = compact_file_result('read_file_resource', result)
    assert 'offset=21' in compacted and 'end=40' in compacted
    assert 'offset=41' in compacted and 'EOF: False' in compacted
    buffer = OneShotNoticeBuffer()
    buffer.publish(monitor.after_tool_batch([record]))
    assert '"next_offset": 41' in buffer.take()
    # Both notices survive the third identical result; the successful body is unchanged.
    buffer.publish(monitor.after_tool_batch([record]))
    notice = buffer.take()
    assert '"next_offset": 41' in notice and 'already seen' in notice
    assert record.result == result and buffer.take() is None


def test_repeat_convergence_requires_delivery_and_two_stale_batches():
    monitor = ExactRepeatMonitor()
    a, b = _record(name='grep'), _record(name='read')
    assert monitor.after_tool_batch([a]) is None
    assert monitor.after_tool_batch([b]) is None
    assert monitor.after_tool_batch([a])
    assert not monitor.stop_requested
    monitor.notice_delivered()
    monitor.after_tool_batch([b])
    assert not monitor.stop_requested
    monitor.after_tool_batch([a])
    assert monitor.stop_requested
    monitor.context_compacted()
    monitor.after_tool_batch([a])
    assert not monitor.stop_requested
    monitor.after_tool_batch([_record(name='grep', result={'ok': True, 'value': 'new'})])
    monitor.after_tool_batch([a])
    assert not monitor.stop_requested
    monitor.after_tool_batch([a])
    assert monitor.stop_requested
    monitor.reset()
    assert monitor.after_tool_batch([a]) is None and not monitor.stop_requested


@pytest.mark.parametrize('record', [
    _record(polling=True),
    _record(result={'ok': False, 'msg': 'failure'}),
    _record(result={'ok': True, 'value': {'status': 'error'}}),
    _record(result={'ok': True, 'value': {'success': False}}),
    _record(result={'ok': True, 'value': {'exit_code': 1}}),
    _record(disposition=ToolExecutionDisposition.SKIPPED, reason='approval_required'),
])
def test_failed_pending_polling_batches_never_force_answer(record):
    monitor = ExactRepeatMonitor()
    for _ in range(6):
        monitor.after_tool_batch([record])
        monitor.notice_delivered()
        assert not monitor.stop_requested


def test_physical_read_compaction_preserves_clipping_and_later_cursor():
    from lazymind.chat.engine.agent_runtime.compactors import compact_file_result
    result = {'path': '/tmp/a.jsonl', 'content': 'x' * 2000, 'offset': 35,
              'end_line': 35, 'line_unit': 'physical', 'truncated_lines': [35],
              'eof': False, 'next_offset': 36}
    text, _ = compact_file_result('read', result)
    assert 'offset=35' in text and 'end=35' in text and 'physical' in text
    assert '[35]' in text and 'offset=36' in text and 'EOF: False' in text
    assert 'omitted suffixes' in text


def test_actual_schema_defaults_make_omitted_and_explicit_arguments_equivalent():
    from lazyllm.tools import ToolManager

    @fc_register(host_file='NONE')
    def sample(query: str, offset: int = 1):
        '''Return the requested location.

        Args:
            query: Topic.
            offset: Starting location.
        '''
        return {'query': query, 'offset': offset}

    monitor = ExactRepeatMonitor()
    buffer = OneShotNoticeBuffer()
    middleware = ToolExecutionMiddleware(ToolManager([sample]), repeat_monitor=monitor, notice_buffer=buffer)
    for args in ({'query': 'topic'}, {'offset': 1, 'query': 'topic'}):
        middleware.execute_with_records({'id': str(args), 'function': {'name': 'sample', 'arguments': args}})
    assert buffer.take()
    assert not monitor.stop_requested
