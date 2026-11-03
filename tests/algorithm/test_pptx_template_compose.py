"""Exercise editable output packages, not just the composition prompt."""
import hashlib
from copy import deepcopy
from zipfile import ZipFile

import pytest
from pptx import Presentation
from pptx.util import Inches

from lazymind.chat.engine.tools.file_resources.pptx_template_fill import _inspect_page
from lazymind.chat.engine.tools.file_resources.pptx_template_compose import compose_template
from lazymind.chat.engine.tools.file_resources.pptx_content_edit import inspect_content, edit_content


@pytest.fixture
def template(tmp_path):
    source = tmp_path / 'template.pptx'
    prs = Presentation()
    slide = prs.slides.add_slide(prs.slide_layouts[1])
    slide.shapes.title.text = 'Sample title'
    slide.placeholders[1].text = 'Sample body'
    slide.notes_slide.notes_text_frame.text = 'Sample private notes'
    marker = slide.shapes.add_shape(1, Inches(.1), Inches(.1), Inches(.1), Inches(.1))
    marker.name = 'Brand mark'
    prs.slides.add_slide(prs.slide_layouts[1]).shapes.title.text = 'Unused sample page'
    prs.save(source)
    info = _inspect_page(source, 1)
    body_id = str(slide.placeholders[1].shape_id)
    page = {'source_page': 1, 'remove_shape_ids': [body_id],
            'edits': [{'id': s['id'], 'expected_text': s['text'], 'action': 'replace', 'text': 'Results'}
                      for s in info['slots'] if s['shape_id'] != body_id],
            'notes': 'Measured under identical conditions.',
            'charts': [{'type': 'column', 'title': 'Latency', 'unit': 'ms', 'source': 'Benchmark A',
                        'box': [1, 2, 8, 4], 'categories': ['Before', 'After'],
                        'series': [{'name': 'Latency', 'values': [100, 60]}]}]}
    return source, tmp_path / 'output.pptx', info['sha256'], page


def test_scientific_palette_rotates_across_charts_and_distinguishes_bars(template):
    source, output, digest, page = template
    compose_template(source, output, digest, [deepcopy(page) for _ in range(3)])
    prs = Presentation(output)
    colors = []
    for slide in prs.slides:
        chart = next(s.chart for s in slide.shapes if s.has_chart)
        point_colors = [str(p.format.fill.fore_color.rgb) for p in chart.series[0].points]
        assert len(set(point_colors)) == 2
        colors.append(point_colors[0])
        assert str(chart.value_axis.major_gridlines.format.line.color.rgb) == 'E2E8F0'
    assert colors == ['326BE6', '139F91', 'DD8B32']


def test_page_chart_edit_preserves_every_other_package_part(template, tmp_path):
    source, output, digest, page = template
    compose_template(source, output, digest, [deepcopy(page) for _ in range(2)])
    inspected = inspect_content(output, [2])
    assert inspected['pages'][0]['charts'][0]['chart_index'] == 1
    edited = tmp_path / 'recolored.pptx'
    result = edit_content(output, edited, inspected['sha256'],
                          chart_edits=[{'page': 2, 'chart_index': 1, 'palette': 'purple'}])
    assert result['updated_chart_pages'] == [2]
    with ZipFile(output) as before, ZipFile(edited) as after:
        changed = [name for name in before.namelist() if before.read(name) != after.read(name)]
        assert len(changed) == 1 and changed[0].startswith('ppt/charts/chart')
    chart = next(s.chart for s in Presentation(edited).slides[1].shapes if s.has_chart)
    assert str(chart.series[0].points[0].format.fill.fore_color.rgb) == '8064B0'
    assert list(chart.series[0].values) == [100, 60]


def test_multiple_series_have_distinct_explicit_colors(template):
    source, output, digest, page = template
    page['charts'][0]['series'].append({'name': 'Comparator', 'values': [110, 90]})
    page['charts'][0]['palette'] = ['#0072B2', '#E69F00']
    compose_template(source, output, digest, [page])
    chart = next(s.chart for s in Presentation(output).slides[0].shapes if s.has_chart)
    assert [str(s.format.fill.fore_color.rgb) for s in chart.series] == ['0072B2', 'E69F00']


