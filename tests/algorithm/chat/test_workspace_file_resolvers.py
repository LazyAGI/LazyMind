"""Host file declarations enumerate final inputs before any side effect."""
import json
import time
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
    monkeypatch.setattr(paths, 'managed_path', lambda path, operation='read': False)
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
    assert {item.path for item in result.files if item.operation == 'write'} == {str(host_root / 'task')}
    assert result.arguments['artifacts'][1]['value'] == [str(host_root / 'two.txt'), str(host_root / 'three.txt')]
    assert result.arguments['artifacts'][2]['value']['caption'] == 'image'
    assert arguments['artifacts'][0]['value']['path'] == '../one.txt'


def test_managed_media_produces_no_host_intent(host_root, monkeypatch):
    monkeypatch.setattr(paths, 'managed_path', lambda path, operation='read': True)
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


def test_acl_read_roots_exempt_reads_but_not_mutations(tmp_path):
    from lazymind.chat.engine.tools.workspace_context import ToolResolutionContext, tool_resolution_scope

    owned = tmp_path / 'owned'
    foreign = tmp_path / 'foreign'
    owned.mkdir()
    foreign.mkdir()
    request = ToolResolutionContext(acl_read_roots=(str(owned.resolve()),))
    with tool_resolution_scope(request):
        read = paths.FileResolution().local(str(owned / 'from-other-conversation.png'))
        write = paths.FileResolution()
        write.local(str(owned / 'replace.png'), operation='write')
        outside = paths.FileResolution()
        outside.local(str(foreign / 'secret.png'))
    assert read == str(owned / 'from-other-conversation.png')
    assert write.files[0].operation == 'write'
    assert outside.files[0].path == str(foreign / 'secret.png')


def test_media_short_name_resolves_historical_attachment_before_authorization(
    host_root, monkeypatch,
):
    from lazymind.chat.engine.tools.file_resources import resolver

    attachment = str(host_root / 'uploads' / 'old.png')
    monkeypatch.setattr(
        resolver, 'resolve_attachment_path',
        lambda name, prefer_newest=False: (attachment, None),
    )
    result = resolve_media_files({'url': 'old.png'})
    assert result.arguments['url'] == attachment
    assert result.files[0].path == attachment


@pytest.mark.parametrize('source', ['current', 'history'])
def test_real_attachment_context_resolves_current_and_historical_short_names(
    tmp_path, monkeypatch, source,
):
    import lazyllm
    from lazymind.chat.engine.tools.workspace_context import (
        ToolResolutionContext, normalize_managed_files, tool_resolution_scope,
    )

    attachment = tmp_path / f'{source}.png'
    attachment.write_bytes(b'image')
    config = {
        'files': [str(attachment)] if source == 'current' else [],
        'history_files_per_turn': {'2': [str(attachment)]} if source == 'history' else {},
        'citation_state': {},
    }
    previous = lazyllm.globals.get('agentic_config')
    lazyllm.globals['agentic_config'] = config
    try:
        context = ToolResolutionContext(managed_files=normalize_managed_files([str(attachment)]))
        with tool_resolution_scope(context):
            result = resolve_media_files({'url': attachment.name})
    finally:
        lazyllm.globals['agentic_config'] = previous or {}
    assert result.arguments['url'] == str(attachment.resolve())
    assert result.files == ()


