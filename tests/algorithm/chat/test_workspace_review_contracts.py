"""PR #710: production middleware -> approval protocol -> real local file IO."""
import copy
import hashlib
from pathlib import Path

import pytest
from lazyllm.tools.agent import ToolExecutionDisposition

from lazymind.chat.engine.agent_runtime.cancellation import UserCancelledError


def call(method, **arguments):
    return {'id': 'same-provider-id', 'function': {
        'name': 'LocalFileToolkit_' + method, 'arguments': arguments,
    }}


def test_external_read_uses_core_approval_without_custom_gate(workspace_runtime, tmp_path):
    target = tmp_path / 'outside.txt'
    target.write_text('outside content')
    middleware, core, _ = workspace_runtime()
    batch = middleware.execute_with_records(call('read', filepath=str(target)))
    assert batch.results[0]['ok'], batch.results
    assert batch.results[0]['value']['content'] == 'outside content'
    assert batch.results[0]['value']['filepath'] == str(target.resolve())
    assert [action for action, _ in core.events] == ['prepare', 'approve', 'claim', 'complete']


@pytest.mark.parametrize('mode', ['always_ask', 'ask_as_needed', 'allow_all'])
def test_external_write_waits_for_decision_and_checks_version(workspace_runtime, tmp_path, mode):
    target = tmp_path / 'outside.txt'
    target.write_text('before')
    middleware, core, _ = workspace_runtime(permission_mode=mode)
    read = middleware.execute_with_records(call('read', filepath=str(target)))
    assert read.results[0]['ok']

    def approve(operation):
        assert target.read_text() == 'before'
        operation.update(status='allowed', decision='allowed')

    core.on_poll = approve
    changed = middleware.execute_with_records(call(
        'string_replace', filepath=str(target), old_string='before', new_string='after',
    ))
    assert changed.results[0]['ok'], changed.results
    assert target.read_text() == 'after'
    assert core.operations[next(reversed(core.operations))]['payload']['expected_version'] == hashlib.sha256(b'before').hexdigest()


@pytest.mark.parametrize('decision', ['rejected', 'expired'])
def test_external_rejection_or_expiry_has_no_file_effect(workspace_runtime, tmp_path, decision):
    target = tmp_path / 'outside.txt'
    target.write_text('unchanged')
    middleware, core, _ = workspace_runtime()
    core.on_poll = lambda operation: operation.update(status=decision, decision='denied')
    batch = middleware.execute_with_records(call('overwrite', filepath=str(target), content='bad',
                                                  expected_version=hashlib.sha256(b'unchanged').hexdigest()))
    assert not batch.results[0]['ok']
    assert target.read_text() == 'unchanged'
    assert all(action != 'claim' for action, _ in core.events)


def test_all_asks_are_prepared_before_any_tool_executes(workspace_runtime, tmp_path):
    from lazymind.chat.engine.tools.calculator import calculator
    middleware, core, _ = workspace_runtime(extra_tools=[calculator])
    target1, target2 = tmp_path / 'workspace' / 'a.txt', tmp_path / 'b.txt'
    ordinary_effects = []
    tool = middleware.tools_info['calculator']
    original = tool.apply

    def observe(*args, **kwargs):
        ordinary_effects.append(True)
        return original(*args, **kwargs)

    tool.apply = observe

    def approve(operation):
        assert len(core.operations) == 2
        assert not ordinary_effects and not target1.exists() and not target2.exists()
        operation.update(status='allowed', decision='allowed')

    core.on_poll = approve
    batch = middleware.execute_with_records([
        {'id': 'ordinary', 'function': {'name': 'calculator', 'arguments': {'expression': '1+1'}}},
        call('create', filepath=str(target1), content='a'),
        call('create', filepath=str(target2), content='b'),
    ])
    assert all(result['ok'] for result in batch.results), batch.results
    assert ordinary_effects == [True]
    actions = [action for action, _ in core.events]
    assert actions[:4] == ['prepare', 'prepare', 'approve', 'approve']
    assert target1.read_text() == 'a' and target2.read_text() == 'b'


def test_prepared_calls_keep_ids_order_and_sequential_versions(workspace_runtime, tmp_path):
    from lazymind.chat.engine.tools.calculator import calculator
    target = tmp_path / 'workspace' / 'notes.txt'
    middleware, core, _ = workspace_runtime(extra_tools=[calculator])
    target.write_text('seed')
    batch = middleware.execute_with_records([
        call('read', filepath='notes.txt'),
        call('append', filepath='notes.txt', content='+'),
        {'id': 'ordinary', 'function': {'name': 'calculator', 'arguments': {'expression': '1+1'}}},
        call('append', filepath='notes.txt', content='+'),
    ])
    assert all(result['ok'] for result in batch.results), batch.results
    assert target.read_text() == 'seed++'
    assert [record.index for record in batch.records] == [0, 1, 2, 3]
    assert all(record.disposition is ToolExecutionDisposition.EXECUTED for record in batch.records)
    payloads = [operation['payload'] for operation in core.operations.values()]
    assert len({payload['call_id'] for payload in payloads}) == 3
    assert all(payload['path'] == str(target.resolve()) for payload in payloads)
    assert payloads[1]['depends_on'] in core.operations
    requests = len(core.events)
    forbidden = middleware.execute_with_records(call('append', filepath='notes.txt', content='+'), allowed_tool_names=set())
    assert not forbidden.results[0]['ok'] and len(core.events) == requests


