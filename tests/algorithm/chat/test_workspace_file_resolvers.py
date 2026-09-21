"""Host file declarations enumerate final inputs before any side effect."""
import json
from types import SimpleNamespace

import pytest

from lazymind.chat.engine.tools import host_file_resolution as paths
from lazymind.chat.engine.tools.multimodal import resolve_media_files, resolve_video_file
from lazymind.document_tools import resolve_writer_files
from lazymind.chat.engine.subagent import tools as artifacts


@pytest.fixture
def host_root(tmp_path, monkeypatch):
    monkeypatch.setattr(paths, 'canonical_host_path',
                        lambda value, default_root=None: str((__import__('pathlib').Path(default_root or tmp_path) / value).resolve()))
    monkeypatch.setattr(paths, 'managed_path', lambda path: False)
    monkeypatch.setattr(paths, 'get_workspace_permission_context', lambda: None)
    return tmp_path


def test_media_batch_enumerates_every_input_and_preserves_remote(host_root):
    result = resolve_media_files({'urls': '["first.png", "https://example.org/image.png"]',
                                  'first_frame_url': 'frame.png', 'reference_urls': ['other.png']})
    assert result.arguments['urls'] == [str(host_root / 'first.png'), 'https://example.org/image.png']
    assert {item.path for item in result.files} == {
        str(host_root / name) for name in ('first.png', 'frame.png', 'other.png')}
    assert all(item.operation == 'read' for item in result.files)


@pytest.mark.parametrize('value', ['s3://bucket/image.png', '/static-files/%2e%2e/private.png',
                                  'file://foreign-host/private.png'])
def test_media_rejects_unknown_or_escaping_locators(host_root, value):
    with pytest.raises(Exception):
        resolve_media_files({'url': value})


def test_video_requires_local_input(host_root):
    with pytest.raises(Exception):
        resolve_video_file({'url': 'https://example.org/video.mp4'})
    assert resolve_video_file({'url': 'video.mp4'}).files[0].path == str(host_root / 'video.mp4')


def test_writer_nested_json_resources_and_assets(host_root):
    arguments = {'writing_task_json': json.dumps({'task_id': 'safe', 'inputs': [
        {'resource_type': 'image', 'uri': 'input.png'}]}),
        'input_resources_json': json.dumps([{'resource_type': 'table', 'uri': 'table.csv'}]),
        'media_assets_json': json.dumps({'assets': {'asset': {
            'media_asset_id': 'asset', 'local_path': 'asset.png', 'uri': 'feishu://image-token'}}}),
        'acquired_resources_json': json.dumps({'need': {'resource_type': 'image', 'uri': 'new.png'}}),
        'media_store': 'media'}
    result = resolve_writer_files(arguments)
    assert {(item.path, item.operation) for item in result.files} == {
        (str(host_root / name), 'read') for name in ('input.png', 'table.csv', 'asset.png', 'new.png')
    } | {(str(host_root / 'media'), 'write')}
    decoded = json.loads(result.arguments['writing_task_json'])
    assert decoded['inputs'][0]['uri'] == str(host_root / 'input.png')
    assert arguments['media_store'] == 'media'


def test_writer_internal_ids_and_indirect_resource_files_fail_closed(host_root):
    for arguments in ({'writing_task_json': '{"task_id":"../escape"}'},
                      {'resources_json': '["hidden-resources.json"]'},
                      {'acquired_resources_json': '{"need":"hidden-resource.json"}'},
                      {'resources_json': '[{"resource_type":"document","uri":"unknown://host/path"}]'}):
        with pytest.raises(Exception):
            resolve_writer_files(arguments)


def test_artifact_batch_normalizes_all_file_shapes(host_root, monkeypatch):
    context = SimpleNamespace(workspace_path=str(host_root / 'task'))
    monkeypatch.setattr(artifacts, 'require_context', lambda: context)
    arguments = {'artifacts': [
        {'key': 'file', 'content_type': 'file', 'value': {'path': '../one.txt'}},
        {'key': 'list', 'content_type': 'file_list', 'value': ['../two.txt', {'path': '../three.txt'}]},
        {'key': 'image', 'content_type': 'image', 'value': {'url': '../four.png', 'caption': 'image'}},
        {'key': 'text', 'value': '/this/is/content'},
    ]}
    result = artifacts.resolve_artifact_files(arguments)
    assert {item.path for item in result.files if item.operation == 'read'} == {
        str(host_root / name) for name in ('one.txt', 'two.txt', 'three.txt', 'four.png')}
    assert result.arguments['artifacts'][1]['value'] == [str(host_root / 'two.txt'), str(host_root / 'three.txt')]
    assert result.arguments['artifacts'][2]['value']['caption'] == 'image'
    assert arguments['artifacts'][0]['value']['path'] == '../one.txt'