def test_repeated_layouts_remove_samples_and_embed_editable_charts(template):
    source, output, digest, page = template
    original = source.read_bytes()
    pages = [deepcopy(page) for _ in range(3)]
    pages[1]['edits'][0]['text'] = 'Independent page'
    pages[1]['charts'][0]['series'][0]['values'] = [200, 80]
    result = compose_template(source, output, digest, pages)
    assert result['slide_count'] == 3
    assert result['editable_chart_count'] == 3
    assert source.read_bytes() == original
    prs = Presentation(output)
    assert [s.shapes.title.text for s in prs.slides] == ['Results', 'Independent page', 'Results']
    for index, slide in enumerate(prs.slides):
        assert slide.slide_layout.name == 'Title and Content'
        assert any(s.name == 'Brand mark' for s in slide.shapes)
        assert 'Sample body' not in '\n'.join(s.text for s in slide.shapes if s.has_text_frame)
        charts = [s.chart for s in slide.shapes if s.has_chart]
        assert len(charts) == 1
        chart_ns = '{http://schemas.openxmlformats.org/drawingml/2006/chart}'
        ids = {e.get('val') for e in charts[0]._chartSpace.iter(chart_ns + 'axId')}
        refs = {e.get('val') for e in charts[0]._chartSpace.iter(chart_ns + 'crossAx')}
        assert len(ids) == 2 and refs == ids
        assert all(0 < int(value) < 2 ** 31 for value in ids)
        assert list(charts[0].series[0].values) == ([200, 80] if index == 1 else [100, 60])
        assert 'ms' in charts[0].chart_title.text_frame.text
        assert 'Benchmark A' in slide.notes_slide.notes_text_frame.text
        assert 'Sample private notes' not in slide.notes_slide.notes_text_frame.text
    with ZipFile(output) as archive:
        assert len([n for n in archive.namelist() if n.startswith('ppt/embeddings/')]) == 3
        assert not any(b'Unused sample page' in archive.read(n) or b'Sample private notes' in archive.read(n)
                       for n in archive.namelist() if n.endswith('.xml'))


def test_no_chart_composition_discards_unused_sample_pages(template):
    source, output, digest, page = template
    page['charts'] = []
    compose_template(source, output, digest, [page])
    with ZipFile(output) as archive:
        assert not any(b'Unused sample page' in archive.read(n)
                       for n in archive.namelist() if n.endswith('.xml'))


@pytest.mark.parametrize('change, message', [
    (lambda p: p.update(source_page=99), 'source_page'),
    (lambda p: p.update(remove_shape_ids=['999']), 'remove_shape_ids'),
    (lambda p: p.update(edits=[]), 'surviving ORIGINAL'),
    (lambda p: p['edits'][0].update(expected_text='Stale'), 'Stale text'),
    (lambda p: p['charts'][0].update(box=[0, 0, 100, 5]), 'inside the slide'),
    (lambda p: p['charts'][0].update(box=[1, .5, 8, 4]), 'overlaps'),
    (lambda p: p['charts'][0].update(source=''), 'source citation'),
    (lambda p: p['charts'][0]['series'][0].update(values=[float('nan'), 60]), 'finite'),
    (lambda p: p['charts'][0]['series'][0].update(values=[60]), 'matching categories'),
    (lambda p: p['edits'][0].update(text='Too long ' * 500), 'does not fit'),
])
def test_invalid_composition_never_replaces_output(template, change, message):
    source, output, digest, page = template
    output.write_bytes(b'existing revision')
    change(page)
    with pytest.raises(ValueError, match=message):
        compose_template(source, output, digest, [page])
    assert output.read_bytes() == b'existing revision'
    assert hashlib.sha256(source.read_bytes()).hexdigest() == digest


def test_hash_and_overwrite_guards(template):
    source, output, digest, page = template
    with pytest.raises(ValueError, match='Template changed'):
        compose_template(source, output, 'stale', [page])
    with pytest.raises(ValueError, match='overwrite'):
        compose_template(source, source, digest, [page])


@pytest.mark.parametrize('kind', ['bar', 'line'])
def test_chart_types_and_series_remain_editable(template, kind):
    source, output, digest, page = template
    page['charts'][0]['type'] = kind
    page['charts'][0]['series'].append({'name': 'Baseline', 'values': [120, 120]})
    compose_template(source, output, digest, [page])
    chart = next(s.chart for s in Presentation(output).slides[0].shapes if s.has_chart)
    assert len(chart.series) == 2
    assert chart.has_legend