def test_approval_unavailable_and_cancel_block_whole_batch(workspace_runtime, tmp_path):
    from lazymind.chat.engine.tools.calculator import calculator
    middleware, core, _ = workspace_runtime(extra_tools=[calculator])
    def unavailable(_):
        raise OSError('private transport details')
    core.on_poll = unavailable
    tools = [call('create', filepath='notes.txt', content='x'),
             {'id': 'calc', 'function': {'name': 'calculator', 'arguments': {'expression': '2+2'}}}]
    result = middleware.execute_with_records(tools)
    assert all(not value['ok'] for value in result.results)
    assert all('private' not in str(value) for value in result.results)
    assert not (tmp_path / 'workspace' / 'notes.txt').exists()
    core.on_poll = lambda _: (_ for _ in ()).throw(UserCancelledError('stopped'))
    with pytest.raises(UserCancelledError):
        middleware.execute_with_records(tools)


def test_lease_is_bound_to_payload_and_absent_from_status_query(workspace_runtime):
    middleware, core, config = workspace_runtime()
    config['_workspace_execution'] = {
        'task_id': 'task', 'generation': 'generation', 'attempt_id': 'attempt', 'lease_token': 'test-only-lease',
    }
    result = middleware.execute_with_records(call('create', filepath='notes.txt', content='x'))
    assert result.results[0]['ok'], result.results
    assert next(iter(core.operations.values()))['payload']['lease_token'] == 'test-only-lease'
    assert 'test-only-lease' not in str(result.results)


@pytest.mark.parametrize('method,args', [
    ('glob', {'pattern': '**/*.txt'}), ('grep', {'pattern': 'needle'}),
    ('ls', {}), ('info', {}),
])
def test_approved_external_directory_results_use_absolute_paths(workspace_runtime, tmp_path, method, args):
    outside = tmp_path / 'outside'
    outside.mkdir()
    (outside / 'notes.txt').write_text('needle')
    middleware, _, _ = workspace_runtime()
    result = middleware.execute_with_records(call(method, path=str(outside), **args))
    assert result.results[0]['ok'], result.results
    value = result.results[0]['value']
    assert value['path'] == str(outside.resolve())
    if method in {'glob', 'grep'}:
        assert value['match_count'] == 1
    if method == 'ls':
        assert value['entries'][0]['path'] == str((outside / 'notes.txt').resolve())
    assert all(key not in str(value) for key in ('source_id', 'workspace_id', 'permission_version'))


def test_sensitive_files_and_links_cannot_be_searched_indirectly(workspace_runtime, tmp_path):
    root = tmp_path / 'workspace'
    middleware, _, _ = workspace_runtime(root)
    (root / 'notes.txt').write_text('needle public')
    (root / '.env').write_text('needle private')
    outside = tmp_path / 'outside.txt'
    outside.write_text('needle outside')
    (root / 'link.txt').symlink_to(outside)
    result = middleware.execute_with_records(call('grep', pattern='needle'))
    assert result.results[0]['ok'], result.results
    assert [entry['content'] for entry in result.results[0]['value']['matches']] == ['needle public']


def test_file_identity_change_during_approval_is_rejected(workspace_runtime, tmp_path):
    target = tmp_path / 'outside.txt'
    target.write_text('approved')
    middleware, core, _ = workspace_runtime()
    def approve(operation):
        replacement = tmp_path / 'replacement.txt'
        replacement.write_text('not approved')
        replacement.replace(target)
        operation.update(status='allowed', decision='allowed')
    core.on_poll = approve
    result = middleware.execute_with_records(call('read', filepath=str(target)))
    assert not result.results[0]['ok']
    assert 'not approved' not in str(result.results)


def test_failed_file_writes_are_not_exempt_from_failure_budget(workspace_runtime):
    middleware, core, _ = workspace_runtime()
    result = middleware.execute_with_records(call('append', filepath='missing.txt', content='+'))
    assert not result.results[0]['ok']
    requests = len(core.events)
    result = middleware.execute_with_records(call('append', filepath='missing.txt', content='different'))
    assert not result.results[0]['ok']
    assert len(core.events) == requests


def test_completed_claim_receipt_never_becomes_empty_success(workspace_runtime, tmp_path):
    middleware, core, _ = workspace_runtime()
    target = tmp_path / 'workspace' / 'empty.txt'
    target.write_text('')
    first = middleware.execute_with_records(call('read', filepath='empty.txt'))
    assert first.results[0]['ok'] and first.results[0]['value']['content'] == ''
    core.on_claim = lambda operation: operation.update(status='completed')
    result = middleware.execute_with_records(call('read', filepath='empty.txt'))
    assert not result.results[0]['ok']


@pytest.mark.parametrize('count', [0, -1, 101, True])
def test_invalid_replacement_count_does_not_reach_core(workspace_runtime, count):
    middleware, core, _ = workspace_runtime()
    result = middleware.execute_with_records(call('string_replace', filepath='notes.txt',
                                                  old_string='old', new_string='new', expected_replacements=count))
    assert not result.results[0]['ok']
    assert core.events == []


def test_normalization_keeps_original_provider_arguments_unchanged(workspace_runtime):
    middleware, _, _ = workspace_runtime()
    original = call('create', filepath='notes.txt', content='x')
    snapshot = copy.deepcopy(original)
    assert middleware.execute_with_records(original).results[0]['ok']
    assert original == snapshot


def test_toolkit_has_no_core_executor_or_skill_fs():
    from lazymind.chat.engine.tools.local_fs import LocalFileToolkit
    import lazymind.chat.engine.tools.local_fs as module
    assert LocalFileToolkit.__host_file_access__ == 'DECLARED'
    assert not hasattr(LocalFileToolkit, '_core_operation')
    assert not hasattr(module, 'WorkspaceSkillFS')
    source = (Path(__file__).resolve().parents[3] / 'algorithm/lazymind/chat/engine/agent_runtime/executor.py').read_text()
    assert 'WorkspaceSkillFS' not in source
