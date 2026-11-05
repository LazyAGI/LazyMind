"""User-visible native edits and package-preservation regression coverage."""
import hashlib
from zipfile import ZipFile

from pptx import Presentation
from pptx.util import Inches
import pytest

from lazymind.chat.engine.tools.file_resources.pptx_content_edit import inspect_content
from lazymind.chat.engine.tools.file_resources.pptx_structure_edit import edit_structure


@pytest.fixture
def deck(tmp_path):
    prs = Presentation()
    slide = prs.slides.add_slide(prs.slide_layouts[6])
    table = slide.shapes.add_table(2, 2, Inches(1), Inches(1), Inches(6), Inches(2)).table
    for i, values in enumerate([['Name', 'Value'], ['Old', '10']]):
        for j, value in enumerate(values):
            table.cell(i, j).text = value
    table.cell(1, 0).text_frame.paragraphs[0].runs[0].font.bold = True
    slide.shapes.add_textbox(Inches(1), Inches(4), Inches(3), Inches(2)).text = 'One\nTwo'
    slide.notes_slide.notes_text_frame.text = 'Preserve notes'
    prs.slides.add_slide(prs.slide_layouts[6]).shapes.add_textbox(0, 0, Inches(5), Inches(1)).text = 'Untouched'
    source = tmp_path / 'source.pptx'
    prs.save(source)
    return source


def apply(deck, tmp_path, operations):
    output = tmp_path / 'edited.pptx'
    result = edit_structure(deck, output, hashlib.sha256(deck.read_bytes()).hexdigest(), operations)
    return Presentation(output), result, output


def test_table_and_list_batch_preserves_unrelated_parts(deck, tmp_path):
    table_id, list_id = [s.shape_id for s in Presentation(deck).slides[0].shapes]
    ops = [
        {'op': 'table_set_cell', 'page': 1, 'shape_id': table_id, 'row': 2, 'column': 2, 'text': '20'},
        {'op': 'table_insert_row', 'page': 1, 'shape_id': table_id, 'index': 3, 'values': ['New', '30']},
        {'op': 'table_insert_column', 'page': 1, 'shape_id': table_id, 'index': 2, 'values': ['Unit', 'kg', 'g']},
        {'op': 'table_delete_row', 'page': 1, 'shape_id': table_id, 'index': 1},
        {'op': 'table_delete_column', 'page': 1, 'shape_id': table_id, 'index': 2},
        {'op': 'set_list', 'page': 1, 'shape_id': list_id, 'values': ['Alpha', 'Beta'], 'ordered': True},
        {'op': 'list_insert_item', 'page': 1, 'shape_id': list_id, 'index': 3, 'text': 'Gamma'},
        {'op': 'list_set_item', 'page': 1, 'shape_id': list_id, 'index': 2, 'text': 'Updated'},
        {'op': 'list_delete_item', 'page': 1, 'shape_id': list_id, 'index': 1},
    ]
    prs, result, output = apply(deck, tmp_path, ops)
    table = prs.slides[0].shapes[0].table
    assert [[c.text for c in r.cells] for r in table.rows] == [['Old', '20'], ['New', '30']]
    assert table.cell(0, 0).text_frame.paragraphs[0].runs[0].font.bold is True
    assert prs.slides[0].shapes[1].text == 'Updated\nGamma'
    assert sum(c.width for c in table.columns) == Inches(6)
    assert sum(r.height for r in table.rows) == Inches(2)
    assert result['regenerated_pages'] == []
    with ZipFile(deck) as before, ZipFile(output) as after:
        assert before.namelist() == after.namelist()
        for name in before.namelist():
            if name != 'ppt/slides/slide1.xml':
                assert before.read(name) == after.read(name), name
    snapshot = inspect_content(output, [1])['pages'][0]['layout']['shapes']
    assert snapshot[0]['table']['rows'] == [['Old', '20'], ['New', '30']]
    assert snapshot[1]['paragraphs'] == ['Updated', 'Gamma']


def test_add_delete_copy_and_batch_geometry(deck, tmp_path):
    target = Presentation(deck).slides[0].shapes[1].shape_id
    prs, result, output = apply(deck, tmp_path, [
        {'op': 'delete_shape', 'page': 1, 'shape_id': target},
        {'op': 'add_list', 'page': 1, 'box': [1, 4, 2, 2], 'values': ['A', 'B']},
        {'op': 'add_text', 'page': 1, 'box': [4, 4, 2, 2], 'text': 'C'},
        {'op': 'add_table', 'page': 2, 'box': [1, 2, 5, 2], 'rows': [['A', 'B'], ['1', '2']]},
    ])
    ids = [s['shape_id'] for s in result['created_shapes'] if s['page'] == 1]
    assert len(set(s.shape_id for s in prs.slides[0].shapes)) == 3
    prs, result, _ = apply(output, tmp_path / 'next', [
        {'op': 'resize', 'page': 1, 'shape_ids': ids, 'dw': 0.5},
        {'op': 'move', 'page': 1, 'shape_ids': ids, 'dy': 0.25},
        {'op': 'align', 'page': 1, 'shape_ids': ids, 'edge': 'top'},
        {'op': 'duplicate_shape', 'page': 1, 'shape_id': ids[0], 'box': [7, 4, 2, 2]},
        {'op': 'send_to_back', 'page': 1, 'shape_id': ids[1]},
    ])
    assert prs.slides[0].shapes[0].shape_id == ids[1]
    assert prs.slides[0].shapes[0].width == Inches(2.5)
    assert prs.slides[0].shapes[0].top == Inches(4.25)
    assert len(prs.slides[0].shapes) == 4
    assert prs.slides[1].shapes[1].has_table


@pytest.mark.parametrize('failure', ['stale', 'bad_index', 'bad_count', 'unknown', 'offslide', 'merged'])
def test_bad_batch_retains_original_and_existing_output(deck, tmp_path, failure):
    if failure == 'merged':
        prs = Presentation(deck)
        table = prs.slides[0].shapes[0].table
        table.cell(0, 0).merge(table.cell(0, 1))
        prs.save(deck)
    original = deck.read_bytes()
    output = tmp_path / 'edited.pptx'
    output.write_bytes(b'previous export')
    operation = {'op': 'table_insert_row', 'page': 1, 'shape_id': 2, 'index': 2, 'values': ['A', 'B']}
    if failure == 'bad_index': operation['index'] = 100
    if failure == 'bad_count': operation['values'] = ['A']
    if failure == 'unknown': operation['shape_id'] = 999
    if failure == 'offslide': operation = {'op': 'move', 'page': 1, 'shape_id': 2, 'dx': 100}
    with pytest.raises(ValueError):
        edit_structure(deck, output, 'wrong' if failure == 'stale' else hashlib.sha256(original).hexdigest(), [
            {'op': 'add_text', 'page': 2, 'box': [1, 4, 3, 1], 'text': 'Must roll back'}, operation])
    assert deck.read_bytes() == original
    assert output.read_bytes() == b'previous export'
