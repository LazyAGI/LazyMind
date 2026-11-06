import hashlib
from zipfile import ZipFile

from lxml import etree
from pptx import Presentation
from pptx.util import Inches
import pytest

from lazymind.chat.engine.tools.file_resources.pptx_layout_edit import apply_layout_repairs, layout_snapshot
from lazymind.chat.engine.tools.file_resources.pptx_content_edit import inspect_content


@pytest.fixture
def deck(tmp_path):
    path = tmp_path / 'source.pptx'
    prs = Presentation()
    slide = prs.slides.add_slide(prs.slide_layouts[1])
    slide.shapes.title.text = 'Approved title'
    slide.placeholders[1].text = 'Approved body'
    slide.notes_slide.notes_text_frame.text = 'Keep these notes'
    prs.slides.add_slide(prs.slide_layouts[1]).shapes.title.text = 'Untouched page'
    prs.save(path)
    return path


def patch(deck, output, **overrides):
    target = layout_snapshot(deck)[0]['shapes'][0]
    move = {'page': 1, 'shape_id': target['shape_id'], 'expected_box': target['box'], 'box': [1, 1, 8, 1]}
    move.update(overrides)
    return apply_layout_repairs(deck, output, {'moves': [move]},
                                source_sha256=hashlib.sha256(deck.read_bytes()).hexdigest())


def test_layout_patch_preserves_unrelated_parts_and_objects(deck, tmp_path):
    original = deck.read_bytes()
    out = tmp_path / 'result.pptx'
    stats = patch(deck, out)
    assert stats['updated_layout_pages'] == [1]
    with ZipFile(deck) as before, ZipFile(out) as after:
        assert before.namelist() == after.namelist()
        for name in before.namelist():
            if name != 'ppt/slides/slide1.xml':
                assert before.read(name) == after.read(name), name
        roots = [etree.fromstring(z.read('ppt/slides/slide1.xml')) for z in (before, after)]
        ns = {'p': 'http://schemas.openxmlformats.org/presentationml/2006/main'}
        shapes = [root.findall('p:cSld/p:spTree/p:sp', ns) for root in roots]
        assert etree.tostring(shapes[0][1]) == etree.tostring(shapes[1][1])
    assert deck.read_bytes() == original
    result = Presentation(out)
    assert result.slides[0].shapes.title.text == 'Approved title'
    assert result.slides[0].shapes.title.top == Inches(1)
    assert result.slides[0].notes_slide.notes_text_frame.text == 'Keep these notes'
    assert inspect_content(out, [1])['pages'][0]['layout']['shapes'][0]['element_id'] == '1:shape:2'


@pytest.mark.parametrize('overrides', [
    {'page': 3}, {'page': True}, {'shape_id': 999}, {'shape_id': '2'},
    {'expected_box': [0, 0, 1, 1]}, {'box': [0, 0, 100, 1]},
    {'box': [0, 0, float('nan'), 1]}, {'box': [0, 0, -1, 1]},
    {'box': [0, 0, True, 1]}, {'text': 'Injected rewrite'}, {'expected_box': None},
])
def test_invalid_patch_is_atomic(deck, tmp_path, overrides):
    output = tmp_path / 'rejected.pptx'
    output.write_bytes(b'existing output')
    original = deck.read_bytes()
    with pytest.raises(ValueError):
        patch(deck, output, **overrides)
    assert output.read_bytes() == b'existing output'
    assert deck.read_bytes() == original


def test_stale_revision_and_review_scope_rejected(deck, tmp_path):
    target = layout_snapshot(deck)[0]['shapes'][0]
    plan = {'moves': [{'page': 1, 'shape_id': target['shape_id'],
                       'expected_box': target['box'], 'box': [1, 1, 8, 1]}]}
    output = tmp_path / 'out.pptx'
    with pytest.raises(ValueError, match='Deck changed'):
        apply_layout_repairs(deck, output, plan, source_sha256='old')
    with pytest.raises(ValueError, match='outside the reviewed'):
        apply_layout_repairs(deck, output, plan, source_sha256=hashlib.sha256(deck.read_bytes()).hexdigest(),
                             allowed_pages={2})
    with pytest.raises(ValueError, match='overwrite'):
        apply_layout_repairs(deck, deck, plan, source_sha256=hashlib.sha256(deck.read_bytes()).hexdigest())
    with pytest.raises(ValueError, match='problem shapes'):
        apply_layout_repairs(deck, output, plan, source_sha256=hashlib.sha256(deck.read_bytes()).hexdigest(),
                             allowed_shapes={(1, 3)})
    assert not output.exists()


def test_page_numbers_follow_presentation_order_not_zip_names(deck, tmp_path):
    prs = Presentation(deck)
    slides = prs.slides._sldIdLst
    slides.insert(0, slides[-1])
    prs.save(deck)
    output = tmp_path / 'reordered.pptx'
    stats = patch(deck, output)
    assert stats['changed_parts'] == ['ppt/slides/slide2.xml']
    result = Presentation(output)
    assert result.slides[0].shapes.title.text == 'Untouched page'
    assert result.slides[0].shapes.title.top == Inches(1)
    with ZipFile(deck) as before, ZipFile(output) as after:
        assert before.read('ppt/slides/slide1.xml') == after.read('ppt/slides/slide1.xml')


def test_group_and_table_patches_keep_children_and_cell_content(deck, tmp_path):
    prs = Presentation(deck)
    slide = prs.slides[0]
    group = slide.shapes.add_group_shape()
    group.shapes.add_textbox(Inches(1), Inches(2), Inches(3), Inches(1)).text = 'Grouped content'
    table = slide.shapes.add_table(2, 2, Inches(1), Inches(3), Inches(4), Inches(1))
    table.table.cell(0, 0).text = 'Cell content'
    prs.save(deck)
    layout = layout_snapshot(deck)[0]
    moves = [{'page': 1, 'shape_id': s['shape_id'], 'expected_box': s['box'],
              'box': [s['box'][0] + .25, *s['box'][1:]]} for s in layout['shapes'][-2:]]
    output = tmp_path / 'groups.pptx'
    apply_layout_repairs(deck, output, {'moves': moves},
                         source_sha256=hashlib.sha256(deck.read_bytes()).hexdigest())
    updated = Presentation(output).slides[0]
    assert updated.shapes[-2].shapes[0].text == 'Grouped content'
    assert updated.shapes[-2].shapes[0].left == Inches(1)
    assert updated.shapes[-1].table.cell(0, 0).text == 'Cell content'
    assert updated.shapes[-1].left == Inches(1.25)