def test_managed_media_produces_no_host_intent(host_root, monkeypatch):
    monkeypatch.setattr(paths, 'managed_path', lambda path: True)
    result = resolve_media_files({'urls': ['upload.png']})
    assert result.files == ()
    assert result.arguments['urls'] == [str(host_root / 'upload.png')]


def test_writer_profiles_artifact_inputs_are_explicit_reads(host_root):
    result = resolve_writer_files({'resource_profiles_json': '["profile.json"]'})
    assert result.files[0].path == str(host_root / 'profile.json')
    assert json.loads(result.arguments['resource_profiles_json']) == [str(host_root / 'profile.json')]


def test_canonical_image_path_is_not_reinterpreted_as_upload(host_root, monkeypatch):
    from lazymind.chat.engine.tools.infra import image_generation_support as images
    monkeypatch.setattr(images, 'resolve_local_image_path', lambda value: '/wrong/upload/path')
    target = str(host_root / 'image.png')
    assert images.resolve_tool_image_path(target) == target


def test_tool_resolution_snapshot_does_not_use_mutable_subagent_workspace(tmp_path, monkeypatch):
    from lazymind.chat.engine.subagent import context as contexts
    from lazymind.chat.engine.tools.workspace_context import (
        ToolResolutionContext, tool_resolution_scope,
    )

    original, changed = tmp_path / 'captured', tmp_path / 'changed'
    original.mkdir()
    changed.mkdir()
    monkeypatch.setattr(contexts, 'get_context', lambda: SimpleNamespace(workspace_path=str(changed)))
    request = ToolResolutionContext(managed_roots=(str(original.resolve()),))
    with tool_resolution_scope(request):
        assert paths.managed_path(str(original / 'asset.png'))
        assert not paths.managed_path(str(changed / 'asset.png'))
        result = artifacts.resolve_artifact_files({'artifacts': [
            {'key': 'image', 'content_type': 'image', 'value': 'asset.png'}]})
    assert result.arguments['artifacts'][0]['value'] == str(original / 'asset.png')
    assert result.files == ()


def test_image_editor_executes_canonical_arguments(host_root, monkeypatch):
    from pathlib import Path
    from lazymind.chat.engine.tools import multimodal

    source = host_root / 'source.png'
    source.write_bytes(b'authorized bytes')
    monkeypatch.setattr(multimodal, '_missing_media_model_result', lambda _: None)
    monkeypatch.setattr(multimodal, 'run_image_model',
                        lambda role, prompt, **kwargs: [Path(item).read_bytes() for item in kwargs['files']])
    resolved = multimodal.resolve_media_files({'prompt': 'edit', 'urls': ['source.png']})
    assert multimodal.image_editor(**resolved.arguments) == [b'authorized bytes']


def test_writer_custom_media_store_writes_inside_declared_directory(host_root, monkeypatch):
    from PIL import Image
    from lazymind.document_tools import WriterCreateToolkit
    from lazymind.document_tools import writing

    source = host_root / 'source.png'
    Image.new('RGB', (2, 2), 'red').save(source)
    internal = host_root / 'internal'
    internal.mkdir()
    monkeypatch.setattr(writing, '_temp_root', lambda: internal)
    resolved = resolve_writer_files({
        'writing_task_json': '{"task_id":"task","query":"image","task_type":"write"}',
        'input_resources_json': json.dumps([{'resource_type': 'image', 'uri': 'source.png'}]),
        'media_store': 'output',
    })
    result = json.loads(WriterCreateToolkit().collect_available_media(**resolved.arguments))
    directory = host_root / 'output'
    writes = [intent.path for intent in resolved.files if intent.operation == 'write']
    assert writes == [str(directory)]
    assert result['media_assets']['assets']
    for asset in result['media_assets']['assets'].values():
        assert __import__('pathlib').Path(asset['local_path']).is_relative_to(directory)
        assert __import__('pathlib').Path(asset['local_path']).is_file()
    assert (directory / 'media_assets.json').is_file()
    assert (directory / 'profile_input_resources.json').is_file()


def test_writer_output_store_rejects_existing_descendant_escape(host_root):
    output = host_root / 'output'
    output.mkdir()
    outside = host_root / 'outside'
    outside.mkdir()
    (output / 'assets').symlink_to(outside, target_is_directory=True)
    with pytest.raises(Exception, match='links outside'):
        resolve_writer_files({'media_store': 'output'})
    assert list(outside.iterdir()) == []


