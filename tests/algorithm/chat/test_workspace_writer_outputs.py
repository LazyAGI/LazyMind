"""Writer IO stays native; execution guards validate paths before invocation."""
import pytest
from PIL import Image
from lazyllm.tools.agent import HostFileIntent
from lazymind.chat.engine.tools.host_access_guard import HostAccessGuard


def test_writer_directory_drift_is_rejected_before_execution(tmp_path):
    directory, outside = tmp_path / 'output', tmp_path / 'outside'
    directory.mkdir()
    outside.mkdir()
    guard = HostAccessGuard((HostFileIntent(str(directory), 'write'),))
    directory.rmdir()
    directory.symlink_to(outside, target_is_directory=True)
    with pytest.raises(Exception):
        guard.validate()
    assert list(outside.iterdir()) == []


def test_guarded_writer_collects_from_staged_input_into_external_store(tmp_path):
    import json
    from pathlib import Path
    from lazymind.document_tools import WriterCreateToolkit, resolve_writer_files
    from lazymind.chat.engine.tools.host_access_guard import host_access_scope
    from lazymind.chat.engine.tools.workspace_context import (
        ToolResolutionContext, WorkspaceContext,
        tool_resolution_scope, workspace_permission_scope,
    )

    task = tmp_path / 'task'
    task.mkdir()
    source = tmp_path / 'input.png'
    Image.new('RGB', (2, 2), 'blue').save(source)
    output = tmp_path / 'output'
    permission = WorkspaceContext.from_snapshot({
        'workspace_id': 'workspace', 'root': str(task), 'workspace_version': 1,
        'permission_mode': 'always_ask', 'permission_version': 1,
    })
    with tool_resolution_scope(ToolResolutionContext(managed_roots=(str(task.resolve()),))), workspace_permission_scope(permission):
        resolved = resolve_writer_files({
            'writing_task_json': '{"task_id":"task","query":"image","task_type":"write"}',
            'input_resources_json': json.dumps([{'resource_type': 'image', 'uri': str(source)}]),
            'media_store': str(output),
        })
        guard = HostAccessGuard(resolved.files)
        with host_access_scope(guard):
            guard.validate()
            result = json.loads(WriterCreateToolkit().collect_available_media(**resolved.arguments))
        assert result['media_assets']['assets']
        staged_inputs = list((task / '.approved-inputs').rglob('input.png'))
        assert len(staged_inputs) == 1
        assert staged_inputs[0].read_bytes() == source.read_bytes()
        assert __import__('lazymind.chat.engine.tools.host_file_resolution', fromlist=['managed_path']).managed_path(str(staged_inputs[0]))
    for asset in result['media_assets']['assets'].values():
        assert Path(asset['local_path']).is_relative_to(output)
        assert Path(asset['local_path']).read_bytes() == source.read_bytes()
