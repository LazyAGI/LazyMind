"""Deterministic structural patches to native slides, with atomic publication."""
from copy import deepcopy
import hashlib
import math
import os
from pathlib import Path
import tempfile
from zipfile import ZipFile

from lxml import etree
from pptx import Presentation

from .pptx_template_fill import NS, _load


def _tag(name):
    prefix, local = name.split(':')
    return '{%s}%s' % (NS[prefix], local)


def _text(value):
    if not isinstance(value, str) or len(value) > 4000 or any(ord(c) < 32 and c not in '\n\t' for c in value):
        raise ValueError('Text must be a string of at most 4000 characters without control characters')
    return value


def _index(value, length, insert=False):
    if type(value) is not int or not 1 <= value <= length + int(insert):
        raise ValueError('Index must be a valid 1-based position; insertion allows length + 1')
    return value - 1


def _values(values, count=None):
    if not isinstance(values, list) or not values or len(values) > 100:
        raise ValueError('values must contain 1–100 text items')
    if count is not None and len(values) != count:
        raise ValueError('values must match the table row/column count exactly')
    return [_text(value) for value in values]


def _box(values, size):
    if (not isinstance(values, (list, tuple)) or len(values) != 4
            or any(type(v) not in (int, float) or not math.isfinite(v) for v in values)):
        raise ValueError('box requires [x,y,width,height] in inches')
    x, y, w, h = values
    if min(x, y) < 0 or min(w, h) <= 0 or x + w > size[0] + 1e-6 or y + h > size[1] + 1e-6:
        raise ValueError('Shape box must fit inside the slide')
    return [round(v * 914400) for v in values]


def _shape(tree, identity):
    if type(identity) is not int:
        raise ValueError('Use an inspected integer shape_id')
    found = [node for node in tree if any(p.get('id') == str(identity) for p in node.findall('./*/p:cNvPr', NS))]
    if len(found) != 1:
        raise ValueError('Unknown or ambiguous shape_id; inspect the current page')
    return found[0]


def _transform(shape):
    if shape.tag == _tag('p:graphicFrame'):
        result = shape.find('p:xfrm', NS)
    else:
        result = shape.find('p:spPr/a:xfrm', NS)
        if result is None:
            result = shape.find('p:grpSpPr/a:xfrm', NS)
    if result is None:
        raise ValueError('Shape has no explicit geometry; use layout_edits with inspected geometry')
    return result


def _geometry(shape):
    transform = _transform(shape)
    off, ext = transform.find('a:off', NS), transform.find('a:ext', NS)
    return [int(off.get('x')), int(off.get('y')), int(ext.get('cx')), int(ext.get('cy'))]


def _set_box(shape, box):
    table = shape.find('.//a:tbl', NS)
    if table is not None:
        for nodes, attr, total in [(table.findall('a:tblGrid/a:gridCol', NS), 'w', box[2]),
                                   (table.findall('a:tr', NS), 'h', box[3])]:
            weights = [int(n.get(attr)) for n in nodes]
            remainder = total
            for i, (node, weight) in enumerate(zip(nodes, weights)):
                extent = remainder if i == len(nodes) - 1 else round(total * weight / sum(weights))
                node.set(attr, str(extent))
                remainder -= extent
    transform = _transform(shape)
    for tag, attrs in [('off', dict(x=box[0], y=box[1])), ('ext', dict(cx=box[2], cy=box[3]))]:
        node = transform.find(f'a:{tag}', NS)
        for key, value in attrs.items():
            node.set(key, str(value))


def _replace_paragraph_text(paragraph, text):
    # Preserve the paragraph and first run's formatting, not obsolete links/fields.
    style = paragraph.find('a:r/a:rPr', NS)
    style = deepcopy(style) if style is not None else None
    if style is not None:
        for child in list(style):
            if etree.QName(child).localname.startswith('hlink'):
                style.remove(child)
    for child in list(paragraph):
        if child.tag != _tag('a:pPr'):
            paragraph.remove(child)
    run = etree.SubElement(paragraph, _tag('a:r'))
    if style is not None:
        run.append(style)
    etree.SubElement(run, _tag('a:t')).text = _text(text)


def _set_cell(cell, text):
    body = cell.find('a:txBody', NS)
    paragraphs = body.findall('a:p', NS)
    first = paragraphs[0]
    _replace_paragraph_text(first, text)
    for paragraph in paragraphs[1:]:
        body.remove(paragraph)


