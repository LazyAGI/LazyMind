"""Targeted native slide text and speaker-note edits, preserving package parts."""
import hashlib
import posixpath
import tempfile
from pathlib import Path
from collections.abc import Mapping
from zipfile import ZipFile, ZIP_DEFLATED
from lxml import etree
from .pptx_template_fill import _load, _xml, _slots, _inspect_page, fill_template, NS
from .pptx_layout_check import _related

REL = 'http://schemas.openxmlformats.org/package/2006/relationships'
OFFICE = 'http://schemas.openxmlformats.org/officeDocument/2006/relationships/'
CT = 'http://schemas.openxmlformats.org/package/2006/content-types'


def _charts(data, slide_name, root):
    rel_name = posixpath.join(posixpath.dirname(slide_name), '_rels', posixpath.basename(slide_name) + '.rels')
    if rel_name not in data:
        return []
    rels = {r.get('Id'): r for r in _xml(data[rel_name])}
    charts = []
    for node in root.iter('{http://schemas.openxmlformats.org/drawingml/2006/chart}chart'):
        rel = rels.get(node.get('{%s}id' % NS['r']))
        if rel is None or rel.get('TargetMode') == 'External':
            continue
        target = rel.get('Target', '')
        name = (target.lstrip('/') if target.startswith('/') else
                posixpath.normpath(posixpath.join(posixpath.dirname(slide_name), target)))
        if name in data:
            charts.append((name, _xml(data[name])))
    return charts


def inspect_content(source, pages=None):
    from .pptx_layout_edit import layout_snapshot

    source = Path(source)
    loaded = _load(source)
    selected = pages if pages is not None else list(range(1, len(loaded[2]) + 1))
    if not isinstance(selected, list) or any(type(p) is not int or not 1 <= p <= len(loaded[2]) for p in selected):
        raise ValueError('pages must be a list of 1-based slide numbers')
    layouts = {p['page']: p for p in layout_snapshot(source, selected)}
    results = []
    for page in selected:
        info = _inspect_page(source, page, loaded)
        _, notes = _related(loaded[1], loaded[2][page - 1][0], 'notesSlide')
        body = _notes_body(notes) if notes is not None else None
        info.pop('quality_contract', None)
        info['layout'] = layouts[page]
        info['speaker_notes'] = '\n'.join(''.join(p.itertext())
                                          for p in body.findall('a:p', NS)) if body is not None else ''
        from .pptx_chart_style import C
        info['charts'] = [
            {'chart_index': index, 'series_count': len(root.findall(f'.//{{{C}}}ser')),
             'colors': root.xpath('.//a:srgbClr/@val', namespaces=NS)}
            for index, (_, root) in enumerate(_charts(loaded[1], loaded[2][page - 1][0], loaded[2][page - 1][1]), 1)
        ]
        results.append(info)
    return {'sha256': hashlib.sha256(source.read_bytes()).hexdigest(), 'slide_count': len(loaded[2]),
            'pages': results, 'instruction': 'Edit this generated deck, not the original template. '
            'Supply sparse text_edits with id/expected_text/text and speaker_notes with page/text. '
            'For geometry use layout_edits with page/shape_id/expected_box/box from layout, in inches. '
            'Detailed notes belong in speaker notes, not visible slide text. No generation workflow required.'}


def _notes_body(root):
    for sp in root.findall('.//p:sp', NS):
        ph = sp.find('p:nvSpPr/p:nvPr/p:ph', NS)
        if ph is not None and ph.get('type') == 'body':
            return sp.find('p:txBody', NS)
    return None


def _add_body(tree):
    ids = [int(x.get('id')) for x in tree.findall('.//p:cNvPr', NS) if x.get('id', '').isdigit()]
    sp = etree.SubElement(tree, '{%s}sp' % NS['p'])
    nv = etree.SubElement(sp, '{%s}nvSpPr' % NS['p'])
    etree.SubElement(nv, '{%s}cNvPr' % NS['p'], id=str(max(ids, default=1) + 1), name='Speaker Notes')
    etree.SubElement(nv, '{%s}cNvSpPr' % NS['p'])
    pr = etree.SubElement(nv, '{%s}nvPr' % NS['p'])
    etree.SubElement(pr, '{%s}ph' % NS['p'], type='body', idx='1')
    etree.SubElement(sp, '{%s}spPr' % NS['p'])
    body = etree.SubElement(sp, '{%s}txBody' % NS['p'])
    etree.SubElement(body, '{%s}bodyPr' % NS['a'])
    etree.SubElement(body, '{%s}lstStyle' % NS['a'])
    return body


