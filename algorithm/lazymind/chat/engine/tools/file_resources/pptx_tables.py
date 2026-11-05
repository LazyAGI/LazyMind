"""Native tables and conservative recovery of legacy text-box tables.

Recovery preserves cell contents and order; it never guesses missing facts or
repairs semantic column mistakes. Capacity estimates are not a render guarantee.
"""
import math
import re
import unicodedata

from lxml import etree
from pptx.dml.color import RGBColor
from pptx.enum.text import MSO_ANCHOR, PP_ALIGN
from pptx.util import Inches, Pt


A = 'http://schemas.openxmlformats.org/drawingml/2006/main'


def _box(value):
    if (not isinstance(value, list) or len(value) != 4
            or any(type(v) not in (int, float) or not math.isfinite(v) for v in value)
            or min(value[:2]) < 0 or min(value[2:]) <= 0):
        raise ValueError('TABLE_BOX_INVALID: box needs nonnegative x/y and positive width/height in inches')
    return value


def _width(text):
    return sum(0 if unicodedata.combining(c) else 1.05 if unicodedata.east_asian_width(c) in 'WF'
               else .35 if c.isspace() else .72 if c.isupper() else .65 for c in text)


def _lines(text, width, size):
    """Account for line breaks and whole Latin tokens without splitting numbers."""
    count = 0
    for line in text.split('\n'):
        used = 0
        count += 1
        for token in re.findall(r'[A-Za-z0-9_.%+−/-]+|.', line):
            advance = _width(token) * size
            if advance > width:
                return math.inf
            if used + advance > width:
                count += 1
                used = 0
            used += advance
    return count


def _rows(spec):
    rows, headers = spec.get('rows'), spec.get('headers')
    if not isinstance(rows, list) or not 1 <= len(rows) <= 40:
        raise ValueError('TABLE_ROWS_INVALID: provide 1–40 complete rows')
    rows = ([headers] if headers is not None else []) + rows
    columns = len(rows[0]) if isinstance(rows[0], list) else 0
    if not 2 <= columns <= 12:
        raise ValueError('TABLE_COLUMNS_INVALID: provide 2–12 columns')
    for index, row in enumerate(rows, 1):
        if not isinstance(row, list) or len(row) != columns:
            raise ValueError(f'TABLE_COLUMNS_MISMATCH: row {index} needs {columns} cells; do not drop or shift values')
        if any(not isinstance(cell, str) or len(cell) > 4000 for cell in row):
            raise ValueError(f'TABLE_CELL_INVALID: row {index} requires plain-text cells of at most 4000 characters')
    return rows


def _layout(spec, rows):
    x, y, w, h = _box(spec.get('box'))
    size = spec.get('font_size', 18)
    minimum = spec.get('min_font_size', size)
    if (any(type(v) not in (int, float) or not math.isfinite(v) for v in (size, minimum))
            or not 12 <= minimum <= size <= 44):
        raise ValueError('TABLE_FONT_INVALID: require 12 <= min_font_size <= font_size <= 44')
    weights = [max(1, max(_width(line) for row in rows for line in row[c].split('\n'))) ** .6
               for c in range(len(rows[0]))]
    automatic = [w * weight / sum(weights) for weight in weights]
    supplied = spec.get('column_widths')
    for widths in (supplied, spec.get('_recovered_widths')):
        if widths is not None and (
            not isinstance(widths, list) or len(widths) != len(weights)
            or any(type(v) not in (int, float) or not math.isfinite(v) or v <= 0 for v in widths)
            or abs(sum(widths) - w) > .01
        ):
            raise ValueError('TABLE_WIDTHS_INVALID: positive column_widths must match the column count and box width')
    candidates = [supplied] if supplied is not None else [automatic, [w / len(weights)] * len(weights)]
    if supplied is None and spec.get('_recovered_widths'):
        candidates.insert(0, spec['_recovered_widths'])
    while True:
        for widths in candidates:
            heights = [max(_lines(cell, widths[c] * 72 - 12, size) for c, cell in enumerate(row))
                       * size * 1.25 + 8 for row in rows]
            if sum(heights) <= h * 72:
                extra = (h * 72 - sum(heights)) / len(rows)
                return widths, [(height + extra) / 72 for height in heights], size
        if size <= minimum:
            break
        size = max(minimum, size - 1)
    raise ValueError('TABLE_LAYOUT_OVERFLOW: cells cannot fit this box at the allowed font size; '
                     'increase its bounds, revise column_widths, or split the table. No cells were discarded.')