def test_global_upload_and_writer_parents_do_not_exempt_other_users(tmp_path, monkeypatch):
    import tempfile
    from pathlib import Path
    from lazymind.chat.engine.tools.workspace_context import ToolResolutionContext, tool_resolution_scope
    from lazymind.chat.service.utils import static_file_url

    uploads = tmp_path / 'uploads'
    uploads.mkdir()
    own = uploads / 'own.txt'
    foreign = uploads / 'foreign.txt'
    own.write_text('attached')
    foreign.write_text('other user secret')
    monkeypatch.setattr(static_file_url, '_upload_root', lambda: str(uploads))
    request = ToolResolutionContext(managed_files=frozenset({str(own.resolve())}))
    with tool_resolution_scope(request):
        assert paths.managed_path(str(own))
        assert not paths.managed_path(str(foreign))
        assert not paths.managed_path(str(Path(tempfile.gettempdir()) / 'lazymind-writer-tools' / 'foreign' / 'secret.txt'))
        assert paths.FileResolution().local(str(foreign)) == str(foreign)
        resolution = resolve_media_files({'url': str(foreign)})
        assert [intent.path for intent in resolution.files] == [str(foreign)]


def test_artifact_open_rejects_source_replaced_after_guard_precheck(tmp_path, monkeypatch):
    from lazyllm.tools.agent import HostFileIntent
    from lazymind.chat.engine.tools.host_access_guard import HostAccessGuard, host_access_scope
    from lazymind.chat.engine.tools.workspace_context import WorkspaceContext, workspace_permission_scope

    source, secret = tmp_path / 'source.txt', tmp_path / 'secret.txt'
    source.write_text('approved')
    secret.write_text('secret')
    destination = tmp_path / 'task'
    destination.mkdir()
    guard = HostAccessGuard((HostFileIntent(str(source), 'read'),))
    request = WorkspaceContext.from_config({'_subagent_workspace': str(destination)})
    source.unlink()
    source.symlink_to(secret)
    with workspace_permission_scope(request), host_access_scope(guard):
        with pytest.raises(Exception):
            paths.copy_artifact_input(str(source), str(destination))
    assert not (destination / 'source.txt').exists()
    assert secret.read_text() == 'secret'


def test_staged_media_cannot_reread_a_replaced_original(tmp_path):
    from lazyllm.tools.agent import HostFileIntent
    from pathlib import Path
    from lazymind.chat.engine.tools.host_access_guard import HostAccessGuard, host_access_scope
    from lazymind.chat.engine.tools.workspace_context import WorkspaceContext, workspace_permission_scope

    source = tmp_path / 'source.png'
    source.write_bytes(b'approved')
    guard = HostAccessGuard((HostFileIntent(str(source), 'read'),))
    with workspace_permission_scope(WorkspaceContext.from_config({})), host_access_scope(guard):
        staged = paths.stage_input_file(str(source))
        source.write_bytes(b'changed')
        assert Path(staged).read_bytes() == b'approved'


def test_writer_inputs_are_private_copies_of_guarded_reads(tmp_path):
    from lazyllm.tools.agent import HostFileIntent
    from pathlib import Path
    from lazymind.document_tools import WriterCreateToolkit
    from lazymind.chat.engine.tools.host_access_guard import HostAccessGuard, host_access_scope
    from lazymind.chat.engine.tools.workspace_context import WorkspaceContext, workspace_permission_scope

    source = tmp_path / 'input.txt'
    source.write_text('approved input')
    guard = HostAccessGuard((HostFileIntent(str(source), 'read'),))
    with workspace_permission_scope(WorkspaceContext.from_config({})), host_access_scope(guard):
        result = json.loads(WriterCreateToolkit().build_resources(file_paths_json=json.dumps([str(source)])))
    staged = Path(result[0]['uri'])
    assert staged != source
    source.write_text('changed input')
    assert staged.read_text() == 'approved input'


def test_writer_generated_temp_root_belongs_to_captured_task(tmp_path):
    from lazymind.document_tools.artifacts import _temp_root
    from lazymind.chat.engine.tools.workspace_context import ToolResolutionContext, tool_resolution_scope

    request = ToolResolutionContext(managed_roots=(str(tmp_path.resolve()),))
    with tool_resolution_scope(request):
        directory = _temp_root()
        assert directory.is_relative_to(tmp_path)
        assert paths.managed_path(str(directory / 'draft.json'))
