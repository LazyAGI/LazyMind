"""Writer outputs use pinned file opens and directory-scoped mutation guards."""
import hashlib
from io import BytesIO

import pytest
from PIL import Image
from lazyllm.tools.agent import HostFileIntent
from lazyllm.tools.agent import host_file_io
from lazyllm.tools.writer.data_models.task import InputResource
from lazyllm.tools.writer.tools.base import WriterToolBase
from lazyllm.tools.writer.tools.multimodal_tools import WriterMultimodalTools
from lazyllm.tools.writer.utils.artifact import save_artifact_json
from lazymind.chat.engine.tools.host_access_guard import HostAccessGuard


@pytest.mark.parametrize('kind', ['json', 'markdown', 'asset'])
def test_writer_leaf_replaced_after_validation_does_not_write_secret(tmp_path, monkeypatch, kind):
    directory = tmp_path / 'output'
    directory.mkdir()
    secret = tmp_path / 'secret.txt'
    secret.write_bytes(b'keep secret')
    image = BytesIO()
    Image.new('RGB', (2, 2), 'red').save(image, format='PNG')
    data = image.getvalue()
    if kind == 'asset':
        (directory / 'assets').mkdir()
        destination = directory / 'assets' / (hashlib.sha256(data).hexdigest() + '.png')
    else:
        destination = directory / ('draft.json' if kind == 'json' else 'draft.md')
    guard = HostAccessGuard((HostFileIntent(str(directory), 'write'),))
    original = guard.check_path
    injected = False

    def replace_after_validation(path, operation='read'):
        nonlocal injected
        result = original(path, operation)
        if str(path) == str(destination) and not injected:
            destination.symlink_to(secret)
            injected = True
        return result

    monkeypatch.setattr(guard, 'check_path', replace_after_validation)
    with host_file_io.host_file_execution_scope(guard):
        with pytest.raises(Exception):
            if kind == 'json':
                save_artifact_json({'data': 'new'}, str(destination))
            elif kind == 'markdown':
                WriterToolBase(artifact_store=str(directory))._write_markdown_artifact('draft.md', 'new')
            else:
                WriterMultimodalTools(artifact_store=str(directory))._materialize_image_bytes(
                    data, InputResource(resource_type='image', uri='https://example.org/image.png'), suffix_hint='.png')
    assert secret.read_bytes() == b'keep secret'


def test_writer_directory_replaced_before_mkdir_cannot_escape(tmp_path, monkeypatch):
    directory = tmp_path / 'output'
    directory.mkdir()
    outside = tmp_path / 'outside'
    outside.mkdir()
    guard = HostAccessGuard((HostFileIntent(str(directory), 'write'),))
    original = guard.check_path

    def replace_after_check(path, operation='read'):
        result = original(path, operation)
        if str(path) == str(directory):
            directory.rmdir()
            directory.symlink_to(outside, target_is_directory=True)
        return result

    monkeypatch.setattr(guard, 'check_path', replace_after_check)
    with host_file_io.host_file_execution_scope(guard):
        with pytest.raises(Exception):
            save_artifact_json({'new': 'content'}, str(directory / 'draft.json'))
    assert list(outside.iterdir()) == []


def test_checkpoint_leaf_swapped_before_actual_open_cannot_write_secret(tmp_path, monkeypatch):
    import json
    from types import SimpleNamespace
    from lazymind.chat.engine.tools import writer

    checkpoint = tmp_path / 'checkpoints'
    checkpoint.mkdir()
    temporary = tmp_path / 'temporary'
    temporary.mkdir()
    secret = tmp_path / 'secret.txt'
    secret.write_text('keep secret')

    class Instruction:
        @classmethod
        def model_validate(cls, value):
            return SimpleNamespace(section_title=value['section_title'])

    class Stream:
        def __enter__(self):
            return self

        def __exit__(self, *args):
            pass

        def __iter__(self):
            yield '## Title\n\nBody content'

        def result(self):
            return {}

    class Drafting:
        def __init__(self, **kwargs):
            pass

        def stream_draft_section(self, **kwargs):
            return Stream()

    monkeypatch.setattr(writer, 'SectionInstruction', Instruction)
    monkeypatch.setattr(writer, 'WriterDraftingTools', Drafting)
    monkeypatch.setattr(writer, 'AutoModel', lambda **kwargs: None)
    monkeypatch.setattr(writer, '_temp_root', lambda: temporary)
    monkeypatch.setattr(writer, '_write_input_artifact', lambda *args, **kwargs: '/input.json')
    monkeypatch.setattr(writer, '_primary_data', lambda _: '## Title\n\nBody content')
    guard = HostAccessGuard((HostFileIntent(str(checkpoint), 'write'), HostFileIntent(str(temporary), 'write')))
    original = guard.check_path
    attacked = []

    def attack(path, operation='read'):
        result = original(path, operation)
        if str(path).endswith('.tmp') and not attacked:
            from pathlib import Path
            Path(path).symlink_to(secret)
            attacked.append(path)
        return result

    monkeypatch.setattr(guard, 'check_path', attack)
    with host_file_io.host_file_execution_scope(guard):
        with pytest.raises(Exception):
            writer.WriterToolkitBase().stream_draft_blocks_markdown(
                writing_task_json='{}', writing_context_json='{}',
                section_instructions_json=json.dumps({'instructions': [{'section_title': 'Title'}]}),
                on_delta=lambda _: None, checkpoint_dir=str(checkpoint))
    assert attacked
    assert secret.read_text() == 'keep secret'
    assert not list(checkpoint.glob('*.md'))


def test_guarded_writer_collects_from_staged_input_into_external_store(tmp_path):
    import json
    from pathlib import Path
    from lazymind.chat.engine.tools import writer
    from lazymind.chat.engine.tools.host_access_guard import host_access_scope
    from lazymind.chat.engine.tools.workspace_context import (
        ToolResolutionContext, WorkspacePermissionContext,
        tool_resolution_scope, workspace_permission_scope,
    )

    task = tmp_path / 'task'
    task.mkdir()
    source = tmp_path / 'input.png'
    Image.new('RGB', (2, 2), 'blue').save(source)
    output = tmp_path / 'output'
    config = {'_subagent_workspace': str(task)}
    permission = WorkspacePermissionContext.from_snapshot({
        'workspace_id': 'workspace', 'root': str(task), 'workspace_version': 1,
        'permission_mode': 'always_ask', 'permission_version': 1,
    })
    with tool_resolution_scope(ToolResolutionContext.from_config(config)), workspace_permission_scope(permission):
        resolved = writer.resolve_writer_files({
            'writing_task_json': '{"task_id":"task","query":"image","task_type":"write"}',
            'input_resources_json': json.dumps([{'resource_type': 'image', 'uri': str(source)}]),
            'media_store': str(output),
        })
        guard = HostAccessGuard(resolved.files)
        with host_access_scope(guard), host_file_io.host_file_execution_scope(guard):
            result = json.loads(writer.WriterToolkitBase().collect_available_media(**resolved.arguments))
        assert result['media_assets']['assets']
        staged_inputs = list((task / '.approved-inputs').rglob('input.png'))
        assert len(staged_inputs) == 1
        assert staged_inputs[0].read_bytes() == source.read_bytes()
        assert __import__('lazymind.chat.engine.tools.host_file_resolution', fromlist=['managed_path']).managed_path(str(staged_inputs[0]))
    for asset in result['media_assets']['assets'].values():
        assert Path(asset['local_path']).is_relative_to(output)
        assert Path(asset['local_path']).read_bytes() == source.read_bytes()
