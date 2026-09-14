"""Real owner tools use the generic host-access approval/claim/complete path."""
import lazyllm

from lazymind.chat.engine.subagent.context import SubAgentContext
from lazymind.chat.engine.subagent.tools import save_artifacts
from lazymind.chat.engine.tools.workspace_context import WorkspacePermissionContext


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
    middleware._workspace_permission = WorkspacePermissionContext.from_config(
        {**config, '_subagent_workspace': str(task)}, trusted_local=True)
    return middleware, core, task, emitted


def test_real_artifact_save_external_file_approves_claims_executes_completes(workspace_runtime, tmp_path, monkeypatch):
    source = tmp_path / 'outside.txt'
    source.write_text('external artifact')
    middleware, core, task, emitted = artifact_runtime(workspace_runtime, tmp_path, monkeypatch)
    destination = task / source.name

    def approve(operation):
        assert not destination.exists() and not emitted
        operation.update(status='allowed', decision='allowed')

    def claim(operation):
        assert not destination.exists() and not emitted

    def complete(operation):
        assert destination.read_text() == 'external artifact'
        assert emitted[0]['value']['path'] == str(destination)

    core.on_poll, core.on_claim, core.on_complete = approve, claim, complete
    result = middleware.execute_with_records(artifact_call(source))
    assert result.results[0]['ok'], result.results
    assert [action for action, _ in core.events] == ['prepare', 'approve', 'claim', 'complete']
    assert core.batch_requests == 1
    assert next(iter(core.operations.values()))['payload']['execution_mode'] == 'host_access'
    assert source.read_text() == 'external artifact'


def test_artifact_rejection_preserves_existing_target(workspace_runtime, tmp_path, monkeypatch):
    source = tmp_path / 'outside.txt'
    source.write_text('new')
    middleware, core, task, emitted = artifact_runtime(workspace_runtime, tmp_path, monkeypatch)
    destination = task / source.name
    destination.write_text('existing')
    core.on_poll = lambda operation: operation.update(status='rejected', decision='denied')
    result = middleware.execute_with_records(artifact_call(source))
    assert not result.results[0]['ok']
    assert destination.read_text() == 'existing'
    assert source.read_text() == 'new'
    assert not emitted
    assert [action for action, _ in core.events] == ['prepare', 'approve']


def test_artifact_context_switch_during_approval_fails_before_copy(workspace_runtime, tmp_path, monkeypatch):
    source = tmp_path / 'outside.txt'
    source.write_text('new')
    middleware, core, task, emitted = artifact_runtime(workspace_runtime, tmp_path, monkeypatch)
    other = tmp_path / 'other-task'
    other.mkdir()

    def approve(operation):
        lazyllm.globals['subagent_ctx'].workspace_path = str(other)
        operation.update(status='allowed', decision='allowed')

    core.on_poll = approve
    result = middleware.execute_with_records(artifact_call(source))
    assert not result.results[0]['ok']
    assert not (task / source.name).exists() and not (other / source.name).exists()
    assert not emitted


def test_generic_input_replaced_by_symlink_during_approval_never_executes(workspace_runtime, tmp_path):
    from lazyllm.tools.agent import fc_register, HostFileIntent, HostFileResolution

    original, secret = tmp_path / 'image.png', tmp_path / 'secret.png'
    original.write_bytes(b'original')
    secret.write_bytes(b'secret')
    effects = []

    @fc_register(host_file_access='DECLARED', host_file_resolver=lambda args: HostFileResolution(
        args, (HostFileIntent(args['path'], 'read'),)))
    def consume(path: str):
        '''Read an approved image.

        Args:
            path: Absolute image path.
        '''
        effects.append(True)
        return __import__('pathlib').Path(path).read_bytes()

    middleware, core, _ = workspace_runtime(extra_tools=[consume])

    def approve(operation):
        original.unlink()
        original.symlink_to(secret)
        operation.update(status='allowed', decision='allowed')

    core.on_poll = approve
    result = middleware.execute_with_records({'function': {'name': 'consume', 'arguments': {'path': str(original)}}})
    assert not result.results[0]['ok'] and not effects
    assert secret.read_bytes() == b'secret'
    assert not any(action == 'claim' for action, _ in core.events)
