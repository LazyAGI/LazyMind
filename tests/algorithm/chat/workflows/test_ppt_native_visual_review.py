"""Visual quality is best effort after a usable presentation is delivered."""
import importlib.util
import json
from pathlib import Path

import pytest
from pptx import Presentation

from lazymind.chat.engine.tools.file_resources import pptx_visual_review as visual
from lazymind.chat.engine.tools.file_resources.pptx_template_fill import _inspect_page


@pytest.fixture
def tools():
    path = Path(__file__).resolve().parents[4] / 'workflows/ppt-workflow/scripts/tools.py'
    spec = importlib.util.spec_from_file_location('_test_ppt_visual_review', path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def test_review_submits_pixels_in_batches_and_requires_all_page_findings(tmp_path):
    images = [tmp_path / f'{i}.png' for i in range(5)]
    calls = []

    def vision(system, prompt, paths):
        calls.append(paths)
        numbers = [1, 2, 3, 4] if len(calls) == 1 else [5]
        return json.dumps({'pages': [{'page': n, 'issues': []} for n in numbers]})

    assert visual.review_pages(images, vision)['status'] == 'passed'
    assert calls == [[str(p) for p in images[:4]], [str(images[4])]]
    with pytest.raises(ValueError, match='every requested page'):
        visual.review_pages(images[:1], lambda *_: '{"pages":[]}')


def _color_plan(prompt, color='FFFFFF'):
    from lxml import etree
    payload = json.loads(prompt)
    xml = etree.fromstring(payload['parts']['ppt/slides/slide1.xml'].encode())
    ns = '{http://schemas.openxmlformats.org/drawingml/2006/main}'
    for run in xml.iter(ns + 'r'):
        props = run.find(ns + 'rPr')
        if props is None:
            props = etree.Element(ns + 'rPr')
            run.insert(0, props)
        for child in list(props):
            if child.tag.endswith('solidFill'):
                props.remove(child)
        etree.SubElement(etree.SubElement(props, ns + 'solidFill'), ns + 'srgbClr', val=color)
    return json.dumps({'parts': [{'path': 'ppt/slides/slide1.xml',
                                  'xml': etree.tostring(xml).decode()}]})


def test_generator_delivers_first_then_repairs_color_with_screenshots(tools, tmp_path, monkeypatch):
    source = tmp_path / 'source.pptx'
    prs = Presentation()
    prs.slides.add_slide(prs.slide_layouts[1]).shapes.title.text = 'Original'
    prs.save(source)
    info = _inspect_page(source, 1)
    edits = [{'id': s['id'], 'expected_text': s['text'], 'action': 'replace', 'text': 'Approved content'}
             for s in info['slots']]
    monkeypatch.setattr(tools, '_resolve_template_file', lambda _: (source, source.name))
    monkeypatch.setattr(tools, '_conversation_root', lambda: tmp_path)
    monkeypatch.setattr(tools, '_native_image_assets', lambda _: {})
    monkeypatch.setattr(tools, '_material_vision_role', lambda: 'vlm')
    monkeypatch.setattr(tools, '_ppt_cancel_check', lambda: lambda: None)
    screenshot = tmp_path / 'page-001.png'
    monkeypatch.setattr(visual, 'render_pages', lambda *_: [screenshot])
    published, received = [], []
    monkeypatch.setattr(tools, '_save_artifact', lambda **kw: published.append(kw) or {'ok': True})

    def vision(system, prompt, images):
        received.append(images)
        assert len(published) == 1
        if 'Repair the visible' in system:
            return _color_plan(prompt)
        issues = [{'shape_ids': [], 'problem': 'Dark text on dark background', 'fix': 'Use white text'}]
        return json.dumps({'pages': [{'page': 1, 'issues': issues if len(received) == 1 else []}]})

    monkeypatch.setattr(tools, '_agent_vlm_call', vision)
    result = tools.ppt_fill_template(source.name, info['sha256'], edits=edits)
    assert result['visual_review']['status'] == 'passed'
    assert result['visual_validation'] == 'rendered_model_review'
    assert received == [[str(screenshot)]] * 3
    assert len(published) == 2 and published[0]['value'] != published[1]['value']
    assert all(p['key'] == 'native_pptx' for p in published)
    before, after = [Presentation(p['value']) for p in published]
    assert [s.text for s in before.slides[0].shapes] == [s.text for s in after.slides[0].shapes]
    assert str(after.slides[0].shapes.title.text_frame.paragraphs[0].runs[0].font.color.rgb) == 'FFFFFF'


def test_missing_vision_is_disclosed_without_inventing_a_review(tools, tmp_path, monkeypatch):
    screenshot = tmp_path / 'page-001.png'
    monkeypatch.setattr(visual, 'render_pages', lambda *_: [screenshot])
    monkeypatch.setattr(tools, '_material_vision_role', lambda: '')
    result = tools._review_native_pptx(tmp_path / 'deck.pptx', tmp_path)
    assert result['status'] == 'unavailable'
    assert result['screenshots'] == [str(screenshot)]
    assert 'vision' in result['reason']


@pytest.mark.parametrize('failure', ['renderer', 'reviewer', 'invalid_repair', 'repair_publication'])
def test_optional_quality_failure_keeps_delivered_file(tools, tmp_path, monkeypatch, failure):
    source = tmp_path / 'presentation.pptx'
    prs = Presentation()
    prs.slides.add_slide(prs.slide_layouts[1]).shapes.title.text = 'Keep this content'
    prs.save(source)
    original = source.read_bytes()
    delivered, reviews = [], []

    def publish(**kwargs):
        if failure == 'repair_publication' and delivered:
            raise RuntimeError('optional revision upload failed')
        delivered.append(kwargs)
        return {'ok': True}

    def render(*args):
        assert delivered, 'File must be delivered before any optional rendering'
        if failure == 'renderer':
            raise RuntimeError('renderer unavailable')
        return [tmp_path / 'page.png']

    def vision(system, prompt, images):
        if 'Repair the visible' in system:
            return 'not json' if failure == 'invalid_repair' else _color_plan(prompt)
        reviews.append(True)
        if failure == 'reviewer':
            raise RuntimeError('visual model timed out')
        issues = [{'shape_ids': [], 'problem': 'low contrast', 'fix': 'white text'}]
        return json.dumps({'pages': [{'page': 1, 'issues': issues if len(reviews) == 1 else []}]})

    monkeypatch.setattr(tools, '_save_artifact', publish)
    monkeypatch.setattr(tools, '_material_vision_role', lambda: 'vlm')
    monkeypatch.setattr(tools, '_ppt_cancel_check', lambda: lambda: None)
    monkeypatch.setattr(visual, 'render_pages', render)
    monkeypatch.setattr(tools, '_agent_vlm_call', vision)
    result = tools._deliver_native_pptx(source, tmp_path, 'ppt_fill_template')
    assert result['delivery_preserved'] is True
    assert len(delivered) == 1 and source.read_bytes() == original
    assert Presentation(delivered[0]['value']).slides[0].shapes.title.text == 'Keep this content'
    if failure == 'repair_publication':
        assert 'optional revision upload failed' in result['repair_error']


@pytest.mark.parametrize('stop', ['passed', 'repeat', 'cycle', 'cancel', 'stale'])
def test_native_repairs_continue_beyond_two_rounds_and_preserve_delivery(tools, tmp_path, monkeypatch, stop):
    from lazymind.chat.engine.agent_runtime.cancellation import UserCancelledError
    source = tmp_path / 'presentation.pptx'
    prs = Presentation()
    prs.slides.add_slide(prs.slide_layouts[1]).shapes.title.text = 'Content'
    prs.save(source)
    delivered, calls = [], []
    monkeypatch.setattr(tools, '_save_artifact', lambda **kw: delivered.append(kw) or {'ok': True})
    monkeypatch.setattr(tools, '_review_native_pptx', lambda *_: {
        'status': 'passed' if stop == 'passed' and len(calls) >= 3 else 'needs_revision',
        'screenshots': ['page.png'], 'pages': [{'page': 1, 'issues': []}]})

    def cancel():
        if stop == 'cancel' and len(calls) >= 3:
            raise UserCancelledError('stopped by user')

    def repair(_system, prompt, images):
        assert images == ['page.png']
        calls.append(prompt)
        if stop == 'stale':
            source.write_bytes(source.read_bytes() + b'new revision')
        color = 'FFFFFF' if stop == 'repeat' else ('000001' if stop == 'cycle' and len(calls) == 3
                                                   else f'{len(calls):06X}')
        return _color_plan(prompt, color)

    monkeypatch.setattr(tools, '_ppt_cancel_check', lambda: cancel)
    monkeypatch.setattr(tools, '_agent_vlm_call', repair)
    if stop == 'cancel':
        with pytest.raises(UserCancelledError):
            tools._deliver_native_pptx(source, tmp_path, 'ppt_fill_template')
        assert len(delivered) == 1
        return
    result = tools._deliver_native_pptx(source, tmp_path, 'ppt_fill_template')
    if stop == 'passed':
        assert result['repairs'] == 3 and result['repair_adopted'] is True
        assert len(delivered) == 2
    else:
        assert result['repair_adopted'] is False and len(delivered) == 1
        assert ('no change' if stop == 'repeat' else 'repeated' if stop == 'cycle'
                else 'Deck changed') in result['repair_error']


@pytest.mark.parametrize('shape_ids', [[999], ['2'], [True], None])
def test_visual_review_rejects_uninspected_shape_ids(tmp_path, shape_ids):
    layout = [{'page': 1, 'shapes': [{'shape_id': 2}]}]
    response = {'pages': [{'page': 1, 'issues': [
        {'shape_ids': shape_ids, 'problem': 'overlap', 'fix': 'move'}]}]}
    with pytest.raises(ValueError, match='inspected shape_ids'):
        visual.review_pages([tmp_path / 'page.png'], lambda *_: json.dumps(response), layout)


def test_missing_screenshot_cannot_count_as_passed_review(tmp_path):
    with pytest.raises(ValueError, match='every slide'):
        visual.review_pages([], lambda *_: '{"pages":[]}')
    with pytest.raises(ValueError, match='every slide'):
        visual.review_pages([tmp_path / 'page.png'], lambda *_: '', [{'page': 1}, {'page': 2}])


def test_native_visual_edits_cover_media_background_table_chart_and_theme(tmp_path):
    import base64
    import hashlib
    import io
    from zipfile import ZipFile
    from PIL import Image
    from pptx.chart.data import CategoryChartData
    from pptx.enum.chart import XL_CHART_TYPE
    from pptx.util import Inches
    from lazymind.chat.engine.tools.file_resources.pptx_visual_edit import apply_visual_edits, visual_source

    source, output = tmp_path / 'source.pptx', tmp_path / 'fixed.pptx'
    prs = Presentation()
    slide = prs.slides.add_slide(prs.slide_layouts[6])
    slide.shapes.add_table(1, 1, Inches(1), Inches(1), Inches(2), Inches(1)).table.cell(0, 0).text = 'Sample'
    data = CategoryChartData()
    data.categories = ['A']
    data.add_series('Original label', [7])
    slide.shapes.add_chart(XL_CHART_TYPE.COLUMN_CLUSTERED, Inches(4), Inches(1), Inches(3), Inches(3), data)
    picture = io.BytesIO()
    Image.new('RGB', (20, 20), 'red').save(picture, 'PNG')
    picture.seek(0)
    slide.shapes.add_picture(picture, Inches(1), Inches(3))
    prs.save(source)
    original = source.read_bytes()
    parts = visual_source(source)['parts']
    slide_xml = parts['ppt/slides/slide1.xml'].replace('Sample', 'Readable table')
    slide_xml = slide_xml.replace('<p:spTree>', '<p:bg><p:bgPr><a:solidFill><a:srgbClr val="123456"/>'
                                  '</a:solidFill><a:effectLst/></p:bgPr></p:bg><p:spTree>')
    image = io.BytesIO()
    Image.new('RGB', (20, 20), 'blue').save(image, 'PNG')
    changes = [
        {'path': 'ppt/slides/slide1.xml', 'xml': slide_xml},
        {'path': 'ppt/charts/chart1.xml', 'xml': parts['ppt/charts/chart1.xml'].replace('Original label', 'New label')},
        {'path': 'ppt/theme/theme1.xml', 'xml': parts['ppt/theme/theme1.xml'].replace('Calibri', 'Arial')},
        {'path': 'ppt/media/image1.png', 'data_base64': base64.b64encode(image.getvalue()).decode()},
    ]
    result = apply_visual_edits(source, output, {'parts': changes},
                                source_sha256=hashlib.sha256(original).hexdigest())
    assert result['changed_parts'] == sorted(p['path'] for p in changes)
    edited = Presentation(output)
    assert edited.slides[0].shapes[0].table.cell(0, 0).text == 'Readable table'
    assert str(edited.slides[0].background.fill.fore_color.rgb) == '123456'
    assert edited.slides[0].shapes[1].chart.series[0].name == 'New label'
    assert edited.slides[0].shapes[1].chart.series[0].values == (7.0,)
    assert edited.slides[0].shapes[2].image.blob == image.getvalue()
    assert source.read_bytes() == original
    with ZipFile(source) as before, ZipFile(output) as after:
        for name in before.namelist():
            if name not in result['changed_parts']:
                assert before.read(name) == after.read(name)


@pytest.mark.parametrize('failure', ['xml', 'relationship', 'path', 'stale', 'empty', 'binary'])
def test_native_visual_edits_reject_invalid_packages_without_overwriting(tmp_path, failure):
    import hashlib
    from lazymind.chat.engine.tools.file_resources.pptx_visual_edit import apply_visual_edits, visual_source

    source, output = tmp_path / 'source.pptx', tmp_path / 'fixed.pptx'
    prs = Presentation()
    prs.slides.add_slide(prs.slide_layouts[1]).shapes.title.text = 'Content'
    prs.save(source)
    original = source.read_bytes()
    output.write_bytes(b'last usable candidate')
    parts = visual_source(source)['parts']
    change = {'path': 'ppt/slides/slide1.xml', 'xml': parts['ppt/slides/slide1.xml'].replace('Content', 'Updated')}
    if failure == 'xml':
        change['xml'] = '<broken>'
    elif failure == 'relationship':
        name = 'ppt/slides/_rels/slide1.xml.rels'
        change = {'path': name, 'xml': parts[name].replace('../slideLayouts/slideLayout2.xml', '../missing.xml')}
    elif failure == 'path':
        change['path'] = 'ppt/../../outside.xml'
    elif failure == 'empty':
        change['xml'] = parts['ppt/slides/slide1.xml']
    elif failure == 'binary':
        change = {'path': 'ppt/media/image1.png', 'data_base64': 'invalid!'}
    from lxml.etree import XMLSyntaxError
    with pytest.raises((ValueError, XMLSyntaxError)):
        apply_visual_edits(source, output, {'parts': [change]},
                           source_sha256='stale' if failure == 'stale' else hashlib.sha256(original).hexdigest())
    assert source.read_bytes() == original
    assert output.read_bytes() == b'last usable candidate'
