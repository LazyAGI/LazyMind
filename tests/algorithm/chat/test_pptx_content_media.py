import base64
from io import BytesIO
from zipfile import ZipFile, ZIP_DEFLATED

import pytest
from lxml import etree
from pptx import Presentation
from pptx.chart.data import CategoryChartData
from pptx.enum.chart import XL_CHART_TYPE
from pptx.util import Inches

from lazymind.chat.engine.tools.file_resources import pptx_reader as reader

# Original one-pixel PNG fixture; no external file or service needed.
PNG = base64.b64decode('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+a9WQAAAAASUVORK5CYII=')


def media_deck(path, pages=2):
    deck = Presentation()
    for index in range(pages):
        slide = deck.slides.add_slide(deck.slide_layouts[6])
        slide.shapes.add_textbox(0, 0, Inches(3), Inches(1)).text = f'Page {index + 1}'
        slide.shapes.add_picture(BytesIO(PNG), 0, 0, Inches(1), Inches(1))
    deck.save(path)


def test_images_described_once_and_referenced_across_25_pages(tmp_path, monkeypatch):
    source = tmp_path / 'images.pptx'
    media_deck(source, 25)
    calls = []

    def describe(raw, suffix):
        calls.append((raw, suffix))
        return '截图：页面显示 47 个聊天。'
    monkeypatch.setattr(reader, '_describe_image', describe)
    from lazymind.chat.engine.subagent import tools
    monkeypatch.setattr(tools, '_resolve_attachment', lambda filename, turn: (str(source), None))
    result = tools.pptx_analyzer(source.name, all_pages=True)
    assert result['slide_count'] == 25
    assert calls == [(PNG, '.png')]
    assert '[Image 1]\n截图：页面显示 47 个聊天。' in result['pages'][0]['text']
    assert all('same image as page 1' in page['text'] for page in result['pages'][1:])
    assert all(set(page) == {'page', 'text'} for page in result['pages'])
    single = reader.read_pptx_content(source, page=25)
    assert '截图' in single['text'] and 'same image' not in single['text']


def test_image_failure_preserves_text_and_does_not_leak_error(tmp_path, monkeypatch):
    source = tmp_path / 'images.pptx'
    media_deck(source)

    def unavailable(*args):
        raise RuntimeError('private provider details must never be returned')
    monkeypatch.setattr(reader, '_describe_image', unavailable)
    result = reader.read_pptx_content(source, all_pages=True)
    assert 'Page 1' in result['pages'][0]['text']
    assert 'RuntimeError' in result['pages'][0]['text']
    assert 'image content not read' in result['pages'][0]['text']
    assert 'private provider' not in str(result)
    monkeypatch.setattr(reader, '_describe_image', lambda *args: pytest.fail('vision disabled'))
    assert 'visual reading disabled' in reader.read_pptx_content(source, describe_images=False)['text']


def test_chart_values_and_multiline_table_cells_are_kept(tmp_path):
    source = tmp_path / 'chart.pptx'
    deck = Presentation()
    slide = deck.slides.add_slide(deck.slide_layouts[6])
    values = CategoryChartData()
    values.categories = ['一月', '二月']
    values.add_series('收入', (10, 20))
    slide.shapes.add_chart(XL_CHART_TYPE.COLUMN_CLUSTERED, 0, 0, Inches(3), Inches(2), values)
    table = slide.shapes.add_table(1, 2, 0, 0, Inches(3), Inches(1)).table
    table.cell(0, 0).text = '甲\n乙'
    table.cell(0, 1).text = '丙\t丁'
    deck.save(source)
    result = reader.read_pptx_content(source)['text']
    assert '收入' in result and '0=一月' in result and '1=二月' in result
    assert '0=10' in result and '1=20' in result
    assert '甲\\n乙\t丙\\t丁' in result


def patch_package(source, patch):
    with ZipFile(source) as archive:
        parts = {name: archive.read(name) for name in archive.namelist()}
    patch(parts)
    with ZipFile(source, 'w', ZIP_DEFLATED) as archive:
        for name, raw in parts.items():
            archive.writestr(name, raw)


def test_external_images_are_not_fetched(tmp_path, monkeypatch):
    source = tmp_path / 'external.pptx'
    media_deck(source, 1)

    def patch(parts):
        name = 'ppt/slides/_rels/slide1.xml.rels'
        root = etree.fromstring(parts[name])
        for rel in root:
            if rel.get('Type', '').endswith('/image'):
                rel.set('TargetMode', 'External')
                rel.set('Target', 'https://example.invalid/image.png')
        parts[name] = etree.tostring(root)
    patch_package(source, patch)
    monkeypatch.setattr(reader, '_describe_image', lambda *args: pytest.fail('must not fetch external image'))
    assert 'external or missing relationship' in reader.read_pptx_content(source)['text']