@pytest.mark.parametrize('kind', ['pie', 'doughnut'])
def test_parts_of_whole_are_editable_with_distinct_slices_and_percent_labels(template, kind):
    from pptx.enum.chart import XL_CHART_TYPE
    source, output, digest, page = template
    page['charts'][0].update({
        'type': kind, 'title': 'Workload composition', 'unit': 'requests',
        'categories': ['Text', 'Image', 'Audio'],
        'series': [{'name': 'All requests', 'values': [60, 30, 10]}],
        'source': 'Same-period request counts, n=100',
    })
    compose_template(source, output, digest, [page])
    slide = Presentation(output).slides[0]
    chart = next(s.chart for s in slide.shapes if s.has_chart)
    assert chart.chart_type == (XL_CHART_TYPE.PIE if kind == 'pie' else XL_CHART_TYPE.DOUGHNUT)
    assert list(chart.series[0].values) == [60, 30, 10]
    assert [c.label for c in chart.plots[0].categories] == ['Text', 'Image', 'Audio']
    assert len({str(p.format.fill.fore_color.rgb) for p in chart.series[0].points}) == 3
    assert chart.has_legend
    assert chart.plots[0].data_labels.show_percentage
    assert not chart.plots[0].data_labels.show_value
    assert 'n=100' in slide.notes_slide.notes_text_frame.text
    if kind == 'doughnut':
        assert chart._chartSpace.xpath('.//c:doughnutChart/c:holeSize')[0].get('val') == '60'
    with ZipFile(output) as package:
        assert any(name.startswith('ppt/embeddings/') and name.endswith('.xlsx') for name in package.namelist())


@pytest.mark.parametrize('kind', ['pie', 'doughnut'])
@pytest.mark.parametrize('series', [
    [{'name': 'Invalid', 'values': [-5, 105]}],
    [{'name': 'Invalid', 'values': [0, 0]}],
    [{'name': 'Before', 'values': [10, 20]}, {'name': 'After', 'values': [5, 10]}],
])
def test_parts_of_whole_reject_negative_empty_or_multiple_wholes(template, kind, series):
    source, output, digest, page = template
    page['charts'][0].update({'type': kind, 'series': series})
    with pytest.raises(ValueError, match='Pie/doughnut'):
        compose_template(source, output, digest, [page])


def test_removed_sample_chart_drops_its_workbook_but_keeps_brand_image(template, tmp_path):
    from pptx.chart.data import CategoryChartData
    from pptx.enum.chart import XL_CHART_TYPE
    from PIL import Image

    source, output, _, page = template
    prs = Presentation(source)
    slide = prs.slides[0]
    image = tmp_path / 'brand.png'
    Image.new('RGB', (20, 20), 'blue').save(image)
    slide.shapes.add_picture(str(image), Inches(.2), Inches(.2), Inches(.2), Inches(.2))
    data = CategoryChartData()
    data.categories = ['Fake sample']
    data.add_series('Fake sample', [999])
    sample = slide.shapes.add_chart(XL_CHART_TYPE.COLUMN_CLUSTERED, Inches(1), Inches(2),
                                    Inches(8), Inches(4), data)
    page['remove_shape_ids'].append(str(sample.shape_id))
    prs.save(source)
    compose_template(source, output, hashlib.sha256(source.read_bytes()).hexdigest(), [page])
    with ZipFile(output) as archive:
        assert len([n for n in archive.namelist() if n.startswith('ppt/embeddings/')]) == 1
        assert len([n for n in archive.namelist() if n.startswith('ppt/media/')]) == 1
        assert not any(b'Fake sample' in archive.read(n) for n in archive.namelist() if n.endswith('.xml'))