def _table_op(shape, op):
    table = shape.find('.//a:tbl', NS)
    if table is None:
        raise ValueError('Target is not a native table')
    rows = table.findall('a:tr', NS)
    grid = table.find('a:tblGrid', NS)
    cols = list(grid)
    cells = table.findall('.//a:tc', NS)
    if any(c.get(k) not in (None, '0', '1') for c in cells for k in ('rowSpan', 'gridSpan')) or any(
            c.get(k) in ('1', 'true') for c in table.findall('.//a:tc', NS) for k in ('hMerge', 'vMerge')):
        raise ValueError('Merged tables require unmerging in PowerPoint before structural editing')
    name = op['op']
    if name == 'table_set_cell':
        row = rows[_index(op.get('row'), len(rows))]
        cell = row.findall('a:tc', NS)[_index(op.get('column'), len(cols))]
        _set_cell(cell, op.get('text'))
        return
    axis = 'row' if name.endswith('_row') else 'column'
    insert = '_insert_' in name
    items = rows if axis == 'row' else cols
    index = _index(op.get('index'), len(items), insert)
    if not insert and len(items) == 1:
        raise ValueError('Cannot delete the last row/column; use delete_shape to remove the table')
    if insert:
        values = _values(op.get('values'), len(cols) if axis == 'row' else len(rows))
        if len(items) >= (40 if axis == 'row' else 20):
            raise ValueError('Table limit is 40 rows and 20 columns')
        if axis == 'row':
            row = deepcopy(rows[min(index, len(rows) - 1)])
            for cell, value in zip(row.findall('a:tc', NS), values):
                _set_cell(cell, value)
            table.insert(table.index(rows[0]) + index, row)
        else:
            grid.insert(index, deepcopy(cols[min(index, len(cols) - 1)]))
            for row, value in zip(rows, values):
                cells = row.findall('a:tc', NS)
                cell = deepcopy(cells[min(index, len(cells) - 1)])
                _set_cell(cell, value)
                row.insert(index, cell)
    elif axis == 'row':
        table.remove(rows[index])
    else:
        grid.remove(cols[index])
        for row in rows:
            row.remove(row.findall('a:tc', NS)[index])
    # Keep the same box and relative dimensions of surviving rows/columns.
    nodes = table.findall('a:tr', NS) if axis == 'row' else list(grid)
    attr = 'h' if axis == 'row' else 'w'
    extent = _geometry(shape)[3 if axis == 'row' else 2]
    weights = [int(node.get(attr)) for node in nodes]
    total = sum(weights)
    remaining = extent
    for index, (node, weight) in enumerate(zip(nodes, weights)):
        size = remaining if index == len(nodes) - 1 else round(extent * weight / total)
        if size < 914400 * (0.18 if axis == 'row' else 0.25):
            raise ValueError('Table cells would be too small; enlarge the table first')
        node.set(attr, str(size))
        remaining -= size


def _list_op(shape, op):
    body = shape.find('p:txBody', NS)
    if body is None:
        raise ValueError('List target must be a text shape')
    paragraphs = body.findall('a:p', NS)
    name = op['op']
    if name == 'set_list':
        values = _values(op.get('values'))
        template = deepcopy(paragraphs[0])
        for paragraph in paragraphs:
            body.remove(paragraph)
        for value in values:
            paragraph = deepcopy(template)
            _replace_paragraph_text(paragraph, value)
            body.append(paragraph)
    else:
        index = _index(op.get('index'), len(paragraphs), name == 'list_insert_item')
        if name == 'list_delete_item':
            if len(paragraphs) == 1:
                raise ValueError('Use delete_shape to remove the final list item')
            body.remove(paragraphs[index])
        elif name == 'list_set_item':
            _replace_paragraph_text(paragraphs[index], op.get('text'))
        else:
            paragraph = deepcopy(paragraphs[min(index, len(paragraphs) - 1)])
            _replace_paragraph_text(paragraph, op.get('text'))
            body.insert(body.index(paragraphs[0]) + index, paragraph)
    if name == 'set_list':
        ordered = op.get('ordered', False)
        if type(ordered) is not bool:
            raise ValueError('ordered must be boolean')
        for paragraph in body.findall('a:p', NS):
            props = paragraph.find('a:pPr', NS)
            if props is None:
                props = etree.Element(_tag('a:pPr'))
                paragraph.insert(0, props)
            for child in list(props):
                if etree.QName(child).localname in ('buNone', 'buChar', 'buAutoNum'):
                    props.remove(child)
            bullet = etree.Element(_tag('a:buAutoNum' if ordered else 'a:buChar'))
            bullet.set('type' if ordered else 'char', 'arabicPeriod' if ordered else '•')
            # Bullet choice precedes tabLst/defRPr/extLst in DrawingML.
            position = next((i for i, c in enumerate(props) if etree.QName(c).localname in
                             ('tabLst', 'defRPr', 'extLst')), len(props))
            props.insert(position, bullet)
            props.set('marL', '285750')
            props.set('indent', '-190500')


