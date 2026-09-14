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


@pytest.mark.parametrize('mode,method,arguments,asks', [
    ('always_ask', 'read', {'filepath': 'notes.txt'}, False),
    ('always_ask', 'create', {'filepath': 'created.txt', 'content': 'x'}, True),
    ('ask_as_needed', 'create', {'filepath': 'created.txt', 'content': 'x'}, False),
    ('ask_as_needed', 'delete', {'filepath': 'notes.txt'}, False),
    ('allow_all', 'delete', {'filepath': 'notes.txt'}, False),
])
def test_algorithm_snapshot_decides_workspace_policy_before_core(
        workspace_runtime, tmp_path, mode, method, arguments, asks):
    root = tmp_path / 'workspace'
    root.mkdir()
    (root / 'notes.txt').write_text('notes')
    middleware, core, _ = workspace_runtime(root, permission_mode=mode)
    if method == 'delete':
        assert middleware.execute_with_records(call('read', filepath='notes.txt')).results[0]['ok']
        core.events.clear()

    result = middleware.execute_with_records(call(method, **arguments))

    assert result.results[0]['ok'], result.results
    assert bool(core.events) is asks


@pytest.mark.parametrize('mode,asks', [
    ('always_ask', False), ('ask_as_needed', False), ('allow_all', False),
])
def test_algorithm_snapshot_treats_sensitive_reads_by_mode(workspace_runtime, tmp_path, mode, asks):
    root = tmp_path / 'workspace'
    root.mkdir()
    (root / '.env').write_text('SECRET=value')
    middleware, core, _ = workspace_runtime(root, permission_mode=mode)

    result = middleware.execute_with_records(call('read', filepath='.env'))

    assert result.results[0]['ok'], result.results
    assert bool(core.events) is asks


def test_algorithm_policy_denies_git_metadata_before_core(workspace_runtime, tmp_path):
    root = tmp_path / 'workspace'
    (root / '.git').mkdir(parents=True)
    (root / '.git' / 'config').write_text('private')
    middleware, core, _ = workspace_runtime(root, permission_mode='allow_all')

    result = middleware.execute_with_records(call('read', filepath='.git/config'))

    assert not result.results[0]['ok']
    assert core.events == []


@pytest.mark.parametrize('mode', ['always_ask', 'ask_as_needed', 'allow_all'])
@pytest.mark.parametrize('method', ['create', 'delete'])
def test_algorithm_policy_denies_sensitive_mutations_before_core(
        workspace_runtime, tmp_path, mode, method):
    root = tmp_path / 'workspace'
    root.mkdir()
    target = root / '.env'
    arguments = {'filepath': '.env'}
    if method == 'create':
        arguments['content'] = 'SECRET=new'
    else:
        target.write_text('SECRET=old')
    middleware, core, _ = workspace_runtime(root, permission_mode=mode)

    result = middleware.execute_with_records(call(method, **arguments))

    assert not result.results[0]['ok']
    assert core.events == []
    if method == 'delete':
        assert target.read_text() == 'SECRET=old'
    else:
        assert not target.exists()


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
    assert len({payload['call_id'] for payload in payloads}) == 2
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
    middleware, core, config = workspace_runtime(execution_identity={
        'task_id': 'task', 'generation': 'generation', 'attempt_id': 'attempt', 'lease_token': 'test-only-lease',
    })
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


def test_completed_claim_receipt_never_becomes_empty_success_for_asked_path(workspace_runtime, tmp_path):
    middleware, core, _ = workspace_runtime()
    target = tmp_path / 'outside-empty.txt'
    target.write_text('')
    first = middleware.execute_with_records(call('read', filepath=str(target)))
    assert first.results[0]['ok'] and first.results[0]['value']['content'] == ''
    core.on_claim = lambda operation: operation.update(status='completed')
    result = middleware.execute_with_records(call('read', filepath=str(target)))
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
    from lazyllm.tools.agent import ToolManager, HostFileAccess
    declarations = {tool.name: tool.runtime_metadata.host_file_access
                    for tool in ToolManager([LocalFileToolkit()]).all_tools}
    for method in LocalFileToolkit.__public_apis__:
        assert declarations['LocalFileToolkit_' + method] is HostFileAccess.DECLARED
    assert all(capability is HostFileAccess.NONE for name, capability in declarations.items()
               if not name.startswith('LocalFileToolkit_'))
    assert not hasattr(LocalFileToolkit, '_core_operation')
    assert not hasattr(module, 'WorkspaceSkillFS')
    source = (Path(__file__).resolve().parents[3] / 'algorithm/lazymind/chat/engine/agent_runtime/executor.py').read_text()
    assert 'WorkspaceSkillFS' not in source


