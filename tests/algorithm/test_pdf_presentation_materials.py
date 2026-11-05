"""Real PDF images travel through one pool to native and HTML presentations."""
import hashlib
import importlib.util
import io
import json
from pathlib import Path
from zipfile import ZipFile

import fitz
import pytest
from PIL import Image, ImageDraw
from pptx import Presentation

from lazymind.chat.engine.tools.file_resources.pdf_material_images import extract_pdf_images
from lazymind.chat.engine.tools.file_resources.pptx_template_fill import _inspect_page


@pytest.fixture
def pdf(tmp_path):
    image = Image.new('RGBA', (400, 200), (255, 255, 255, 0))
    draw = ImageDraw.Draw(image)
    draw.rectangle((10, 30, 120, 190), fill='blue')
    draw.rectangle((200, 70, 310, 190), fill='green')
    stream = io.BytesIO()
    image.save(stream, format='PNG')
    source = tmp_path / 'evidence.pdf'
    with fitz.open() as doc:
        page = doc.new_page(width=600, height=400)
        page.insert_text((30, 30), 'Figure 1: latency (ms), measured baseline')
        page.insert_image(fitz.Rect(50, 80, 450, 280), stream=stream.getvalue())
        page = doc.new_page(width=600, height=400)
        page.draw_rect(fitz.Rect(50, 100, 180, 300), color=(0, 0, 1), fill=(0, 0, 1))
        page.insert_text((50, 330), 'Vector chart with axis labels')
        doc.save(source)
    return source


def test_extract_embedded_mask_and_vector_region(pdf, tmp_path):
    before = pdf.read_bytes()
    first = extract_pdf_images(pdf, tmp_path / 'extracted')
    assert first['next_page'] == 2
    asset = first['images'][0]
    assert asset['pdf_provenance']['page'] == 1
    assert asset['pdf_provenance']['kind'] == 'embedded_image'
    with Image.open(asset['local_path']) as image:
        assert image.size == (400, 200)
        assert image.mode == 'RGBA'
        assert image.getpixel((0, 0))[3] == 0
    preview = extract_pdf_images(pdf, tmp_path / 'extracted', page=2)
    assert preview['next_page'] is None
    assert preview['images'][0]['pdf_provenance']['kind'] == 'page_preview'
    crop = extract_pdf_images(pdf, tmp_path / 'extracted', page=2, regions=[[30, 80, 220, 350]])
    assert crop['images'][0]['pdf_provenance']['kind'] == 'region'
    with Image.open(crop['images'][0]['local_path']) as image:
        assert image.size == (380, 540)
    assert pdf.read_bytes() == before


@pytest.mark.parametrize('page, regions', [
    (0, None), (True, None), (3, None), (1, []), (1, [[0, 0, 9999, 99]]),
    (1, [[20, 20, 10, 10]]), (1, [[0, 0, float('nan'), 20]]),
])
def test_invalid_page_and_crops_are_rejected(pdf, tmp_path, page, regions):
    with pytest.raises(ValueError):
        extract_pdf_images(pdf, tmp_path / 'extracted', page, regions)


@pytest.fixture
def tools(monkeypatch, tmp_path, pdf):
    spec = importlib.util.spec_from_file_location(
        'pdf_material_tools', Path(__file__).parents[2] / 'workflows/ppt-workflow/scripts/tools.py')
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    monkeypatch.setattr(module, '_conversation_root', lambda: tmp_path / 'conversation')

    def find(filename, turn=None):
        if filename != pdf.name or turn not in (None, 2):
            raise ValueError('Attachment not authorized')
        return {'path': str(pdf)}
    monkeypatch.setattr(module, 'find_user_attachment', find)
    monkeypatch.setattr(module, '_ui_slot_order_list', lambda _: [])
    monkeypatch.setattr(module, '_save_artifact', lambda **kw: {'ok': True})
    from lazymind import vision_model
    monkeypatch.setattr(vision_model, 'is_model_role_available', lambda role: role == 'llm')
    monkeypatch.setattr(vision_model, '_llm_configuration', lambda: {'vision': True})
    monkeypatch.setattr(module, 'get_model_role_runtime_identity', lambda role: {
        'role': role, 'source': 'openai', 'model': 'visual-main',
    })
    module.visual_calls = []

    def inspect(system_prompt, user_prompt, images, **kwargs):
        assert Path(images[0]).is_file()
        module.visual_calls.append(images[0])
        return '蓝绿柱状图，图片未包含坐标轴和图例，需结合论文补齐。'

    monkeypatch.setattr(module, '_agent_vlm_call', inspect)
    return module


