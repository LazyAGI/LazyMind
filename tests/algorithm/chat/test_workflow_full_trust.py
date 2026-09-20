"""Workflow trust bypasses approval only inside an executor-owned scope."""
import asyncio
import os
import shlex
import subprocess
import sys
from dataclasses import replace

import pytest

from lazymind.chat.engine.tools.workspace_context import WorkspaceContext


def call(name, **arguments):
    return {'function': {'name': name, 'arguments': arguments}}


@pytest.mark.parametrize('bound', [True, False])
def test_full_trust_writes_outside_workspace_without_approval(workspace_runtime, tmp_path, bound):
    middleware, core, _ = workspace_runtime(gate=lambda _: 'deny')
    middleware._workspace_permission = replace(
        middleware._workspace_permission, workflow_full_trust=True,
        active=bound, workspace_id='workspace' if bound else '',
    )
    target = tmp_path / 'outside.txt'
    result = middleware.execute_with_records(call('write', path=str(target), content='workflow'))
    assert result.results[0]['ok'], result.results
    assert target.read_text() == 'workflow'
    assert middleware.execute_with_records(call('read', path=str(target))).results[0]['value']['content'] == 'workflow'
    assert not core.events


def test_full_trust_allows_shell_and_undeclared_tools(workspace_runtime, tmp_path):
    from lazyllm.tools.agent.shell_tool import shell

    def nested_context() -> bool:
        """Check execution context inside the real ToolManager worker."""
        return WorkspaceContext.from_config({}).workflow_full_trust

    middleware, core, _ = workspace_runtime(extra_tools=[shell, nested_context])
    middleware._workspace_permission = replace(middleware._workspace_permission, workflow_full_trust=True)
    target = tmp_path / 'shell-output.txt'
    args = [sys.executable, '-c',
            'import pathlib, sys; pathlib.Path(sys.argv[1]).write_text("trusted")', str(target)]
    cmd = subprocess.list2cmdline(args) if os.name == 'nt' else shlex.join(args)
    result = middleware.execute_with_records(call('shell', cmd=cmd))
    assert result.results[0]['ok'], result.results
    assert result.results[0]['value']['exit_code'] == 0, result.results
    assert target.read_text() == 'trusted'
    assert middleware.execute_with_records(call('nested_context')).results[0]['value'] is True
    assert not core.events
    assert not WorkspaceContext.from_config({}).workflow_full_trust


def test_full_trust_keeps_cancellation(workspace_runtime, tmp_path):
    from lazymind.chat.engine.agent_runtime.cancellation import UserCancelledError

    def cancel(_):
        raise UserCancelledError('cancelled')

    middleware, core, _ = workspace_runtime(cancel_check=cancel)
    middleware._workspace_permission = replace(middleware._workspace_permission, workflow_full_trust=True)
    with pytest.raises(UserCancelledError):
        middleware.execute_with_records(call('write', path=str(tmp_path / 'cancelled'), content='no'))
    assert not (tmp_path / 'cancelled').exists()
    assert not core.events


def test_concurrent_chat_still_requires_approval(workspace_runtime, tmp_path):
    from concurrent.futures import ThreadPoolExecutor
    from lazymind.chat.engine.agent_runtime.tool_call_guard import ToolExecutionMiddleware

    chat, core, _ = workspace_runtime()
    workflow = ToolExecutionMiddleware(
        chat._manager, workspace_permission=replace(chat._workspace_permission, workflow_full_trust=True),
    )

    def reject(operation):
        operation.update(status='rejected', decision='rejected')

    core.on_poll = reject
    with ThreadPoolExecutor(max_workers=2) as pool:
        trusted = pool.submit(workflow.execute_with_records,
                              call('write', path=str(tmp_path / 'workflow'), content='yes'))
        untrusted = pool.submit(chat.execute_with_records,
                                call('write', path=str(tmp_path / 'chat'), content='no'))
        assert trusted.result().results[0]['ok']
        assert not untrusted.result().results[0]['ok']
    assert (tmp_path / 'workflow').read_text() == 'yes'
    assert not (tmp_path / 'chat').exists()
    assert core.batch_requests == 1
    assert all(path == str(tmp_path / 'chat') for _, path in core.events)


def test_full_trust_keeps_invalid_arguments_rejected(workspace_runtime, tmp_path):
    middleware, core, _ = workspace_runtime()
    middleware._workspace_permission = replace(middleware._workspace_permission, workflow_full_trust=True)
    result = middleware.execute_with_records(call('write', path=str(tmp_path / 'invalid')))
    assert not result.results[0]['ok']
    assert not (tmp_path / 'invalid').exists()
    assert not core.events


def test_snapshot_and_model_config_cannot_enable_full_trust():
    assert not WorkspaceContext.from_snapshot({'workflow_full_trust': True}).workflow_full_trust
    assert not WorkspaceContext.from_config({
        'workflow_full_trust': True, 'agent_type': 'workflow_step',
        'parent_agentic_config': {'workflow_full_trust': True},
    }).workflow_full_trust


