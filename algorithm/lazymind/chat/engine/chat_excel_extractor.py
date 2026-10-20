from __future__ import annotations

from dataclasses import dataclass
from pathlib import Path
from typing import Dict


MAX_INLINE_ROWS = 200
MAX_INLINE_CELLS = 4_000
MAX_INLINE_CHARS = 30_000
MAX_FULL_PARSE_BYTES = 512 * 1024
PREVIEW_ROWS = 5

_INLINE_ANALYSIS_INSTRUCTION = (
    'This is the complete spreadsheet content. Answer directly from this context. '
    'Do not call Python, calculator, or another analysis tool merely to re-read, re-sum, '
    'or verify values that are already present. Use a tool only when the user explicitly '
    'requests a transformation or analysis that cannot be answered reliably from this complete content. '
    'The inline representation contains cell values, not a faithful model of formatting, merged ranges, '
    'hidden rows or columns, or formula freshness; do not make claims about those features from this text.'
)
_LARGE_ANALYSIS_INSTRUCTION = (
    'The preview is not the complete spreadsheet. Before answering any question that depends on '
    'whole-file calculations, filtering, grouping, statistics, joins, comparisons, or conclusions, '
    'you MUST resolve this exact attachment with `find_user_attachment` and use an available Python '
    'execution tool with pandas to analyze the original file. Use openpyxl as well when merged cells, '
    'hidden rows or columns, formulas, cached values, or workbook structure affect the answer. '
    'The code analysis MUST: (1) inspect all sheet names and dimensions before choosing data; '
    '(2) identify the header and data ranges and state the selected sheet/range; (3) define units, '
    'hidden-row policy, and exclusions such as title, subtotal, and total rows; (4) analyze the complete '
    'selected ranges rather than only head rows; and (5) validate covered record counts, missing or '
    'unmatched values, and suspicious formula caches before reporting results. Do not recalculate formulas '
    'or modify the original workbook unless the user asks; if saved formula values are missing or stale, '
    'report that limitation instead of treating them as zero. Never calculate whole-file results from the '
    'preview or by manually copying values into calculator calls. Return the analysis scope and material '
    'anomalies with the result. If this runtime does not expose Python execution, say so and limit the '
    'answer to facts visible in the preview.'
)


@dataclass(frozen=True)
class SpreadsheetExtraction:
    """Prompt-ready spreadsheet content and the routing decision behind it."""

    text: str
    inline: bool
    total_rows: int
    total_cells: int
    sheet_shapes: Dict[str, tuple[int, int]]


def _read_csv(path: Path, *, nrows: int | None = None):
    import pandas as pd

    separator = '\t' if path.suffix.lower() == '.tsv' else ','
    try:
        frame = pd.read_csv(path, sep=separator, nrows=nrows)
    except UnicodeDecodeError:
        frame = pd.read_csv(path, sep=separator, encoding='gb18030', nrows=nrows)
    return {path.stem or 'Sheet1': frame}


def _read_workbook(path: Path, *, nrows: int | None = None):
    import pandas as pd

    return pd.read_excel(path, sheet_name=None, nrows=nrows)


def _load_sheets(path: Path, *, nrows: int | None = None):
    if path.suffix.lower() in {'.csv', '.tsv'}:
        return _read_csv(path, nrows=nrows)
    return _read_workbook(path, nrows=nrows)


def _frame_csv(frame) -> str:
    # pandas owns type normalization; CSV keeps the result dependency-free
    # (DataFrame.to_markdown would add a runtime dependency on tabulate).
    return frame.fillna('').to_csv(index=False, lineterminator='\n').rstrip()


def _sheet_block(name: str, frame, *, preview: bool) -> str:
    visible = frame.head(PREVIEW_ROWS) if preview else frame
    body = _frame_csv(visible)
    suffix = f' (first {min(PREVIEW_ROWS, len(frame))} rows)' if preview else ''
    return f'### Sheet: {name}{suffix}\n```csv\n{body}\n```'


def extract_spreadsheet(file_path: str) -> SpreadsheetExtraction:
    """Parse CSV/TSV/XLS/XLSX with pandas and choose inline or Python analysis."""
    path = Path(file_path)
    source_bytes = path.stat().st_size
    if source_bytes > MAX_FULL_PARSE_BYTES:
        sheets = _load_sheets(path, nrows=PREVIEW_ROWS)
        shapes = {
            str(name): (int(frame.shape[0]), int(frame.shape[1]))
            for name, frame in sheets.items()
        }
        previews = '\n\n'.join(
            _sheet_block(str(name), frame, preview=False) for name, frame in sheets.items()
        )
        text = (
            f'Large spreadsheet detected from source size ({source_bytes} bytes); full dimensions '
            'were intentionally not loaded into chat context.\n\n'
            f'{previews}\n\n'
            f'{_LARGE_ANALYSIS_INSTRUCTION}'
        )
        return SpreadsheetExtraction(
            text=text,
            inline=False,
            total_rows=-1,
            total_cells=-1,
            sheet_shapes=shapes,
        )
    sheets = _load_sheets(path)
    shapes = {str(name): (int(frame.shape[0]), int(frame.shape[1])) for name, frame in sheets.items()}
    total_rows = sum(rows for rows, _columns in shapes.values())
    total_cells = sum(rows * columns for rows, columns in shapes.values())
    full_blocks = [_sheet_block(str(name), frame, preview=False) for name, frame in sheets.items()]
    full_text = '\n\n'.join(full_blocks)
    inline = (
        total_rows <= MAX_INLINE_ROWS
        and total_cells <= MAX_INLINE_CELLS
        and len(full_text) <= MAX_INLINE_CHARS
    )
    if inline:
        text = (
            f'Spreadsheet parsed with pandas: {len(sheets)} sheet(s), '
            f'{total_rows} data row(s), {total_cells} cell(s).\n'
            f'{_INLINE_ANALYSIS_INSTRUCTION}\n\n{full_text}'
        )
    else:
        shape_lines = '\n'.join(
            f'- {name}: {rows} rows x {columns} columns'
            for name, (rows, columns) in shapes.items()
        )
        previews = '\n\n'.join(
            _sheet_block(str(name), frame, preview=True) for name, frame in sheets.items()
        )
        text = (
            f'Large spreadsheet detected: {len(sheets)} sheet(s), {total_rows} data row(s), '
            f'{total_cells} cell(s).\n{shape_lines}\n\n{previews}\n\n'
            f'{_LARGE_ANALYSIS_INSTRUCTION}'
        )
    return SpreadsheetExtraction(
        text=text,
        inline=inline,
        total_rows=total_rows,
        total_cells=total_cells,
        sheet_shapes=shapes,
    )