def test_shared_pdf_pool_feeds_native_pptx_and_html(tools, pdf, tmp_path, monkeypatch):
    found = tools.ppt_extract_pdf_images(pdf.name, turn=2)
    assert found['images'][0]['visual_inspection']['role'] == 'llm'
    assert len(tools.visual_calls) == 1
    registered = tools.ppt_register_material_images(found['images'])
    assert len(tools.visual_calls) == 1  # Registration reuses inspected pixels.
    returned = tools._tool_payload(registered)['images'][0]
    assert returned['material_index'] == 1
    assert (returned['width'], returned['height']) == (400, 200)
    assert returned['pdf_provenance']['page'] == 1
    assert '坐标轴' in returned['visual_inspection']['description']
    asset = tools._load_material_manifest()['images'][0]
    assert asset['pdf_provenance']['page'] == 1
    assert asset['sha256'] == hashlib.sha256(Path(asset['path']).read_bytes()).hexdigest()
    # HTML receives the same image bytes, PDF attribution and dimensions.
    deck = tmp_path / 'html'
    deck.mkdir()
    (deck / 'info_pack.json').write_text('{}')
    attached = tools._attach_material_images_to_deck(deck)
    html_image = Path(attached['reference_images'][0])
    assert html_image.read_bytes() == Path(asset['path']).read_bytes()
    info = json.loads((deck / 'info_pack.json').read_text(encoding='utf-8'))['user_assets']
    assert 'p. 1' in info['reference_image_captions'][str(html_image)]
    assert '坐标轴' in info['reference_image_captions'][str(html_image)]
    assert info['reference_image_sources'][str(html_image)]['pdf_provenance']['filename'] == pdf.name
    # Native output embeds the selected asset and fits it without distortion/crop.
    source = tmp_path / 'template.pptx'
    prs = Presentation()
    slide = prs.slides.add_slide(prs.slide_layouts[1])
    slide.shapes.title.text = 'Evidence'
    body_id = str(slide.placeholders[1].shape_id)
    prs.save(source)
    page = _inspect_page(source, 1)
    plan = {'source_page': 1, 'remove_shape_ids': [body_id],
            'edits': [{'id': s['id'], 'expected_text': s['text'], 'action': 'keep'}
                      for s in page['slots'] if s['shape_id'] != body_id],
            'images': [{'material_index': 1, 'box': [1, 2, 8, 4]}]}
    published = []
    monkeypatch.setattr(tools, '_resolve_template_file', lambda _: (source, source.name))
    monkeypatch.setattr(tools, '_save_artifact', lambda **kw: published.append(kw) or {'ok': True})
    result = tools.ppt_fill_template(source.name, page['sha256'], pages=[plan])
    assert result['inserted_image_count'] == 1
    output = published[0]['value']
    slide = Presentation(output).slides[0]
    picture = next(s for s in slide.shapes if s.shape_type == 13)
    assert picture.width / picture.height == 2
    assert picture.crop_left == picture.crop_right == picture.crop_top == picture.crop_bottom == 0
    assert 'evidence.pdf' in slide.notes_slide.notes_text_frame.text
    assert 'p. 1' in slide.notes_slide.notes_text_frame.text
    with ZipFile(output) as archive:
        assert any(n.startswith('ppt/media/') for n in archive.namelist())


def test_only_authorized_pdf_and_scoped_registered_images_are_available(tools, pdf, tmp_path):
    with pytest.raises(ValueError, match='authorized'):
        tools.ppt_extract_pdf_images('/unrelated/other.pdf')
    found = tools.ppt_extract_pdf_images(pdf.name)
    tools.ppt_register_material_images(found['images'])
    plan = [{'images': [{'material_index': 1}]}]
    assert tools._native_image_assets(plan)[1]['pdf_provenance']['page'] == 1
    with pytest.raises(ValueError, match='material_index'):
        tools._native_image_assets([{'images': [{'material_index': 2}]}])
    manifest = tools._load_material_manifest()
    asset_path = Path(manifest['images'][0]['path'])
    with Image.open(asset_path) as original:
        modified = original.copy()
    ImageDraw.Draw(modified).rectangle((0, 0, 30, 30), fill='red')
    modified.save(asset_path)
    with pytest.raises(ValueError, match='changed'):
        tools._native_image_assets(plan)
    manifest['images'][0]['path'] = found['images'][0]['local_path']
    tools._write_material_manifest(manifest)
    with pytest.raises(ValueError, match='outside'):
        tools._native_image_assets(plan)


