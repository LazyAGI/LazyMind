"""Owned Markdown dependencies become reusable media without gating the writing step."""
import base64
import hashlib
import io
import json
from pathlib import Path

import pytest
from PIL import Image

from lazymind.document_tools import markdown_inputs, writing


def image_resource(color):
    stream = io.BytesIO()
    Image.new('RGB', (2, 2), color).save(stream, format='PNG')
    data = stream.getvalue()
    return {'name': 'figure.png', 'mime_type': 'image/png', 'content_base64': base64.b64encode(data).decode(),
            'content_hash': 'sha256:' + hashlib.sha256(data).hexdigest()}


def markdown_file(directory, text, dependencies, warnings=()):
    directory.mkdir(parents=True, exist_ok=True)
    path = directory / 'material.md'
    manifest = {'images': [{'source': source, 'resource_id': identifier} for source, identifier in dependencies.items()],
                'warnings': list(warnings)}
    encoded = base64.b64encode(json.dumps(manifest, ensure_ascii=False).encode()).decode()
    path.write_text(text + '\n\n<!-- lazymind-markdown-images:v1:' + encoded + ' -->\n', encoding='utf-8')
    return path


def collect(monkeypatch, tmp_path, paths, resources):
    from lazymind import model_config

    monkeypatch.setattr(model_config, 'is_model_role_available', lambda _: False)

    def read(identifier, timeout):
        value = resources[identifier]
        if isinstance(value, Exception):
            raise value
        return value

    monkeypatch.setattr(markdown_inputs, 'read_image_resource', read)
    return writing.collect_document_media({'task_id': 'test', 'task_type': 'write', 'query': 'Write using the material'},
                                          file_paths=list(map(str, paths)), media_store=str(tmp_path / 'media'),
                                          analyze_source_images=False)


def test_markdown_images_are_copied_and_scoped_without_changing_source(monkeypatch, tmp_path):
    text = '# Original\n\n![结果](<images/图 1.png>)\n'
    paths = [markdown_file(tmp_path / str(index), text, {'images/图 1.png': identifier})
             for index, identifier in enumerate(['red', 'blue'])]
    originals = [path.read_bytes() for path in paths]
    result = collect(monkeypatch, tmp_path, paths, {'red': image_resource('red'), 'blue': image_resource('blue')})
    assets = result['media_assets']['assets']
    assert len(assets) == 2
    assert [path.read_bytes() for path in paths] == originals
    assert [Path(path).read_text() for path in result['source_materials']] == [text, text]
    assert {key for asset in assets.values() for key in asset['meta']['markdown_source_references']} == {'0', '1'}
    for asset in assets.values():
        copy = Path(asset['local_path'])
        assert copy.is_file() and copy.parent.name == 'assets'
        assert hashlib.sha256(copy.read_bytes()).hexdigest() == asset['meta']['sha256']
    for path in paths:
        path.unlink()
    for path in (tmp_path / 'media' / 'inputs').iterdir():
        path.unlink()
    assert all(Path(asset['local_path']).is_file() for asset in assets.values())


@pytest.mark.parametrize('failure', [
    TimeoutError('timeout'), PermissionError('not authorized'),
    {'content_base64': 'invalid', 'mime_type': 'image/png'},
])
def test_bad_dependency_does_not_discard_good_images_or_source(monkeypatch, tmp_path, failure):
    text = '![good](good.png)\n\n![missing](missing.png)\n'
    path = markdown_file(tmp_path, text, {'good.png': 'good', 'missing.png': 'bad'})
    result = collect(monkeypatch, tmp_path, [path], {'good': image_resource('red'), 'bad': failure})
    assert len(result['media_assets']['assets']) == 1
    assert Path(result['source_materials'][0]).read_text() == text
    assert any('missing.png' in warning for warning in result['warnings'])


def test_image_budget_exhaustion_returns_the_original_document(tmp_path):
    path = markdown_file(tmp_path, '# Original\n\n![x](x.png)', {'x.png': 'image'})
    documents, resources, warnings = markdown_inputs.prepare_markdown_inputs(
        [path], tmp_path / 'media', read_resource=lambda *_: pytest.fail('must not start late IO'), budget=0)
    assert documents[0]['text'].startswith('# Original')
    assert resources == [] and warnings


def test_image_copy_failure_and_corrupt_images_do_not_fail_collection(monkeypatch, tmp_path):
    text = '# Keep this body\n\n![x](x.png)'
    path = markdown_file(tmp_path, text, {'x.png': 'image'})
    broken = image_resource('red')
    data = b'not really an image'
    broken.update(content_base64=base64.b64encode(data).decode(),
                  content_hash='sha256:' + hashlib.sha256(data).hexdigest())
    result = collect(monkeypatch, tmp_path, [path], {'image': broken})
    assert result['media_assets']['assets'] == {}
    assert Path(result['source_materials'][0]).read_text() == text
    assert result['warnings']

    original = Path.write_bytes

    def fail_image_copy(target, data):
        if target.parent.name == 'inputs':
            raise OSError('disk write failed')
        return original(target, data)

    monkeypatch.setattr(Path, 'write_bytes', fail_image_copy)
    result = collect(monkeypatch, tmp_path, [path], {'image': image_resource('blue')})
    assert result['media_assets']['assets'] == {}
    assert Path(result['source_materials'][0]).read_text() == text
    assert result['warnings']


