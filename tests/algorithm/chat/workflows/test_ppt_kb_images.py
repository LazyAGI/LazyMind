"""Stored OCR images remain usable when optional retrieval is unavailable."""
import importlib.util
import json
from pathlib import Path

import pytest
from PIL import Image, ImageDraw

from lazymind.chat.engine.tools.file_resources import kb_material_images as kb

ROOT = Path(__file__).resolve().parents[4]


@pytest.fixture
def uploads(tmp_path, monkeypatch):
    monkeypatch.setenv('LAZYMIND_UPLOAD_ROOT', str(tmp_path))
    image = tmp_path / 'normalized images' / 'figure.png'
    image.parent.mkdir()
    figure = Image.new('RGB', (300, 200), 'white')
    ImageDraw.Draw(figure).rectangle((30, 40, 250, 160), fill='teal')
    figure.save(image)
    return image


def core_page(monkeypatch, segments, next_token=''):
    calls = []

    def get(path, params=None):
        calls.append((path, params))
        if path.endswith('/segments'):
            return {'segments': segments, 'total_size': 5, 'next_page_token': next_token}
        return {'display_name': 'Research.pdf'}

    monkeypatch.setattr(kb, 'get_core_api', get)
    return calls


def test_base_chunks_keep_images_and_provenance_without_rag(monkeypatch, uploads):
    # Guard against accidentally routing collection through the optional RAG wrapper.
    import builtins
    original = builtins.__import__

    def no_rag(name, *args, **kwargs):
        if 'lazy_kb' in name or '.rag' in name or 'retriever' in name:
            pytest.fail(f'optional retrieval dependency: {name}')
        return original(name, *args, **kwargs)

    monkeypatch.setattr(builtins, '__import__', no_rag)
    calls = core_page(monkeypatch, [
        {'segment_id': 'text', 'number': 0, 'text': 'Research findings'},
        {'segment_id': 'image', 'number': 1, 'text': str(uploads)},
        {'segment_id': 'markdown', 'number': 2,
         'text': f'![Figure 1](<{uploads}>)\n![Result](https://example.org/result.png?version=2)'},
    ])
    result = kb.collect_document_images('dataset', 'document')
    assert result['count'] == 2
    first, second = result['images']
    assert first['local_path'] == str(uploads)
    assert first['kb_provenance'] == {
        'knowledge_base_id': 'dataset', 'document_id': 'document', 'file_name': 'Research.pdf',
        'chunk_id': 'image', 'chunk_number': 1,
    }
    assert second['url'] == 'https://example.org/result.png?version=2'
    assert second['caption'] == 'Result'
    assert calls == [('/datasets/dataset/documents/document', None),
                     ('/datasets/dataset/documents/document/segments', {'page_size': 50, 'page_token': ''})]


def test_empty_page_cursor_and_image_metadata(monkeypatch, uploads):
    core_page(monkeypatch, [{'text': 'text only'}], next_token='next')
    result = kb.collect_document_images('ds', 'doc')
    assert result['images'] == [] and result['has_more'] and result['next_page_token'] == 'next'
    core_page(monkeypatch, [{
        'segment_id': 'fig', 'metadata': {'normalized_source_path': str(uploads), 'page': 8, 'bbox': [1, 2, 3, 4]},
        'image_keys': ['/static-files/normalized%20images/figure.png?expires=123&sig=redacted'],
        'display_content': '<img src="https://example.org/chart.jpg?a=1&amp;b=2">',
    }])
    result = kb.collect_document_images('ds', 'doc', page_token='next')
    assert result['count'] == 2 and not result['has_more']
    assert result['images'][0]['kb_provenance']['page'] == 8
    assert result['images'][1]['url'].endswith('?a=1&b=2')


def test_disabled_missing_and_outside_upload_paths_are_not_collected(monkeypatch, uploads, tmp_path):
    outside = tmp_path.parent / (tmp_path.name + '-outside.png')
    Image.new('RGB', (2, 2)).save(outside)
    symlink = uploads.parent / 'escape.png'
    symlink.symlink_to(outside)
    try:
        core_page(monkeypatch, [
            {'text': str(uploads), 'enabled': False}, {'text': str(outside)}, {'text': str(symlink)},
            {'text': '/static-files/../escape.png'}, {'text': str(uploads.parent / 'missing.png')},
            {'text': '[PDF](https://example.org/source.pdf)'}, {'text': 'file:///private/secret.png'},
            {'text': 'https://user:password@example.org/image.png'},
        ])
        assert kb.collect_document_images('ds', 'doc')['images'] == []
    finally:
        outside.unlink()


def test_permission_error_stops_before_reading_chunks(monkeypatch):
    calls = []

    def denied(path, params=None):
        calls.append(path)
        raise PermissionError('not authorized')

    monkeypatch.setattr(kb, 'get_core_api', denied)
    with pytest.raises(PermissionError):
        kb.collect_document_images('ds', 'other-document')
    assert calls == ['/datasets/ds/documents/other-document']


@pytest.mark.parametrize('size', [0, 101, True, '50'])
def test_invalid_page_size_makes_no_request(monkeypatch, size):
    monkeypatch.setattr(kb, 'get_core_api', lambda *a, **kw: pytest.fail('unexpected request'))
    with pytest.raises(ValueError):
        kb.collect_document_images('ds', 'doc', page_size=size)


def test_repeated_cursor_is_reported(monkeypatch):
    core_page(monkeypatch, [], next_token='same')
    with pytest.raises(ValueError, match='repeated'):
        kb.collect_document_images('ds', 'doc', page_token='same')


def test_collected_image_is_registered_and_attached_with_source(monkeypatch, uploads, tmp_path):
    spec = importlib.util.spec_from_file_location('_ppt_kb_tools', ROOT / 'workflows/ppt-workflow/scripts/tools.py')
    tools = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(tools)
    material_root = tmp_path / 'materials'
    material_root.mkdir()
    monkeypatch.setattr(tools, '_material_root', lambda: material_root)
    monkeypatch.setattr(tools, 'select_vision_model_role', lambda: 'vlm')
    monkeypatch.setattr(tools, '_inspect_material_image', lambda path, role: {
        'status': 'inspected', 'description': 'Diagram of the described method', 'role': role})
    monkeypatch.setattr(tools, '_save_artifact', lambda **kw: {'success': True})
    monkeypatch.setattr(tools, '_ui_slot_order_list', lambda *a: [])
    core_page(monkeypatch, [{'segment_id': 'image', 'text': str(uploads)}])
    collected = tools.ppt_collect_kb_images('ds', 'doc')
    result = tools.ppt_register_material_images(collected['images'])
    assert result['count'] == 1
    manifest = tools._load_material_manifest()
    assert manifest['images'][0]['kb_provenance']['chunk_id'] == 'image'
    assert Path(manifest['images'][0]['path']).read_bytes() == uploads.read_bytes()
    deck = tmp_path / 'deck'
    deck.mkdir()
    (deck / 'info_pack.json').write_text('{}')
    assert tools._attach_material_images_to_deck(deck)['attached'] == 1
    info = json.loads((deck / 'info_pack.json').read_text())
    source = next(iter(info['user_assets']['reference_image_sources'].values()))
    assert source['source'] == 'kb:ds/doc' and source['kb_provenance']['chunk_id'] == 'image'
    assert tools._native_image_assets([{'images': [{'material_index': 1}]}])[1]['kb_provenance']['chunk_id'] == 'image'