def test_ordinary_subagent_cannot_execute_workflow_package_top_level(monkeypatch, tmp_path):
    import base64
    from types import SimpleNamespace
    from lazymind.chat.engine.subagent.runner import _resolve_runtime_tools
    from lazymind.workflow_sdk import WorkflowClient

    marker = tmp_path / 'untrusted-code-ran'
    source = f'from pathlib import Path\nPath({str(marker)!r}).touch()\ndef run(): return True\n'
    package = {'revision_id': 'revision', 'tree_hash': 'hash', 'files': {
        'scripts/tools.py': base64.b64encode(source.encode()).decode(),
    }}
    monkeypatch.setattr(WorkflowClient, 'get_workflow', lambda *_: SimpleNamespace(result=package))
    monkeypatch.setattr('tempfile.gettempdir', lambda: str(tmp_path))
    params = {'workflow_id': 'workflow', 'revision_id': 'revision', 'tree_hash': 'hash',
              'workflow_full_trust': True, 'agent_type': 'workflow_step',
              'workspace_context': {'permission_mode': 'always_ask'}}
    with pytest.raises(RuntimeError, match='trusted Workflow execution'):
        _resolve_runtime_tools(['run'], params)
    assert not marker.exists()


@pytest.mark.asyncio
@pytest.mark.parametrize('failure', [RuntimeError, asyncio.CancelledError])
async def test_trust_scope_is_inherited_by_children_but_not_parallel_chat(failure):
    from lazymind.chat.engine.tools.workspace_context import workflow_execution_scope

    async def read_trust():
        await asyncio.sleep(0)
        return WorkspaceContext.from_config({}).workflow_full_trust

    async def workflow():
        with workflow_execution_scope():
            assert await asyncio.create_task(read_trust())
            assert await asyncio.to_thread(lambda: WorkspaceContext.from_config({}).workflow_full_trust)
            raise failure('workflow stopped')

    results = await asyncio.gather(workflow(), read_trust(), return_exceptions=True)
    assert isinstance(results[0], failure)
    assert results[1] is False
    assert not WorkspaceContext.from_config({}).workflow_full_trust


def test_declared_file_tools_available_without_workspace_snapshot():
    from lazymind.chat.engine.subagent.runner import _resolve_runtime_tools
    from lazymind.chat.engine.tools.workspace_context import workflow_execution_scope

    with workflow_execution_scope():
        tools = _resolve_runtime_tools(['read', 'write'], {})
    assert len(tools) == 2


@pytest.mark.asyncio
@pytest.mark.parametrize('backgrounds', ['disabled', 'enabled'])
async def test_real_ppt_package_post_check_preserves_business_result(monkeypatch, tmp_path, backgrounds):
    import base64
    from pathlib import Path
    from types import SimpleNamespace
    from lazymind.chat.engine.subagent import runner
    from lazymind.chat.workflow.remote_executor import RemoteWorkflowExecutor
    from lazymind.chat.engine.tools.workspace_context import workflow_execution_scope
    from lazymind.workflow_sdk import WorkflowClient

    source = Path(__file__).resolve().parents[3] / 'workflows/ppt-workflow/scripts/tools.py'
    package = {'revision_id': 'ppt-revision', 'tree_hash': 'ppt-hash', 'files': {
        'scripts/tools.py': base64.b64encode(source.read_bytes()).decode(),
    }}
    monkeypatch.setattr(WorkflowClient, 'get_workflow', lambda *_: SimpleNamespace(result=package))
    monkeypatch.setattr('tempfile.gettempdir', lambda: str(tmp_path))
    load = runner.load_workflow_tools

    def load_without_image_model(params, names):
        functions = load(params, names)
        monkeypatch.setitem(functions[names[0]].__globals__, 'is_model_role_available', lambda _: False)
        return functions

    monkeypatch.setattr(runner, 'load_workflow_tools', load_without_image_model)
    events = []

    async def event(_client, _task, _lease, value):
        events.append(value)

    worker = RemoteWorkflowExecutor()
    worker.runtime = SimpleNamespace(task_event=event)
    params = {
        'workflow_id': 'ppt-workflow', 'revision_id': 'ppt-revision', 'tree_hash': 'ppt-hash',
        'step_id': 'analyze_requirements', 'workspace_context': {'permission_mode': 'always_ask'},
        'workflow_runtime': {'post_step_checks': [{
            'step_id': 'analyze_requirements', 'tool': 'check_ppt_workflow_capabilities',
            'arguments': {'capability_requirements': 'ppt_capability_requirements'},
        }]},
    }
    artifacts = [{'slot': 'ppt_capability_requirements', 'content_type': 'text',
                  'value': {'text': f'AI_BACKGROUND_IMAGES: {backgrounds}'}}]
    with workflow_execution_scope():
        if backgrounds == 'enabled':
            with pytest.raises(RuntimeError, match='MEDIA_CAPABILITY_DEPENDENCY_MISSING'):
                await worker._run_post_step_checks(None, 'task', 'lease', params, artifacts)
        else:
            await worker._run_post_step_checks(None, 'task', 'lease', params, artifacts)
    assert events[-1]['tool_results'][0]['result']['ok'] is (backgrounds == 'disabled')