def _new_notes():
    root = etree.Element('{%s}notes' % NS['p'], nsmap={k: v for k, v in NS.items()})
    common = etree.SubElement(root, '{%s}cSld' % NS['p'])
    tree = etree.SubElement(common, '{%s}spTree' % NS['p'])
    nv = etree.SubElement(tree, '{%s}nvGrpSpPr' % NS['p'])
    etree.SubElement(nv, '{%s}cNvPr' % NS['p'], id='1', name='')
    etree.SubElement(nv, '{%s}cNvGrpSpPr' % NS['p'])
    etree.SubElement(nv, '{%s}nvPr' % NS['p'])
    etree.SubElement(tree, '{%s}grpSpPr' % NS['p'])
    _add_body(tree)
    override = etree.SubElement(root, '{%s}clrMapOvr' % NS['p'])
    etree.SubElement(override, '{%s}masterClrMapping' % NS['a'])
    return root


def _serialized(root):
    return etree.tostring(root, xml_declaration=True, encoding='UTF-8', standalone=True)


def edit_content(source, output, source_sha256, text_edits=None, speaker_notes=None, require_all_notes=False,
                 chart_edits=None):
    source, output = Path(source), Path(output)
    if source.resolve() == output.resolve():
        raise ValueError('Output must not overwrite source')
    if hashlib.sha256(source.read_bytes()).hexdigest() != source_sha256:
        raise ValueError('Deck changed; inspect the current revision again')
    infos, data, slides = _load(source)
    text_edits = [] if text_edits is None else text_edits
    speaker_notes = [] if speaker_notes is None else speaker_notes
    chart_edits = [] if chart_edits is None else chart_edits
    if not isinstance(text_edits, (list, tuple)) or not isinstance(
            speaker_notes, (list, tuple)) or not isinstance(chart_edits, list) or not (
            text_edits or speaker_notes or chart_edits):
        raise ValueError('Provide text_edits, speaker_notes and/or chart_edits lists')
    notes = {}
    for item in speaker_notes:
        if not isinstance(item, Mapping) or type(item.get('page')) is not int or not 1 <= item['page'] <= len(slides):
            raise ValueError('Invalid notes page')
        page, text = item['page'], item.get('text')
        if page in notes or not isinstance(text, str) or len(
                text) > 50000 or any(ord(c) < 32 and c not in '\t\r\n' for c in text):
            raise ValueError('Duplicate page or invalid notes text (maximum 50000 characters per page)')
        notes[page] = text
    if require_all_notes and (set(notes) != set(range(1, len(slides) + 1))
                              or any(not s.strip() for s in notes.values())):
        raise ValueError('Every slide needs non-empty speaker notes')
    changed_pages = []
    if text_edits:
        slots = {s['id']: s for p, (_, root) in enumerate(slides, 1) for _, s in _slots(root, p)}
        replacements = {}
        for item in text_edits:
            if not isinstance(item, Mapping):
                raise ValueError('Each text edit must be an object')
            slot_id = item.get('id')
            if slot_id is None:
                page = item.get('page')
                if type(page) is not int or not 1 <= page <= len(slides):
                    raise ValueError('Provide an exact slot id or a valid 1-based page and expected_text')
                matches = [s['id'] for s in slots.values()
                           if s['id'].startswith(f'{page}:') and s['text'] == item.get('expected_text')]
                if len(matches) != 1:
                    raise ValueError(
                        f'Page {page}: original text matches {len(matches)} slots; '
                        'inspect this page and use an exact slot id')
                slot_id = matches[0]
            if not isinstance(slot_id, str) or slot_id not in slots:
                raise ValueError(f'Unknown text slot {slot_id!r}; inspect the target page and copy its exact id')
            if slot_id in replacements:
                raise ValueError(f'Duplicate text slot {slot_id!r}; submit only one edit per slot')
            replacements[slot_id] = {**item, 'id': slot_id, 'action': 'replace'}
        edits = [
            replacements.get(
                s['id'], {
                    'id': s['id'], 'expected_text': s['text'], 'action': 'keep'}) for s in slots.values()]
        # Reuse the established hash/text guards and pre-publication fit checks.
        with tempfile.TemporaryDirectory(prefix='pptx-content-') as temporary:
            candidate = Path(temporary) / 'text.pptx'
            fill_template(source, candidate, source_sha256, edits)
            infos, data, slides = _load(candidate)
        changed_pages = sorted({int(i.split(':')[0]) for i in replacements})
    for page, text in notes.items():
        slide_name = slides[page - 1][0]
        note_name, root = _related(data, slide_name, 'notesSlide')
        if root is None:
            index = 1
            while f'ppt/notesSlides/notesSlide{index}.xml' in data:
                index += 1
            note_name = f'ppt/notesSlides/notesSlide{index}.xml'
            root = _new_notes()
            rel_name = posixpath.join(posixpath.dirname(slide_name), '_rels', posixpath.basename(slide_name) + '.rels')
            rels = (
                _xml(data[rel_name]) if rel_name in data
                else etree.Element('{%s}Relationships' % REL, nsmap={None: REL})
            )
            rid = 1
            while any(r.get('Id') == f'rId{rid}' for r in rels):
                rid += 1
            etree.SubElement(
                rels, '{%s}Relationship' % REL, Id=f'rId{rid}', Type=OFFICE + 'notesSlide',
                Target=f'../notesSlides/notesSlide{index}.xml',
            )
            data[rel_name] = _serialized(rels)
            nr = etree.Element('{%s}Relationships' % REL, nsmap={None: REL})
            etree.SubElement(
                nr, '{%s}Relationship' % REL, Id='rId1', Type=OFFICE + 'slide',
                Target=posixpath.relpath(slide_name, 'ppt/notesSlides'),
            )
            master_name, _ = _related(data, 'ppt/presentation.xml', 'notesMaster')
            if master_name:
                etree.SubElement(
                    nr, '{%s}Relationship' % REL, Id='rId2', Type=OFFICE + 'notesMaster',
                    Target=posixpath.relpath(master_name, 'ppt/notesSlides'),
                )
            data[f'ppt/notesSlides/_rels/notesSlide{index}.xml.rels'] = _serialized(nr)
            types = _xml(data['[Content_Types].xml'])
            etree.SubElement(types, '{%s}Override' % CT, PartName='/' + note_name,
                             ContentType='application/vnd.openxmlformats-officedocument.presentationml.notesSlide+xml')
            data['[Content_Types].xml'] = _serialized(types)
        body = _notes_body(root)
        if body is None:
            body = _add_body(root.find('p:cSld/p:spTree', NS))
        for p in list(body.findall('a:p', NS)):
            body.remove(p)
        for line in text.replace('\r\n', '\n').replace('\r', '\n').split('\n'):
            p = etree.SubElement(body, '{%s}p' % NS['a'])
            run = etree.SubElement(p, '{%s}r' % NS['a'])
            etree.SubElement(run, '{%s}t' % NS['a']).text = line
        data[note_name] = _serialized(root)
    chart_pages, targets = set(), set()
    for edit in chart_edits:
        if not isinstance(edit, dict):
            raise ValueError('Each chart edit must be an object')
        page, index = edit.get('page'), edit.get('chart_index')
        if type(page) is not int or not 1 <= page <= len(slides) or type(index) is not int:
            raise ValueError('Chart edits require an inspected 1-based page and chart_index')
        charts = _charts(data, slides[page - 1][0], slides[page - 1][1])
        if not 1 <= index <= len(charts) or (page, index) in targets or 'palette' not in edit:
            raise ValueError('Unknown/duplicate chart or missing palette; inspect the target page first')
        from .pptx_chart_style import style_chart
        name, root = charts[index - 1]
        if any(other != page - 1 and any(n == name for n, _ in _charts(data, slide_name, slide_root))
               for other, (slide_name, slide_root) in enumerate(slides)):
            raise ValueError('This chart is shared with another slide; a page-only recolor would affect both')
        style_chart(root, edit['palette'])
        data[name] = _serialized(root)
        targets.add((page, index))
        chart_pages.add(page)
    output.parent.mkdir(parents=True, exist_ok=True)
    with ZipFile(output, 'w', compression=ZIP_DEFLATED) as archive:
        originals = {i.filename: i for i in infos}
        for name, raw in data.items():
            archive.writestr(originals.get(name, name), raw)
    return {'slide_count': len(slides), 'updated_text_pages': changed_pages,
            'updated_chart_pages': sorted(chart_pages),
            'updated_notes_pages': sorted(notes), 'original_unchanged': True,
            'representation': 'native_pptx', 'generation_status': 'completed'}
