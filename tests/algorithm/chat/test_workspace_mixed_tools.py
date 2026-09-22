"""Real owner tools use the generic host-access approval/claim/complete path."""
import lazyllm

from lazymind.chat.engine.subagent.context import SubAgentContext
from lazymind.chat.engine.subagent.tools import save_artifacts
from lazymind.chat.engine.tools.workspace_context import ToolResolutionContext, WorkspaceContext


def artifact_call(source):
    return {'id': 'artifact-call', 'function': {'name': 'save_artifacts', 'arguments': {
        'artifacts': [{'key': 'result', 'content_type': 'file', 'value': {'path': str(source)}}],
    }}}


def artifact_runtime(workspace_runtime, tmp_path, monkeypatch):
    task = tmp_path / 'task'
    task.mkdir()
    emitted = []
    context = SubAgentContext(task_id='task', conversation_id='conversation', agent_type='test',
                              objective='save', params={}, workspace_path=str(task),
                              input_slots=[], output_slots=['result'], db=None, emit=emitted.append)
    lazyllm.globals['subagent_ctx'] = lazyllm.globals.get('subagent_ctx')
    monkeypatch.setitem(lazyllm.globals, 'subagent_ctx', context)
    middleware, core, config = workspace_runtime(extra_tools=[save_artifacts])
    tool_context = {**config, '_subagent_workspace': str(task)}
    middleware._workspace_permission = WorkspaceContext.from_config(tool_context, trusted_local=True)
    middleware._tool_context = ToolResolutionContext(managed_roots=(str(task.resolve()),))
    return middleware, core, task, emitted


def test_real_artifact_read_uses_local_guard_without_core(workspace_runtime, tmp_path, monkeypatch):
    source = tmp_path / 'outside.txt'
    source.write_text('external artifact')
    middleware, core, task, emitted = artifact_runtime(workspace_runtime, tmp_path, monkeypatch)
    result = middleware.execute_with_records(artifact_call(source))
    assert result.results[0]['ok'], result.results
    assert (task / source.name).read_text() == 'external artifact'
    assert emitted and not core.events


def test_denied_artifact_preserves_existing_target(workspace_runtime, tmp_path, monkeypatch):
    source = tmp_path / 'outside.txt'
    source.write_text('new')
    middleware, core, task, emitted = artifact_runtime(workspace_runtime, tmp_path, monkeypatch)
    (task / source.name).write_text('existing')
    middleware._authorization_gate = lambda _: 'deny'
    result = middleware.execute_with_records(artifact_call(source))
    assert not result.results[0]['ok'] and not emitted and not core.events
    assert (task / source.name).read_text() == 'existing'


def test_artifact_context_change_during_batch_approval_fails_before_copy(workspace_runtime, tmp_path, monkeypatch):
    source = tmp_path / 'outside.txt'
    source.write_text('new')
    middleware, core, task, emitted = artifact_runtime(workspace_runtime, tmp_path, monkeypatch)
    other = tmp_path / 'other-task'
    other.mkdir()
    def approve(operation):
        assert not emitted
        lazyllm.globals['subagent_ctx'].workspace_path = str(other)
        operation.update(status='allowed', decision='allowed')
    core.on_poll = approve
    result = middleware.execute_with_records([
        artifact_call(source),
        {'function': {'name': 'write', 'arguments': {'path': str(tmp_path / 'out'), 'content': 'x'}}},
    ])
    assert not result.results[0]['ok'] and not emitted
    assert not (other / source.name).exists()


def test_read_path_change_during_batch_approval_never_executes(workspace_runtime, tmp_path):
    original, other = tmp_path / 'image.png', tmp_path / 'other.png'
    original.write_bytes(b'original')
    other.write_bytes(b'other')
    middleware, core, _ = workspace_runtime()
    def approve(operation):
        original.unlink()
        original.symlink_to(other)
        operation.update(status='allowed', decision='allowed')
    core.on_poll = approve
    result = middleware.execute_with_records([
        {'function': {'name': 'read', 'arguments': {'path': str(original)}}},
        {'function': {'name': 'write', 'arguments': {'path': str(tmp_path / 'out'), 'content': 'x'}}},
    ])
    assert not result.results[0]['ok']
    assert other.read_bytes() == b'other'


def test_external_unbound_snapshot_allows_task_artifacts_not_host_writes(workspace_runtime, tmp_path, monkeypatch):
    middleware, core, task, emitted = artifact_runtime(workspace_runtime, tmp_path, monkeypatch)
    call = {'function': {'name': 'save_artifacts', 'arguments': {'artifacts': [
        {'key': 'result', 'content_type': 'text', 'value': 'analysis'},
    ]}}}
    # Regression: omitting the Core snapshot denies even task-owned text artifacts.
    middleware._workspace_permission = WorkspaceContext.from_snapshot(None, local_runtime=True)
    assert not middleware.execute_with_records(call).results[0]['ok']
    assert not emitted
    # Match Core's ordinary unbound snapshot, without granting a host workspace.
    middleware._workspace_permission = WorkspaceContext.from_snapshot(
        {'workspace_id': '', 'workspace_version': 0, 'permission_mode': 'always_ask', 'permission_version': 1},
        local_runtime=True, user_id='owner', conversation_id='',
    )
    result = middleware.execute_with_records(call)
    assert result.results[0]['ok'], result.results
    assert emitted and not core.events
    outside = tmp_path / 'external-write.txt'
    denied = middleware.execute_with_records({'function': {'name': 'write', 'arguments': {
        'path': str(outside), 'content': 'must not write',
    }}})
    assert not denied.results[0]['ok']
    assert not outside.exists() and not core.events