def test_workflow_publishes_composed_native_deck_and_rejects_ambiguous_requests(template, monkeypatch, tmp_path):
    import importlib.util
    from pathlib import Path

    source, _, digest, page = template
    spec = importlib.util.spec_from_file_location(
        'ppt_composition_test_tools', Path(__file__).parents[2] / 'workflows/ppt-workflow/scripts/tools.py')
    tools = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(tools)
    monkeypatch.setattr(tools, '_resolve_template_file', lambda _: (source, source.name))
    monkeypatch.setattr(tools, '_conversation_root', lambda: tmp_path)
    published = []
    monkeypatch.setattr(tools, '_save_artifact', lambda **kw: published.append(kw) or {'ok': True})
    result = tools.ppt_fill_template(source.name, digest, pages=[page, deepcopy(page)])
    assert result['representation'] == 'native_pptx'
    assert result['slide_count'] == 2
    assert result['editable_chart_count'] == 2
    assert result['generation_status'] == 'completed'
    assert published[0]['key'] == 'native_pptx'
    assert len(Presentation(published[0]['value']).slides) == 2
    with pytest.raises(ValueError, match='exactly one'):
        tools.ppt_fill_template(source.name, digest, edits=[], pages=[page])
    with pytest.raises(ValueError, match='exactly one'):
        tools.ppt_fill_template(source.name, digest)
    assert len(published) == 1


def test_repeated_page_number_fields_are_renumbered(template):
    from lxml import etree
    from lazymind.chat.engine.tools.file_resources.pptx_template_fill import NS

    source, output, _, page = template
    prs = Presentation(source)
    box = prs.slides[0].shapes.add_textbox(Inches(9), Inches(7), Inches(.5), Inches(.3))
    field = etree.SubElement(box.text_frame.paragraphs[0]._p, '{%s}fld' % NS['a'],
                             id='{FA2772E8-A789-444F-9BB3-5BBCBDF12D10}', type='slidenum')
    etree.SubElement(field, '{%s}t' % NS['a']).text = '1'
    prs.save(source)
    info = _inspect_page(source, 1)
    page['edits'] = [{'id': s['id'], 'expected_text': s['text'], 'action': 'keep'}
                     for s in info['slots'] if s['shape_id'] not in page['remove_shape_ids']]
    compose_template(source, output, info['sha256'], [page, deepcopy(page)])
    prs = Presentation(output)
    for i, slide in enumerate(prs.slides, 1):
        assert slide.element.xpath('.//a:fld[@type="slidenum"]/a:t')[0].text == str(i)


def test_balanced_native_text_blocks_replace_left_placeholder(template):
    source, output, digest, page = template
    page['charts'] = []
    page['text_boxes'] = [
        {'text': 'Problem\nRepeated parsing', 'box': [1, 2, 3.5, 2], 'font_size': 22},
        {'text': 'Method\nReuse parsed data', 'box': [5, 2, 3.5, 2], 'font_size': 22},
    ]
    compose_template(source, output, digest, [page])
    slide = Presentation(output).slides[0]
    blocks = [s for s in slide.shapes if s.has_text_frame and s.text.startswith(('Problem', 'Method'))]
    assert len(blocks) == 2
    assert blocks[0].left == Inches(1) and blocks[1].left == Inches(5)
    assert all(s.text_frame.paragraphs[0].font.size.pt == 22 for s in blocks)


def test_new_native_text_rejects_overflow_and_overlap(template):
    source, output, digest, page = template
    page['charts'] = []
    page['text_boxes'] = [{'text': 'Dense content ' * 50, 'box': [1, 2, 2, 1]}]
    with pytest.raises(ValueError, match='does not fit'):
        compose_template(source, output, digest, [page])
    page['text_boxes'] = [{'text': 'One', 'box': [1, 2, 3, 2]},
                          {'text': 'Two', 'box': [2, 2, 3, 2]}]
    with pytest.raises(ValueError, match='overlaps'):
        compose_template(source, output, digest, [page])


def test_resize_existing_body_box_without_moving_brand(template):
    source, output, digest, page = template
    info = _inspect_page(source, 1)
    body_id = page['remove_shape_ids'][0]
    page['remove_shape_ids'] = []
    page['charts'] = []
    page['edits'] = [{'id': s['id'], 'expected_text': s['text'],
                     'action': 'replace', 'text': 'Readable content'} for s in info['slots']]
    page['shape_boxes'] = [{'shape_id': body_id, 'box': [1, 2, 8, 3]}]
    compose_template(source, output, digest, [page])
    slide = Presentation(output).slides[0]
    body = next(s for s in slide.shapes if str(s.shape_id) == body_id)
    assert body.width == Inches(8) and body.left == Inches(1)
    brand = next(s for s in slide.shapes if s.name == 'Brand mark')
    assert brand.left == Inches(.1)