def test_smartart_and_embedded_objects_are_not_silently_lost(tmp_path):
    source = tmp_path / 'diagram.pptx'
    deck = Presentation()
    deck.slides.add_slide(deck.slide_layouts[6])
    deck.save(source)

    def patch(parts):
        p, a, r = reader.NS['p'], reader.NS['a'], reader.NS['r']
        d = 'http://schemas.openxmlformats.org/drawingml/2006/diagram'
        root = etree.fromstring(parts['ppt/slides/slide1.xml'])
        tree = root.find('.//p:spTree', reader.NS)
        frame = etree.SubElement(tree, '{' + p + '}graphicFrame')
        etree.SubElement(frame, '{' + d + '}relIds', {'{' + r + '}dm': 'rIdDiagram'})
        etree.SubElement(frame, '{' + p + '}oleObj')
        parts['ppt/slides/slide1.xml'] = etree.tostring(root)
        name = 'ppt/slides/_rels/slide1.xml.rels'
        rels = etree.fromstring(parts[name])
        etree.SubElement(rels, '{http://schemas.openxmlformats.org/package/2006/relationships}Relationship',
                         Id='rIdDiagram', Type=r + '/diagramData', Target='../diagrams/data1.xml')
        parts[name] = etree.tostring(rels)
        parts['ppt/diagrams/data1.xml'] = (
            f'<d:dataModel xmlns:d="{d}" xmlns:a="{a}"><a:p><a:r><a:t>观察→行动</a:t>'
            '</a:r></a:p></d:dataModel>').encode()
    patch_package(source, patch)
    result = reader.read_pptx_content(source)['text']
    assert '观察→行动' in result and 'visual relationships not interpreted' in result
    assert 'oleObj' in result and 'not interpreted or executed' in result


def test_25_page_mixed_deck_preserves_all_native_content(tmp_path, monkeypatch):
    source = tmp_path / 'mixed.pptx'
    deck = Presentation()
    for index in range(25):
        slide = deck.slides.add_slide(deck.slide_layouts[6])
        slide.shapes.add_picture(BytesIO(PNG), 0, 0, Inches(1), Inches(1))
        slide.notes_slide.notes_text_frame.text = f'备注 {index + 1}'
        slot = index % 5
        if slot == 0:
            table = slide.shapes.add_table(2, 2, 0, 0, Inches(3), Inches(1)).table
            table.cell(0, 0).text = '阶段'
            table.cell(0, 1).text = '预算'
            table.cell(1, 0).text = '构建\n验证'
            table.cell(1, 1).text = '25'
        elif slot in (1, 2):
            values = CategoryChartData()
            values.categories = ['Q1', 'Q2']
            values.add_series('计划', (10, 25))
            values.add_series('实际', (12, 23))
            chart_type = XL_CHART_TYPE.COLUMN_CLUSTERED if slot == 1 else XL_CHART_TYPE.LINE
            slide.shapes.add_chart(chart_type, 0, 0, Inches(3), Inches(2), values)
        elif slot == 3:
            group = slide.shapes.add_group_shape()
            group.shapes.add_textbox(0, 0, Inches(3), Inches(1)).text = '组合：发现→验证'
        else:
            slide.shapes.add_textbox(0, 0, Inches(3), Inches(1)).text = '中文😀\n第二段'
    deck.save(source)
    original = source.read_bytes()
    calls = []
    monkeypatch.setattr(reader, '_describe_image', lambda raw, suffix: (calls.append(raw), '测试图片')[1])
    result = reader.read_pptx_content(source, all_pages=True)
    assert len(result['pages']) == 25 and result['next_page'] is None
    assert calls == [PNG]
    for index, page in enumerate(result['pages']):
        text = page['text']
        assert page['page'] == index + 1
        assert f'[Speaker notes]\n备注 {index + 1}' in text
        if index % 5 == 0:
            assert '阶段\t预算\n构建\\n验证\t25' in text
        elif index % 5 in (1, 2):
            assert '计划' in text and '实际' in text and '1=25' in text and '1=23' in text
        elif index % 5 == 3:
            assert text.count('组合：发现→验证') == 1
        else:
            assert '中文😀\n第二段' in text
    assert source.read_bytes() == original
