"""Unified filesystem tools through the production approval middleware."""
from dataclasses import replace
import pytest
from lazymind.chat.engine.agent_runtime.cancellation import UserCancelledError


def call(method, **arguments):
    return {'id': 'provider-id', 'function': {'name': method, 'arguments': arguments}}


@pytest.mark.parametrize('mode', ['always_ask', 'ask_as_needed', 'allow_all'])
@pytest.mark.parametrize('outside', [False, True])
@pytest.mark.parametrize('operation', ['read', 'write', 'remove'])
def test_permission_matrix(workspace_runtime, tmp_path, mode, outside, operation):
    middleware, core, config = workspace_runtime(permission_mode=mode)
    root = tmp_path if outside else tmp_path / 'workspace'
    target = root / 'notes.txt'
    target.write_text('before')
    arguments = {'path': str(target)}
    if operation == 'write':
        arguments['content'] = 'after'
    result = middleware.execute_with_records(call(operation, **arguments))
    assert result.results[0]['ok'], result.results
    asks = operation != 'read' and (mode == 'always_ask' or mode == 'ask_as_needed' and outside)
    assert any(action == 'approve' for action, _ in core.events) == asks
    assert [action for action, _ in core.events if action != 'approve'] == ['prepare', 'claim', 'complete']
    if operation == 'remove':
        assert not target.exists()
    else:
        assert target.read_text() == ('after' if operation == 'write' else 'before')


@pytest.mark.parametrize('decision', ['rejected', 'expired'])
def test_rejected_or_expired_call_has_no_effect(workspace_runtime, tmp_path, decision):
    middleware, core, _ = workspace_runtime()
    core.on_poll = lambda value: value.update(status=decision, decision='denied')
    target = tmp_path / 'outside.txt'
    result = middleware.execute_with_records(call('write', path=str(target), content='x'))
    assert not result.results[0]['ok']
    assert not target.exists()
    assert not any(action == 'claim' for action, _ in core.events)


def test_unbound_context_uses_internal_cwd_and_asks(workspace_runtime, tmp_path):
    middleware, core, _ = workspace_runtime()
    middleware._workspace_permission = replace(middleware._workspace_permission,
        workspace_id='', root='', workspace_version=0, cwd=str(tmp_path))
    result = middleware.execute_with_records(call('write', path='result.txt', content='x'))
    assert result.results[0]['ok'], result.results
    assert (tmp_path / 'result.txt').read_text() == 'x'
    assert any(action == 'approve' for action, _ in core.events)
    assert next(iter(core.operations.values()))['payload']['workspace_id'] == ''


def test_cancellation_before_execution_has_no_effect(workspace_runtime, tmp_path):
    cancelled = False
    def check(_):
        if cancelled:
            raise UserCancelledError()
    middleware, core, _ = workspace_runtime(cancel_check=check)
    def approve(value):
        nonlocal cancelled
        cancelled = True
        value.update(status='allowed', decision='allowed')
    core.on_poll = approve
    with pytest.raises(UserCancelledError):
        middleware.execute_with_records(call('write', path=str(tmp_path / 'out'), content='x'))
    assert not (tmp_path / 'out').exists()


def test_move_requires_every_intent_before_execution(workspace_runtime, tmp_path):
    middleware, core, _ = workspace_runtime()
    source, target = tmp_path / 'source.txt', tmp_path / 'target.txt'
    source.write_text('data')
    def approve(value):
        assert source.exists() and not target.exists()
        value.update(status='rejected' if value['payload']['operation'] == 'write' else 'allowed',
                     decision='denied' if value['payload']['operation'] == 'write' else 'allowed')
    core.on_poll = approve
    result = middleware.execute_with_records(call('move', src=str(source), dst=str(target)))
    assert not result.results[0]['ok']
    assert source.read_text() == 'data' and not target.exists()
    assert core.batch_requests == 1 and len(core.operations) == 3


def test_path_replaced_during_approval_is_not_written(workspace_runtime, tmp_path):
    middleware, core, _ = workspace_runtime()
    target, other = tmp_path / 'target', tmp_path / 'other'
    target.write_text('original')
    other.write_text('protected')
    def approve(value):
        target.unlink()
        target.symlink_to(other)
        value.update(status='allowed', decision='allowed')
    core.on_poll = approve
    result = middleware.execute_with_records(call('write', path=str(target), content='changed'))
    assert not result.results[0]['ok']
    assert other.read_text() == 'protected'


def test_context_and_prepared_arguments_are_immutable(workspace_runtime, tmp_path):
    middleware, core, config = workspace_runtime()
    def approve(value):
        config['workspace_context']['root'] = str(tmp_path)
        value.update(status='allowed', decision='allowed')
    core.on_poll = approve
    request = call('write', path='out.txt', content='x')
    result = middleware.execute_with_records(request)
    assert result.results[0]['ok'], result.results
    assert (tmp_path / 'workspace' / 'out.txt').read_text() == 'x'
    assert not (tmp_path / 'out.txt').exists()
    assert request['function']['arguments']['path'] == 'out.txt'


@pytest.mark.parametrize('path', ['.git/config', '.env', '.ssh/key'])
def test_protected_writes_are_denied_before_core(workspace_runtime, tmp_path, path):
    middleware, core, _ = workspace_runtime(permission_mode='allow_all')
    result = middleware.execute_with_records(call('write', path=path, content='x'))
    assert not result.results[0]['ok'] and not core.events


def test_same_file_batch_keeps_order_and_all_updates(workspace_runtime, tmp_path):
    middleware, core, _ = workspace_runtime()
    target = tmp_path / 'notes.txt'
    result = middleware.execute_with_records([
        call('write', path=str(target), content='a', mode='create'),
        call('write', path=str(target), content='b', mode='append'),
        call('read', path=str(target)),
    ])
    assert all(item['ok'] for item in result.results), result.results
    assert target.read_text() == 'ab'
    assert result.results[2]['value']['content'] == 'ab'
