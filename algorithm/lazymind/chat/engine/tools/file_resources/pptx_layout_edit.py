"""Revision-bound geometry patches; untouched OOXML parts retain their bytes."""
from collections.abc import Mapping
import hashlib
import math
import os
from pathlib import Path
import tempfile
from zipfile import ZipFile

from lxml import etree
from pptx import Presentation

from .pptx_template_fill import NS, _load


def layout_snapshot(source: Path, pages=None) -> list[dict]:
    """Expose slide-local native shape IDs, distinct from text-run slot IDs.

    Top-level groups are addressable as a unit. Children use group coordinates
    and must not be mistaken for slide-coordinate boxes.
    """
    prs = Presentation(source)
    result = []
    for number, slide in enumerate(prs.slides, 1):
        if pages is not None and number not in pages:
            continue
        shapes = []
        for shape in slide.shapes:
            values = (shape.left, shape.top, shape.width, shape.height)
            if any(value is None for value in values):
                continue
            shapes.append({'shape_id': shape.shape_id, 'shape_name': shape.name,
                           'element_id': f'{number}:shape:{shape.shape_id}',
                           'kind': str(shape.shape_type),
                           'text': shape.text[:200] if shape.has_text_frame else '',
                           'box': [round(value / 914400, 6) for value in values]})
            if shape.has_table:
                shapes[-1]['table'] = {'rows': [[cell.text for cell in row.cells] for row in shape.table.rows],
                                       'merged': any(cell.is_merge_origin or cell.is_spanned
                                                     for row in shape.table.rows for cell in row.cells)}
            if shape.has_text_frame:
                shapes[-1]['paragraphs'] = [paragraph.text for paragraph in shape.text_frame.paragraphs]
        result.append({'page': number, 'size': [prs.slide_width / 914400, prs.slide_height / 914400],
                       'shapes': shapes})
    return result


def apply_layout_repairs(source: Path, output: Path, plan: dict, *, source_sha256: str,
                         allowed_pages=None, allowed_shapes=None) -> dict:
    """Validate the whole patch before atomically writing changed slide parts."""
    source, output = Path(source), Path(output)
    if source.resolve() == output.resolve():
        raise ValueError('Output must not overwrite source')
    if hashlib.sha256(source.read_bytes()).hexdigest() != source_sha256:
        raise ValueError('Deck changed; inspect the current revision again')
    if not isinstance(plan, Mapping) or set(plan) != {'moves'}:
        raise ValueError('Layout repair accepts only a moves object')
    moves = plan['moves']
    if not isinstance(moves, (list, tuple)) or not 1 <= len(moves) <= 100:
        raise ValueError('Layout repair requires 1–100 moves')
    infos, data, slides = _load(source)
    inventory = {p['page']: p for p in layout_snapshot(source)}
    seen, changed = set(), set()
    for move in moves:
        if not isinstance(move, Mapping) or set(move) - {'page', 'shape_id', 'box', 'expected_box'}:
            raise ValueError('Layout repair accepts only page, shape_id, expected_box and box')
        number, identity = move.get('page'), move.get('shape_id')
        if type(number) is not int or number not in inventory or type(identity) is not int:
            raise ValueError('Layout repair references an invalid page or shape')
        if allowed_pages is not None and number not in allowed_pages:
            raise ValueError('Layout repair is outside the reviewed problem pages')
        if allowed_shapes is not None and (number, identity) not in allowed_shapes:
            raise ValueError('Layout repair is outside the reviewed problem shapes')
        if (number, identity) in seen:
            raise ValueError('Duplicate shape in layout repair')
        seen.add((number, identity))
        item = next((s for s in inventory[number]['shapes'] if s['shape_id'] == identity), None)
        if item is None:
            raise ValueError('Unknown shape; inspect the page and use its exact shape_id')
        for field in ('box', 'expected_box'):
            box = move.get(field)
            if (not isinstance(box, (list, tuple)) or len(box) != 4
                    or any(type(v) not in (int, float) or not math.isfinite(v) for v in box)):
                raise ValueError(f'{field} requires four finite numbers in inches')
        if any(abs(a - b) > 0.000002 for a, b in zip(move['expected_box'], item['box'])):
            raise ValueError('Stale shape geometry; inspect the current revision again')
        x, y, w, h = [round(value * 914400) for value in move['box']]
        sw, sh = [round(value * 914400) for value in inventory[number]['size']]
        if min(x, y) < 0 or min(w, h) <= 0 or x + w > sw or y + h > sh:
            raise ValueError('Layout repair box is outside the slide')
        if [x, y, w, h] == [round(v * 914400) for v in item['box']]:
            continue
        name, root = slides[number - 1]
        tree = root.find('p:cSld/p:spTree', NS)
        matches = [node for node in tree if any(
            prop.get('id') == str(identity) for prop in node.findall('./*/p:cNvPr', NS))]
        if len(matches) != 1:
            raise ValueError('Shape ID is not uniquely addressable')
        shape = matches[0]
        kind = etree.QName(shape).localname
        if kind == 'graphicFrame':
            transform = shape.find('p:xfrm', NS)
        else:
            props = shape.find('p:grpSpPr' if kind == 'grpSp' else 'p:spPr', NS)
            if props is None:
                raise ValueError('Shape does not support a local geometry patch')
            transform = props.find('a:xfrm', NS)
            if transform is None:
                transform = etree.Element('{%s}xfrm' % NS['a'])
                props.insert(0, transform)
        if transform is None:
            raise ValueError('Shape transform is missing')
        for tag, attributes in [('off', {'x': x, 'y': y}), ('ext', {'cx': w, 'cy': h})]:
            node = transform.find(f'a:{tag}', NS)
            if node is None:
                node = etree.Element('{%s}%s' % (NS['a'], tag))
                transform.insert(0 if tag == 'off' else 1, node)
            for key, value in attributes.items():
                node.set(key, str(value))
        changed.add(number)
    if not changed:
        raise ValueError('Layout repair made no change')
    for number in changed:
        name, root = slides[number - 1]
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
    return {'updated_layout_pages': sorted(changed), 'changed_parts': [slides[n - 1][0] for n in sorted(changed)],
            'original_unchanged': True}