def add_tables(prs, slide, specs):
    if not isinstance(specs, list) or len(specs) > 8:
        raise ValueError('tables must contain at most 8 native tables per slide')
    reports = []
    for index, spec in enumerate(specs, 1):
        if not isinstance(spec, dict):
            raise ValueError('Each table requires box and rows')
        rows = _rows(spec)
        widths, heights, size = _layout(spec, rows)
        x, y, w, h = spec['box']
        if x + w > prs.slide_width / 914400 + .00001 or y + h > prs.slide_height / 914400 + .00001:
            raise ValueError('TABLE_BOX_INVALID: table is outside the slide; choose a free content region')
        shape = slide.shapes.add_table(len(rows), len(rows[0]), Inches(x), Inches(y), Inches(w), Inches(h))
        shape.name = f'Native table {index}'
        table = shape.table
        table.first_row = False
        table.horz_banding = False
        for column, width in zip(table.columns, widths):
            column.width = Inches(width)
        for row, height in zip(table.rows, heights):
            row.height = Inches(height)
        for r, row in enumerate(rows):
            for c, value in enumerate(row):
                cell = table.cell(r, c)
                cell.text = value
                cell.margin_left = cell.margin_right = Pt(6)
                cell.margin_top = cell.margin_bottom = Pt(3)
                cell.vertical_anchor = MSO_ANCHOR.MIDDLE
                cell.text_frame.word_wrap = True
                header = spec.get('headers') is not None and r == 0
                cell.fill.solid()
                cell.fill.fore_color.rgb = RGBColor.from_string('F1F4F8' if header else 'FFFFFF')
                for paragraph in cell.text_frame.paragraphs:
                    paragraph.alignment = PP_ALIGN.LEFT
                    paragraph.font.name = spec.get('font_family', 'Microsoft YaHei')
                    paragraph.font.size = Pt(size)
                    paragraph.font.bold = header
                    paragraph.font.color.rgb = RGBColor.from_string('1F2937')
                    paragraph.space_before = paragraph.space_after = Pt(0)
                    paragraph.line_spacing = 1.1
                    props = paragraph._p.get_or_add_pPr()
                    props.insert_element_before(etree.Element(f'{{{A}}}buNone'), 'a:tabLst', 'a:defRPr', 'a:extLst')
                props = cell._tc.get_or_add_tcPr()
                for position, edge in enumerate(('lnL', 'lnR', 'lnT', 'lnB')):
                    line = etree.Element(f'{{{A}}}{edge}', w='6350')
                    props.insert(position, line)
                    fill = etree.SubElement(line, f'{{{A}}}solidFill')
                    etree.SubElement(fill, f'{{{A}}}srgbClr', val='D5DCE5')
        reports.append({'rows': len(rows), 'columns': len(rows[0]), 'font_size': size})
    return reports


def _pipe_rows(text):
    if any(marker in text for marker in ('`', '\\|', '||', '://')):
        return None
    rows = []
    for line in text.splitlines():
        if not line.strip():
            continue
        parts = re.split(r'[|｜]', line.strip().strip('|｜'))
        if len(parts) < 2:
            return None
        if all(re.fullmatch(r'\s*:?-{3,}:?\s*', p) for p in parts):
            continue  # Markdown alignment row, not business data.
        rows.append([p.strip() for p in parts])
    return rows or None


def _recovered(boxes, rows, header):
    x = min(b['box'][0] for b in boxes)
    y = min(b['box'][1] for b in boxes)
    right = max(b['box'][0] + b['box'][2] for b in boxes)
    bottom = max(b['box'][1] + b['box'][3] for b in boxes)
    sizes = [b.get('font_size', 22) for b in boxes]
    if any(type(v) not in (int, float) or not math.isfinite(v) for v in sizes):
        raise ValueError('TABLE_FONT_INVALID: recovered cells require finite font sizes')
    size = min(sizes)
    spec = {'box': [x, y, right - x, bottom - y], 'rows': rows[1:] if header else rows,
            'font_size': max(12, min(44, size)), 'min_font_size': max(12, min(16, size)),
            'font_family': boxes[0].get('font_family', 'Microsoft YaHei')}
    if header:
        spec['headers'] = rows[0]
    return spec


