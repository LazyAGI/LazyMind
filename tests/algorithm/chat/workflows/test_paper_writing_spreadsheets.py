"""Real XLSX cells remain source material, never an inferred or executed experiment."""
import hashlib
import json

import pytest
from openpyxl import Workbook

from lazymind.document_tools.paper.writing import attachments


@pytest.fixture
def workbook(tmp_path):
    path = tmp_path / 'observations.xlsx'
    book = Workbook()
    sheet = book.active
    sheet.title = 'Raw observations'
    sheet.append(['batch', 'score', 'derived'])
    sheet.append(['run-alpha', None, '=SUM(B2:B3)'])
    sheet.append(['run-beta', 8.5, None])
    other = book.create_sheet('Historical notes')
    other.append(['status', 'simulated'])
    other.sheet_state = 'hidden'
    book.save(path)
    return path


def test_xlsx_is_selected_in_same_upload_batch_without_historical_merging():
    assert attachments.select_files({'history_files_per_turn': {
        '1': ['old.md'], '2': ['notes.md', 'observations.xlsx']}}) == ['notes.md', 'observations.xlsx']


def test_source_cells_preserve_sheet_row_column_null_and_formula_cache(workbook):
    original = workbook.read_bytes()
    text = attachments.read_workbook(str(workbook))
    assert hashlib.sha256(original).hexdigest() in text
    assert 'not an independently verified experiment or analysis' in text
    data = json.loads(text.rsplit('\n\n', 1)[1])
    raw, hidden = data['sheets']
    assert raw['sheet'] == 'Raw observations' and raw['range'] == 'A1:C3'
    expected = dict(A='run-alpha', C=dict(
        formula='=SUM(B2:B3)', cached_value=None, cache_status='missing'))
    assert raw['rows'][1] == dict(row=2, cells=expected)
    assert raw['rows'][1]['cells'].get('B') is None
    assert 'every omitted cell is blank/null, never zero' in text
    assert raw['read_cells'] == 8 and raw['nonempty_cells'] == 7
    assert hidden['visibility'] == 'hidden' and hidden['rows'][0]['cells']['B'] == 'simulated'
    assert workbook.read_bytes() == original


def test_spreadsheet_only_import_uses_actual_values_without_classification_model(workbook, monkeypatch):
    monkeypatch.setattr(attachments, 'AutoModel', lambda **kwargs:
                        pytest.fail('Spreadsheet content transcribed by model'))
    value = attachments.import_files([str(workbook)], 'Use supplied observations for a paper')
    assert 'run-alpha' in value['writing_materials'] and 'run-beta' in value['writing_materials']
    assert 'analysis' not in value and 'experiment' not in value
    assert 'Formulas were not recalculated' in value['writing_materials']


def test_large_workbook_is_rejected_instead_of_silently_using_a_preview(tmp_path):
    path = tmp_path / 'too-large.xlsx'
    book = Workbook()
    for _ in range(201):
        book.active.append(list(range(100)))
    book.save(path)
    with pytest.raises(attachments.MissingMaterials, match='20000'):
        attachments.read_workbook(str(path))


def test_wrong_a1_dimension_does_not_truncate_real_rows(workbook, tmp_path):
    import re
    from zipfile import ZipFile
    wrong = tmp_path / 'wrong-dimensions.xlsx'
    with ZipFile(workbook) as source, ZipFile(wrong, 'w') as target:
        for item in source.infolist():
            raw = source.read(item.filename)
            if item.filename.startswith('xl/worksheets/') and item.filename.endswith('.xml'):
                raw = re.sub(rb'<dimension ref="[^"]+"', b'<dimension ref="A1"', raw)
            target.writestr(item, raw)
    text = attachments.read_workbook(str(wrong))
    raw = json.loads(text.rsplit('\n\n', 1)[1])['sheets'][0]
    assert raw['range'] == 'A1:C3' and len(raw['rows']) == 3
    assert raw['rows'][2]['cells']['B'] == 8.5


def test_sparse_rows_keep_original_range_and_blank_semantics(tmp_path):
    path = tmp_path / 'sparse.xlsx'
    book = Workbook()
    book.active['A1'] = 'observed'
    book.active['C4'] = '=A1'
    book.save(path)
    text = attachments.read_workbook(str(path))
    sheet = json.loads(text.rsplit('\n\n', 1)[1])['sheets'][0]
    assert sheet['range'] == 'A1:C4' and sheet['read_rows'] == 4
    assert [item['row'] for item in sheet['rows']] == [1, 4]
    assert sheet['nonempty_cells'] == 2
    assert sheet['rows'][1]['cells']['C'] == dict(
        formula='=A1', cached_value=None, cache_status='missing')
    assert 'all-blank rows are omitted' in text