def test_only_rendered_markdown_images_are_collected_and_manifest_is_not_profiled(monkeypatch, tmp_path):
    text = '# Material\n\n![real][ref]\n\n[ref]: images/a.png\n\n```md\n![fake](secret.png)\n```\n'
    path = markdown_file(tmp_path, text, {'images/a.png': 'image', 'secret.png': 'private'})
    result = collect(monkeypatch, tmp_path, [path], {'image': image_resource('green')})
    assert len(result['media_assets']['assets']) == 1
    resources = json.loads(writing.WriterWritingCapabilities().build_resources(file_paths_json=json.dumps([str(path)])))
    assert resources[0]['inline_text'] == text
    assert resources[0]['uri'] == str(path)
    assert 'content_base64' not in resources[0]['inline_text']


def test_optional_source_preview_failure_preserves_media(tmp_path):
    root = tmp_path / 'not-a-directory'
    root.write_text('occupied')
    library = {'assets': {'asset-x': {'local_path': 'keep.png'}}}
    paths, warnings = markdown_inputs.save_source_materials(
        [{'path': 'input.md', 'text': '# body', 'images': {}}], library, root)
    assert not paths and warnings
    assert library['assets']['asset-x']['local_path'] == 'keep.png'


def test_hard_deadline_does_not_wait_for_a_stalled_reader_or_accept_a_late_result(tmp_path):
    from contextvars import ContextVar
    from threading import Event

    owner = ContextVar('image-test-owner', default='wrong-owner')
    owner.set('material-owner')
    release, finished = Event(), Event()

    def stalled(identifier, timeout):
        assert owner.get() == 'material-owner'
        release.wait(2)
        finished.set()
        return image_resource('red')

    path = markdown_file(tmp_path, '# Body\n![x](x.png)', {'x.png': 'image'})
    try:
        documents, resources, warnings = markdown_inputs.prepare_markdown_inputs(
            [path], tmp_path / 'media', read_resource=stalled, budget=0.02)
        assert resources == [] and warnings and documents[0]['images'] == {}
        assert not finished.is_set()
    finally:
        release.set()
        finished.wait(2)
    assert not (tmp_path / 'media').exists()


def test_real_core_result_envelope_is_copied_into_the_media_library(monkeypatch, tmp_path):
    import requests
    from lazymind import model_config
    from lazymind.chat.engine.tools.infra import core_api_client

    monkeypatch.setattr(model_config, 'is_model_role_available', lambda _: False)
    monkeypatch.setattr(core_api_client, '_current_user_headers', lambda: {'X-User-Id': 'material-owner'})
    resource = image_resource('red')
    calls = []

    def get_owned_resource(session, url, *, headers, timeout):
        assert session.trust_env is False
        assert url.endswith('/workflow-input-resources/owned-image')
        assert headers == {'X-User-Id': 'material-owner', 'Workflow-Contract-Version': 'workflow.v1'}
        assert 0 < timeout <= 5
        calls.append(url)
        response = requests.Response()
        response.status_code = 200
        response._content = json.dumps({'contract_version': 'workflow.v1', 'request_id': 'fixture',
                                        'ok': True, 'result': {'resource_id': 'owned-image', **resource}}).encode()
        return response

    monkeypatch.setattr(requests.Session, 'get', get_owned_resource)
    body = '# Original\n\n![Local proof](images/local-proof.png)\n'
    path = markdown_file(tmp_path, body, {'images/local-proof.png': 'owned-image'})
    result = writing.collect_document_media(
        {'task_id': 'contract-test', 'task_type': 'write', 'query': 'Use this source'},
        file_paths=[str(path)], media_store=str(tmp_path / 'media'), analyze_source_images=False,
    )
    assert len(calls) == 1
    assert not result['warnings']
    assert len(result['media_assets']['assets']) == 1
    asset = next(iter(result['media_assets']['assets'].values()))
    assert hashlib.sha256(Path(asset['local_path']).read_bytes()).hexdigest() == resource['content_hash'][7:]
    assert asset['meta']['markdown_source_references'] == {'0': ['images/local-proof.png']}
    assert Path(result['source_materials'][0]).read_text() == body


@pytest.mark.parametrize('count', [3, 10, 32])
def test_image_budget_scales_with_count_and_caps_total_work(monkeypatch, tmp_path, count):
    now = [0.0]
    monkeypatch.setattr(markdown_inputs.time, 'monotonic', lambda: now[0])

    def read(identifier, timeout):
        assert 0 < timeout <= 5
        now[0] += 6
        return image_resource('red')

    refs = {f'{index:02}.png': f'image-{index}' for index in range(count)}
    path = markdown_file(tmp_path, '\n'.join(f'![x]({name})' for name in refs), refs)
    documents, resources, warnings = markdown_inputs.prepare_markdown_inputs(
        [path], tmp_path / 'media', read_resource=read)
    assert len(resources) == min(count, 30)
    assert len(documents[0]['images']) == min(count, 30)
    assert bool(warnings) == (count > 30)