def _new_shape(op, size):
    box = _box(op.get('box'), size)
    prs = Presentation()
    slide = prs.slides.add_slide(prs.slide_layouts[6])
    if op['op'] == 'add_table':
        rows = op.get('rows')
        if not isinstance(rows, list) or not 1 <= len(rows) <= 40:
            raise ValueError('rows must contain 1–40 rows')
        values = [_values(row) for row in rows]
        count = len(values[0])
        if count > 20 or any(len(row) != count for row in values):
            raise ValueError('Table must be rectangular with at most 20 columns')
        if box[2] / count < 914400 * .25 or box[3] / len(rows) < 914400 * .18:
            raise ValueError('New table cells would be too small; enlarge its box')
        shape = slide.shapes.add_table(len(rows), count, *box)
        for i, row in enumerate(values):
            for j, value in enumerate(row):
                shape.table.cell(i, j).text = value
    else:
        shape = slide.shapes.add_textbox(*box)
        shape.text = _text(op.get('text', '')) if op['op'] == 'add_text' else ''
        if op['op'] == 'add_list':
            _list_op(shape._element, {**op, 'op': 'set_list'})
    size_pt = op.get('font_size_pt', 18)
    if type(size_pt) not in (int, float) or not math.isfinite(size_pt) or not 1 <= size_pt <= 400:
        raise ValueError('font_size_pt must be between 1 and 400')
    for props in shape._element.findall('.//a:rPr', NS):
        props.set('sz', str(round(size_pt * 100)))
    for paragraph in shape._element.findall('.//a:p', NS):
        for run in paragraph.findall('a:r', NS):
            props = run.find('a:rPr', NS)
            if props is None:
                props = etree.Element(_tag('a:rPr'))
                run.insert(0, props)
            props.set('sz', str(round(size_pt * 100)))
    return deepcopy(shape._element)


_OPERATIONS = {
    'add_text', 'add_list', 'add_table', 'delete_shape', 'duplicate_shape', 'set_list',
    'list_insert_item', 'list_delete_item', 'list_set_item', 'table_set_cell',
    'table_insert_row', 'table_delete_row', 'table_insert_column', 'table_delete_column',
    'resize', 'move', 'align', 'distribute', 'bring_to_front', 'send_to_back',
}


_FIELDS = {
    'add_text': {'box', 'text', 'font_size_pt'},
    'add_list': {'box', 'values', 'ordered', 'font_size_pt'},
    'add_table': {'box', 'rows', 'font_size_pt'},
    'duplicate_shape': {'box'}, 'resize': {'dw', 'dh'}, 'move': {'dx', 'dy'},
    'align': {'edge'}, 'distribute': {'axis'}, 'set_list': {'values', 'ordered'},
    'list_insert_item': {'index', 'text'}, 'list_set_item': {'index', 'text'},
    'list_delete_item': {'index'}, 'table_set_cell': {'row', 'column', 'text'},
    'table_insert_row': {'index', 'values'}, 'table_insert_column': {'index', 'values'},
    'table_delete_row': {'index'}, 'table_delete_column': {'index'},
}