def test_run_permission_is_immutable_and_not_read_from_later_globals(workspace_runtime):
    import lazyllm

    middleware, core, config = workspace_runtime()
    captured = middleware._workspace_permission
    config['workspace_context']['workspace_id'] = 'forged'
    config['_workspace_execution']['run_id'] = 'another-run'
    lazyllm.globals['agentic_config'] = {'user_id': 'other-owner'}
    result = middleware.execute_with_records(call('create', filepath='frozen.txt', content='ok'))
    assert result.results[0]['ok'], result.results
    payload = next(iter(core.operations.values()))['payload']
    assert payload['workspace_id'] == 'workspace' and payload['run_id'] == 'run'
    assert not hasattr(captured, 'config')
    with pytest.raises((AttributeError, TypeError)):
        captured.permission_mode = 'allow_all'


def test_permission_snapshot_is_typed_and_does_not_read_local_source_protocol(tmp_path):
    from lazymind.chat.engine.tools.workspace_context import WorkspacePermissionContext

    snapshot = WorkspacePermissionContext.from_snapshot(
        {
            'workspace_id': 'workspace',
            'root': str(tmp_path),
            'directory_identity': 'directory',
            'workspace_version': 3,
            'permission_mode': 'always_ask',
            'permission_version': 7,
        },
        user_id='owner',
        conversation_id='conversation',
        execution={'history_id': 'history', 'run_id': 'run'},
    )

    assert snapshot.workspace_id == 'workspace'
    assert snapshot.root == str(tmp_path.resolve())
    assert snapshot.workspace_version == 3
    assert snapshot.permission_mode == 'always_ask'
    assert snapshot.permission_version == 7
    assert snapshot.execution == {'history_id': 'history', 'run_id': 'run'}
    assert not hasattr(snapshot, 'config')


def test_uncalled_undeclared_tool_does_not_block_batch_but_invocation_fails_closed(workspace_runtime):
    from lazyllm.tools.agent import fc_register

    effects = []

    @fc_register(host_file='NONE')
    def safe(value: str):
        '''Record a safe value.

        Args:
            value: Value to record.
        '''
        effects.append(('safe', value))
        return value

    def undeclared(value: str):
        '''An undeclared replacement.

        Args:
            value: Value to record.
        '''
        effects.append(('undeclared', value))
        return value

    middleware, _, _ = workspace_runtime(extra_tools=[safe, undeclared])
    allowed = middleware.execute_with_records({
        'id': 'safe', 'function': {'name': 'safe', 'arguments': {'value': 'ok'}},
    })
    blocked = middleware.execute_with_records({
        'id': 'undeclared', 'function': {'name': 'undeclared', 'arguments': {'value': 'no'}},
    })

    assert allowed.results[0]['ok']
    assert not blocked.results[0]['ok']
    assert effects == [('safe', 'ok')]


def test_opaque_policy_uses_explicit_tool_identity_allowlist(workspace_runtime):
    from lazyllm.tools.agent import fc_register

    effects = []

    @fc_register(host_file='OPAQUE')
    def framework_skill_script(value: str):
        '''Stand in for the framework-owned SkillManager tool.

        Args:
            value: Value to record.
        '''
        effects.append(value)
        return value

    denied, _, _ = workspace_runtime(extra_tools=[framework_skill_script], trusted_local=False)
    denied_result = denied.execute_with_records({
        'id': 'denied', 'function': {'name': 'framework_skill_script', 'arguments': {'value': 'no'}},
    })
    allowed, _, _ = workspace_runtime(
        extra_tools=[framework_skill_script],
        trusted_local=False,
        trusted_opaque_tool_names={'framework_skill_script'},
    )
    allowed_result = allowed.execute_with_records({
        'id': 'allowed', 'function': {'name': 'framework_skill_script', 'arguments': {'value': 'yes'}},
    })

    assert not denied_result.results[0]['ok']
    assert allowed_result.results[0]['ok']
    assert effects == ['yes']


def test_one_batch_request_and_original_tool_binding(workspace_runtime):
    middleware, core, _ = workspace_runtime()
    tool = middleware.tools_info['LocalFileToolkit_create']
    apply = tool.apply
    result = middleware.execute_with_records([
        call('create', filepath='a.txt', content='one'), call('create', filepath='b.txt', content='two'),
    ])
    assert all(item['ok'] for item in result.results)
    assert core.batch_requests == 1
    assert tool.apply is apply


def test_request_contexts_isolate_two_workspaces(workspace_runtime, tmp_path):
    from concurrent.futures import ThreadPoolExecutor
    from lazymind.chat.engine.tools.workspace_context import workspace_permission_scope

    first, _, _ = workspace_runtime(tmp_path / 'first')
    second, _, _ = workspace_runtime(tmp_path / 'second')

    def prepare(runtime):
        with workspace_permission_scope(runtime._workspace_permission):
            return runtime.prepare_tool_calls(call('create', filepath='same.txt', content='x'))[0]

    with ThreadPoolExecutor(max_workers=2) as pool:
        calls = list(pool.map(prepare, [first, second]))
    assert [item.host_files[0].path for item in calls] == [
        str(tmp_path / 'first' / 'same.txt'), str(tmp_path / 'second' / 'same.txt'),
    ]