def test_failed_visual_inspection_skips_image_but_keeps_pdf_candidates(tools, pdf, monkeypatch):
    def fail(*args, **kwargs):
        raise RuntimeError('visual service unavailable')
    monkeypatch.setattr(tools, '_agent_vlm_call', fail)
    found = tools.ppt_extract_pdf_images(pdf.name)
    assert found['images'][0]['visual_inspection']['status'] == 'failed'
    result = tools._tool_payload(tools.ppt_register_material_images(found['images']))
    assert result['count'] == 0 and result['reason'] == 'visual_inspection_failed'
    assert tools._load_material_manifest()['images'] == []


def test_caller_cannot_claim_an_unseen_image_was_inspected(tools, pdf, monkeypatch):
    found = tools.ppt_extract_pdf_images(pdf.name)
    image = found['images'][0]
    image['visual_inspection'] = {'status': 'inspected', 'description': 'Invented numerical result'}
    result = tools._tool_payload(tools.ppt_register_material_images([image]))
    assert 'Invented' not in result['images'][0]['visual_inspection']['description']
    # Changed bytes require a fresh inspection, even if a caller supplies old metadata.
    image_path = Path(image['local_path'])
    with Image.open(image_path) as original:
        changed = original.copy()
    ImageDraw.Draw(changed).rectangle((0, 0, 20, 20), fill='red')
    changed.save(image_path)
    tools.ppt_register_material_images([image])
    assert len(tools.visual_calls) == 2


def test_pdf_without_visual_capability_does_not_call_model(tools, pdf, monkeypatch):
    def unavailable():
        raise tools.VisionModelUnavailable('No visual model')
    monkeypatch.setattr(tools, 'select_vision_model_role', unavailable)
    found = tools.ppt_extract_pdf_images(pdf.name)
    assert found['images'][0]['visual_inspection']['status'] == 'unavailable'
    assert tools.visual_calls == []
    result = tools._tool_payload(tools.ppt_register_material_images(found['images']))
    assert result['reason'] == 'vision_unavailable'


def test_pdf_asset_can_be_bound_to_existing_html_page(tools, pdf, tmp_path, monkeypatch):
    tools.ppt_register_material_images(tools.ppt_extract_pdf_images(pdf.name)['images'])
    deck = tmp_path / 'html'
    deck.mkdir()
    (deck / 'info_pack.json').write_text('{}')
    (deck / 'outline.json').write_text(json.dumps({'pages': [{'page_no': 1, 'title': 'Evidence'}]}))
    monkeypatch.setattr(tools, '_resolve_deck_dir', lambda _: deck)
    monkeypatch.setattr(tools, '_publish_one_slide_outline', lambda *a: {'ok': True})
    stages = []

    def render(deck_dir, stage, **kwargs):
        stages.append((stage, kwargs))
        return {'status': 'ok'}
    monkeypatch.setattr(tools, 'ppt_run_stage', render)
    result = tools.ppt_replace_page_material_image(str(deck), 1, material_index=1)
    assert not tools._tool_failed(result)
    outline = json.loads((deck / 'outline.json').read_text(encoding='utf-8'))
    assert outline['pages'][0]['use_image'] == {'reference_image_index': 0}
    assert stages == [('asset-plan', {}), ('page-html', {'page': 1})]


def test_encrypted_pdf_is_not_silently_rendered(pdf, tmp_path):
    protected = tmp_path / 'protected.pdf'
    with fitz.open(pdf) as document:
        document.save(protected, encryption=fitz.PDF_ENCRYPT_AES_256, owner_pw='owner', user_pw='reader')
    with pytest.raises(ValueError, match='Password-protected'):
        extract_pdf_images(protected, tmp_path / 'extracted')