def edit_structure(source, output, source_sha256, operations):
    """Apply ordered operations in memory; invalid batches never touch output."""
    source, output = Path(source), Path(output)
    if source.resolve() == output.resolve():
        raise ValueError('Output must not overwrite source')
    if hashlib.sha256(source.read_bytes()).hexdigest() != source_sha256:
        raise ValueError('Deck changed; inspect the current revision again')
    if not isinstance(operations, list) or not 1 <= len(operations) <= 200:
        raise ValueError('Provide 1–200 structural operations')
    infos, data, slides = _load(source)
    prs = Presentation(source)
    size = [prs.slide_width / 914400, prs.slide_height / 914400]
    originals = {page: etree.tostring(root) for page, (_, root) in enumerate(slides, 1)}
    changed, created = set(), []
    for op in operations:
        if not isinstance(op, dict) or op.get('op') not in _OPERATIONS:
            raise ValueError('Unknown structural operation')
        target_fields = set() if op['op'].startswith('add_') else {'shape_id', 'shape_ids'}
        if set(op) - {'op', 'page'} - target_fields - _FIELDS.get(op['op'], set()):
            raise ValueError('Unsupported fields for structural operation')
        if 'shape_id' in op and 'shape_ids' in op:
            raise ValueError('Use shape_id or shape_ids, not both')
        page = op.get('page')
        if type(page) is not int or not 1 <= page <= len(slides):
            raise ValueError('page must be a valid 1-based slide number')
        tree = slides[page - 1][1].find('p:cSld/p:spTree', NS)
        name = op['op']
        if name.startswith('add_'):
            shapes = [_new_shape(op, size)]
        else:
            ids = op.get('shape_ids', [op.get('shape_id')])
            if (not isinstance(ids, list) or not ids or any(type(i) is not int for i in ids)
                    or len(ids) != len(set(ids))):
                raise ValueError('shape_ids must contain unique inspected IDs')
            shapes = [_shape(tree, identity) for identity in ids]
        geometric = name in ('resize', 'move', 'align', 'distribute')
        boxes = [_geometry(shape) for shape in shapes] if geometric else []
        for index, shape in enumerate(shapes):
            if name.startswith('add_') or name == 'duplicate_shape':
                clone = deepcopy(shape)
                if name == 'duplicate_shape':
                    if shape.tag not in (_tag('p:sp'), _tag('p:pic')):
                        raise ValueError('Duplicate supports text/shapes/pictures; charts/groups are not copied')
                    _set_box(clone, _box(op.get('box'), size))
                highest = max((int(n.get('id')) for n in tree.findall('.//p:cNvPr', NS)), default=1)
                for prop in clone.findall('.//p:cNvPr', NS):
                    highest += 1
                    prop.set('id', str(highest))
                    prop.set('name', f'Edited shape {highest}')
                ext = tree.find('p:extLst', NS)
                tree.insert(tree.index(ext) if ext is not None else len(tree), clone)
                created.append({'page': page, 'shape_id': highest})
            elif name == 'delete_shape':
                tree.remove(shape)
            elif name in ('bring_to_front', 'send_to_back'):
                tree.remove(shape)
                ext = tree.find('p:extLst', NS)
                position = (tree.index(ext) if ext is not None else len(tree)) if name == 'bring_to_front' else 2
                tree.insert(position, shape)
            elif name.startswith('table_'):
                _table_op(shape, op)
            elif name.startswith('list_') or name == 'set_list':
                _list_op(shape, op)
            else:
                box = [value / 914400 for value in boxes[index]]
                if name in ('move', 'resize'):
                    keys = ('dx', 'dy') if name == 'move' else ('dw', 'dh')
                    offset = 0 if name == 'move' else 2
                    for j, key in enumerate(keys):
                        value = op.get(key, 0)
                        if type(value) not in (int, float) or not math.isfinite(value):
                            raise ValueError(f'{key} must be a finite number in inches')
                        box[offset + j] += value
                elif name == 'align':
                    axis = op.get('edge')
                    if axis not in ('left', 'right', 'top', 'bottom', 'center', 'middle'):
                        raise ValueError('edge must be left/right/top/bottom/center/middle')
                    dim = 0 if axis in ('left', 'right', 'center') else 1
                    extent = dim + 2
                    low = min(b[dim] for b in boxes) / 914400
                    high = max(b[dim] + b[extent] for b in boxes) / 914400
                    box[dim] = (low if axis in ('left', 'top') else high - box[extent] if axis in
                                ('right', 'bottom') else (low + high - box[extent]) / 2)
                else:
                    if op.get('axis') not in ('horizontal', 'vertical') or len(shapes) < 3:
                        raise ValueError('distribute requires axis horizontal/vertical and at least three shapes')
                    dim = 0 if op['axis'] == 'horizontal' else 1
                    order = sorted(range(len(boxes)), key=lambda i: boxes[i][dim])
                    start = boxes[order[0]][dim]
                    end = boxes[order[-1]][dim] + boxes[order[-1]][dim + 2]
                    gap = (end - start - sum(b[dim + 2] for b in boxes)) / (len(boxes) - 1)
                    if gap < 0:
                        raise ValueError('Not enough space to distribute shapes without overlap')
                    before = order[:order.index(index)]
                    box[dim] = (start + sum(boxes[i][dim + 2] for i in before) + len(before) * gap) / 914400
                _set_box(shape, _box(box, size))
        changed.add(page)
    changed = {page for page in changed if etree.tostring(slides[page - 1][1]) != originals[page]}
    if not changed:
        raise ValueError('Structural batch made no change')
    for page in changed:
        name, root = slides[page - 1]
        data[name] = etree.tostring(root, xml_declaration=True, encoding='UTF-8', standalone=True)
    output.parent.mkdir(parents=True, exist_ok=True)
    fd, temporary = tempfile.mkstemp(dir=output.parent, suffix='.pptx')
    os.close(fd)
    try:
        with ZipFile(temporary, 'w') as archive:
            for info in infos:
                archive.writestr(info, data[info.filename])
        os.replace(temporary, output)
    finally:
        Path(temporary).unlink(missing_ok=True)
    return {'updated_structure_pages': sorted(changed), 'created_shapes': created,
            'edit_mode': 'sparse_patch', 'regenerated_pages': [],
            'changed_parts': [slides[p - 1][0] for p in sorted(changed)]}
