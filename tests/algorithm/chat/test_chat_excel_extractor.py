from __future__ import annotations

import os
import subprocess
import sys
import textwrap

import pandas as pd

from lazymind.chat.engine.chat_excel_extractor import extract_spreadsheet


def test_small_csv_is_parsed_with_pandas_and_inlined(tmp_path) -> None:
    path = tmp_path / 'sales.csv'
    path.write_text('city,amount\n上海,12\n北京,30\n', encoding='utf-8')

    result = extract_spreadsheet(str(path))

    assert result.inline is True
    assert result.total_rows == 2
    assert result.total_cells == 4
    assert result.sheet_shapes == {'sales': (2, 2)}
    assert 'Spreadsheet parsed with pandas' in result.text
    assert 'This is the complete spreadsheet content' in result.text
    assert 'Do not call Python, calculator' in result.text
    assert 'not a faithful model of formatting' in result.text
    assert '上海,12' in result.text


def test_small_xlsx_includes_every_sheet(tmp_path) -> None:
    path = tmp_path / 'book.xlsx'
    with pd.ExcelWriter(path) as writer:
        pd.DataFrame({'name': ['a'], 'value': [1]}).to_excel(
            writer, sheet_name='Summary', index=False,
        )
        pd.DataFrame({'name': ['b'], 'value': [2]}).to_excel(
            writer, sheet_name='Details', index=False,
        )

    result = extract_spreadsheet(str(path))

    assert result.inline is True
    assert result.sheet_shapes == {'Summary': (1, 2), 'Details': (1, 2)}
    assert '### Sheet: Summary' in result.text
    assert '### Sheet: Details' in result.text


def test_large_spreadsheet_returns_preview_and_python_analysis_instruction(tmp_path) -> None:
    path = tmp_path / 'large.csv'
    pd.DataFrame({'id': range(201), 'value': range(201)}).to_csv(path, index=False)

    result = extract_spreadsheet(str(path))

    assert result.inline is False
    assert result.total_rows == 201
    assert 'Large spreadsheet detected' in result.text
    assert 'first 5 rows' in result.text
    assert '`find_user_attachment`' in result.text
    assert 'MUST resolve this exact attachment' in result.text
    assert 'Python execution tool with pandas' in result.text
    assert 'Use openpyxl as well' in result.text
    assert 'inspect all sheet names and dimensions' in result.text
    assert 'identify the header and data ranges' in result.text
    assert 'validate covered record counts' in result.text
    assert 'Do not recalculate formulas' in result.text
    assert 'manually copying values into calculator calls' in result.text
    assert '\n200,200\n' not in result.text


def test_large_source_only_loads_preview_rows(monkeypatch, tmp_path) -> None:
    from lazymind.chat.engine import chat_excel_extractor as extractor

    path = tmp_path / 'large-source.csv'
    path.write_text('name,value\na,1\n', encoding='utf-8')
    monkeypatch.setattr(extractor, 'MAX_FULL_PARSE_BYTES', 1)
    calls = []
    original = extractor._load_sheets

    def tracked(source, *, nrows=None):
        calls.append(nrows)
        return original(source, nrows=nrows)

    monkeypatch.setattr(extractor, '_load_sheets', tracked)

    result = extractor.extract_spreadsheet(str(path))

    assert result.inline is False
    assert calls == [extractor.PREVIEW_ROWS]
    assert result.total_rows == -1
    assert 'full dimensions were intentionally not loaded' in result.text


def test_spreadsheet_extractor_works_when_rag_is_disabled(tmp_path) -> None:
    path = tmp_path / 'standalone.xlsx'
    pd.DataFrame({'answer': [42]}).to_excel(path, index=False)
    code = textwrap.dedent('''
        import sys
        from lazymind.chat.engine.chat_excel_extractor import extract_spreadsheet
        result = extract_spreadsheet(sys.argv[1])
        assert result.inline
        assert '42' in result.text
    ''')

    result = subprocess.run(
        [sys.executable, '-c', code, str(path)],
        env={**os.environ, 'LAZYMIND_RAG_ENABLED': 'false', 'LAZYLLM_INIT_DOC': '0'},
        capture_output=True,
        text=True,
        timeout=30,
    )

    assert result.returncode == 0, result.stdout + result.stderr