def test_user_acl_reads_other_conversation_but_not_other_user(tmp_path, monkeypatch):
    from lazymind.chat.engine.tools import system_resources
    from lazymind.chat.engine.tools.workspace_context import ToolResolutionContext, tool_resolution_scope

    runtime_config = __import__('lazymind.config', fromlist=['config']).config
    monkeypatch.setitem(runtime_config._impl, 'agentic_workspace', str(tmp_path / 'agent-workspaces'))
    own_roots = system_resources.user_managed_read_roots('owner')
    own = __import__('pathlib').Path(own_roots[1]) / ('f' * 32) / 'result.png'
    foreign_root = system_resources.user_chat_workspace_roots('other')[0]
    foreign = __import__('pathlib').Path(foreign_root) / ('e' * 32) / 'secret.png'
    own.parent.mkdir(parents=True)
    foreign.parent.mkdir(parents=True)
    own.write_bytes(b'own')
    foreign.write_bytes(b'foreign')

    with tool_resolution_scope(ToolResolutionContext(acl_read_roots=own_roots)):
        own_resolution = paths.FileResolution()
        own_resolution.local(str(own))
        foreign_resolution = paths.FileResolution()
        foreign_resolution.local(str(foreign))
    assert not own_resolution.files
    assert foreign_resolution.files[0].path == str(foreign.resolve())


def test_generated_files_use_owner_scoped_storage(tmp_path, monkeypatch):
    from lazymind.chat.engine.tools import system_resources

    monkeypatch.setattr(system_resources, 'current_user_id', lambda: 'user/one')
    monkeypatch.setattr(
        system_resources, 'user_upload_root',
        lambda user_id: str(tmp_path / system_resources.safe_storage_part(user_id)),
    )
    directory = system_resources.generated_upload_directory('generated-images')
    assert directory == tmp_path / 'user_one' / 'generated-images'


def test_generated_image_is_relocated_below_authenticated_owner_root(tmp_path, monkeypatch):
    import lazyllm
    from lazymind.chat.engine.tools import system_resources
    from lazymind.chat.engine.tools.infra import image_generation_support

    uploads = tmp_path / 'uploads'
    source = tmp_path / 'model-output.png'
    source.write_bytes(b'generated')
    monkeypatch.setenv('LAZYMIND_UPLOAD_ROOT', str(uploads))
    previous = lazyllm.globals.get('agentic_config')
    lazyllm.globals['agentic_config'] = {'user_id': 'owner'}
    try:
        relocated = image_generation_support._relocate_generated_image_to_upload(str(source))
    finally:
        lazyllm.globals['agentic_config'] = previous or {}
    expected_root = __import__('pathlib').Path(system_resources.user_upload_root('owner'))
    assert __import__('pathlib').Path(relocated).is_relative_to(expected_root)
    assert __import__('pathlib').Path(relocated).read_bytes() == b'generated'
    assert not source.exists()


def test_legacy_generated_file_uses_exact_random_id_as_read_capability(tmp_path, monkeypatch):
    from lazymind.chat.service.utils import static_file_url

    uploads = tmp_path / 'uploads'
    name = '0123456789abcdef0123456789abcdef.png'
    legacy = uploads / 'ai_generated' / name
    legacy.parent.mkdir(parents=True)
    legacy.write_bytes(b'image')
    monkeypatch.setenv('LAZYMIND_UPLOAD_ROOT', str(uploads))
    expires = int(time.time()) + 60
    rel = f'ai_generated/{name}'
    signed = f'/static-files/{rel}?expires={expires}&sig={static_file_url.sign_static_file(rel, expires)}'

    allowed = paths.FileResolution().local(signed)
    unsigned = paths.FileResolution().local(f'/static-files/{rel}')
    expired = paths.FileResolution().local(
        f'/static-files/{rel}?expires=1&sig=obsolete',
    )
    guessed = paths.FileResolution()
    guessed.local('/static-files/ai_generated/readable-name.png')
    directory = paths.FileResolution()
    directory.local(f'/static-files/ai_generated?expires={expires}&sig=' +
                    static_file_url.sign_static_file('ai_generated', expires))
    mutation = paths.FileResolution()
    mutation.local(signed, operation='delete')

    assert allowed == str(legacy.resolve())
    assert unsigned == str(legacy.resolve())
    assert expired == str(legacy.resolve())
    assert guessed.files
    assert directory.files
    assert mutation.files[0].operation == 'delete'


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