def recover_text_tables(boxes):
    """Convert only aligned pipe rows or complete grids with an explicit bold header.

    Unrelated prose, ragged grids and incomplete tables are never filled with
    guessed content. Invalid rectangular pipe input gets a precise row error.
    """
    if not isinstance(boxes, list) or len(boxes) > 200:
        raise ValueError('text_boxes must be a list of at most 200 blocks before table recovery')
    if any(not isinstance(b, dict) or not isinstance(b.get('text'), str) for b in boxes):
        raise ValueError('Each text box requires plain text')
    remaining = list(range(len(boxes)))
    eligible = []
    for i, b in enumerate(boxes):
        try:
            _box(b.get('box'))
        except ValueError:
            continue  # Leave ordinary text geometry normalization to its existing renderer.
        eligible.append(i)
    tables = []
    # Preserve vertical ordering and accept both one multiline block and one
    # block per row. Column count is validated later, never padded/truncated.
    for i in sorted(eligible, key=lambda i: boxes[i]['box'][1]):
        if i not in remaining:
            continue
        rows = _pipe_rows(boxes[i]['text'])
        if rows is None:
            continue
        group = [i]
        bottom = boxes[i]['box'][1] + boxes[i]['box'][3]
        for j in sorted(eligible, key=lambda j: boxes[j]['box'][1]):
            if j not in remaining or j == i or boxes[j]['box'][1] < bottom - .01:
                continue
            b, first = boxes[j]['box'], boxes[i]['box']
            if abs(b[0] - first[0]) > .03 or abs(b[2] - first[2]) > .03:
                continue
            following = _pipe_rows(boxes[j]['text'])
            if b[1] - bottom > .25 or following is None:
                break
            rows.extend(following)
            group.append(j)
            bottom = b[1] + b[3]
        if len(rows) >= 2:
            tables.append(_recovered([boxes[j] for j in group], rows, bool(boxes[i].get('bold'))))
            remaining = [j for j in remaining if j not in group]
    # A header must have at least two bold cells and two complete body rows.
    # Matching x/width prevents treating arbitrary multi-column prose as data.
    for i in sorted(eligible, key=lambda i: boxes[i]['box'][1]):
        if i not in remaining or not boxes[i].get('bold'):
            continue
        header = sorted([j for j in eligible if j in remaining and boxes[j].get('bold')
                         and abs(boxes[j]['box'][1] - boxes[i]['box'][1]) < .03],
                        key=lambda j: boxes[j]['box'][0])
        if len(header) < 2 or any(boxes[a]['box'][0] + boxes[a]['box'][2] > boxes[b]['box'][0] + .01
                                  for a, b in zip(header, header[1:])):
            continue
        group, rows = list(header), [[boxes[j]['text'] for j in header]]
        bottom = max(boxes[j]['box'][1] + boxes[j]['box'][3] for j in header)
        while True:
            candidates = [j for j in eligible if j in remaining and j not in group
                          and boxes[j]['box'][1] >= bottom - .01
                          and any(abs(boxes[j]['box'][0] - boxes[h]['box'][0]) < .03
                                  and abs(boxes[j]['box'][2] - boxes[h]['box'][2]) < .03 for h in header)]
            if not candidates:
                break
            y = min(boxes[j]['box'][1] for j in candidates)
            row = sorted([j for j in candidates if abs(boxes[j]['box'][1] - y) < .03],
                         key=lambda j: boxes[j]['box'][0])
            if y - bottom > .25 or len(row) != len(header):
                break
            if any(abs(boxes[j]['box'][0] - boxes[h]['box'][0]) >= .03 for j, h in zip(row, header)):
                break
            group.extend(row)
            rows.append([boxes[j]['text'] for j in row])
            bottom = max(boxes[j]['box'][1] + boxes[j]['box'][3] for j in row)
        if len(rows) >= 3:
            spec = _recovered([boxes[j] for j in group], rows, True)
            widths = [boxes[j]['box'][2] for j in header]
            gap = (spec['box'][2] - sum(widths)) / len(widths)
            spec['_recovered_widths'] = [w + gap for w in widths]
            tables.append(spec)
            remaining = [j for j in remaining if j not in group]
    return [boxes[j] for j in remaining], tables
