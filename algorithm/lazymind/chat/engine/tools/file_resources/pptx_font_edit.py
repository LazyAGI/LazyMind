"""Sparse typography edits sharing inspect_content's stable text slot IDs."""
import hashlib
import os
import tempfile
from collections.abc import Mapping
from pathlib import Path
from zipfile import ZipFile, ZIP_DEFLATED
from lxml import etree

from .font_library import ensure_font
from .pptx_template_fill import _load, _slots, NS
from .presentation_edit import normalize_page_numbers
from .text_format import text_format
from .pptx_layout_check import inspect_layout


def edit_fonts(source, output, source_sha256, target_font=None, pages=None,
               slot_ids=None, text_scope='all', allow_download=False, *,
               font_size_pt=None, bold=None, italic=None, underline=None, text_color=None):
    formatting = text_format(font_size_pt, bold, italic, underline, text_color)
    if not target_font and not formatting:
        raise ValueError('Provide a target_font or at least one text formatting property')
    source, output = Path(source), Path(output)
    if source.resolve() == output.resolve():
        raise ValueError('Output must not overwrite source')
    if hashlib.sha256(source.read_bytes()).hexdigest() != source_sha256:
        raise ValueError('Deck changed; inspect the current revision again')
    if text_scope not in {'all', 'titles', 'body'}:
        raise ValueError('text_scope must be all, titles or body')
    infos, data, slides = _load(source)
    if isinstance(pages, (list, tuple)) and any(type(p) is not int for p in pages):
        raise ValueError('pages must contain integers')
    if isinstance(pages, bool):
        raise ValueError('pages must contain integers')
    selected = normalize_page_numbers(pages)
    selected = set(selected if selected is not None else range(1, len(slides) + 1))
    if any(p > len(slides) for p in selected):
        raise ValueError('Font edit page exceeds slide count')
    if slot_ids is not None and (not isinstance(slot_ids, (list, tuple)) or not slot_ids
                                 or any(not isinstance(s, str) for s in slot_ids)):
        raise ValueError('slot_ids must be a non-empty list of exact inspected IDs')
    requested = set(slot_ids or [])
    targets = []
    found = set()
    for page, (_name, root) in enumerate(slides, 1):
        if page not in selected:
            continue
        for node, slot in _slots(root, page):
            if requested and slot['id'] not in requested:
                continue
            shape = next((a for a in node.iterancestors() if a.tag == '{%s}sp' % NS['p']), None)
            ph = shape.find('.//p:ph', NS) if shape is not None else None
            title = ph is not None and ph.get('type') in {'title', 'ctrTitle'}
            if (text_scope == 'titles' and not title) or (text_scope == 'body' and title):
                continue
            targets.append((page, node, slot))
            found.add(slot['id'])
    if requested - found or not targets:
        raise ValueError('Requested font targets were not found in the selected scope; inspect exact slots')
    family = ensure_font(target_font, allow_download) if target_font else None
    before = {(page, report['shape_id']): report
              for page in selected for report in inspect_layout(data, slides[page - 1][0], slides[page - 1][1], page)}
    changed = set()
    for page, node, slot in targets:
        parent = node if slot['empty_paragraph'] else node.getparent()
        tag = 'endParaRPr' if slot['empty_paragraph'] else 'rPr'
        properties = parent.find('a:' + tag, NS)
        if properties is None:
            properties = etree.Element('{%s}%s' % (NS['a'], tag))
            if slot['empty_paragraph']:
                parent.append(properties)
            else:
                parent.insert(0, properties)
        if 'font_size_pt' in formatting:
            properties.set('sz', str(round(formatting['font_size_pt'] * 100)))
        for key, attr in (('bold', 'b'), ('italic', 'i')):
            if key in formatting:
                properties.set(attr, '1' if formatting[key] else '0')
        if 'underline' in formatting:
            properties.set('u', 'sng' if formatting['underline'] else 'none')
        if 'text_color' in formatting:
            for child in list(properties):
                if etree.QName(child).localname in {'noFill', 'solidFill',
                                                    'gradFill', 'blipFill', 'pattFill', 'grpFill'}:
                    properties.remove(child)
            fill = etree.Element('{%s}solidFill' % NS['a'])
            etree.SubElement(fill, '{%s}srgbClr' % NS['a'], val=formatting['text_color'][1:])
            properties.insert(1 if len(properties) and etree.QName(properties[0]).localname == 'ln' else 0, fill)
        for script in (('latin', 'ea', 'cs') if family else ()):
            child = properties.find('a:' + script, NS)
            if child is None:
                child = etree.Element('{%s}%s' % (NS['a'], script))
                later = {'latin': {'ea', 'cs', 'sym', 'hlinkClick', 'hlinkMouseOver', 'rtl', 'extLst'},
                         'ea': {'cs', 'sym', 'hlinkClick', 'hlinkMouseOver', 'rtl', 'extLst'},
                         'cs': {'sym', 'hlinkClick', 'hlinkMouseOver', 'rtl', 'extLst'}}[script]
                index = next((i for i, c in enumerate(properties) if etree.QName(c).localname in later), len(properties))
                properties.insert(index, child)
            child.set('typeface', family)
        changed.add(page)
    warnings = []
    for page in changed:
        name, root = slides[page - 1]
        for report in inspect_layout(data, name, root, page):
            previous = before.get((page, report['shape_id']), {})
            if report['status'] == 'overflow' and (
                previous.get('status') != 'overflow'
                or report.get('estimated_height_pt', 0) > previous.get('estimated_height_pt', 0) + 1
            ):
                raise ValueError(f'Page {page}, shape {report["shape_id"]}: formatting would overflow; '
                                 'choose a smaller font size or explicitly request layout changes. '
                                 'No version published.')
            if report['status'] in {'overflow', 'unknown'}:
                warnings.append({key: report[key] for key in ('page', 'shape_id', 'status', 'reason') if key in report})
        data[name] = etree.tostring(root, xml_declaration=True, encoding='UTF-8', standalone=True)
    with ZipFile(output, 'w', compression=ZIP_DEFLATED) as archive:
        for info in infos:
            archive.writestr(info, data[info.filename])
    complex_parts = [name for name in data if name.startswith(
        ('ppt/charts/', 'ppt/diagrams/')) and name.endswith('.xml')]
    return {'target_font': family, 'slide_count': len(slides), 'updated_font_pages': sorted(changed),
            'updated_font_slots': len(targets), 'original_unchanged': True,
            'text_format': formatting, 'layout_warnings': warnings,
            'layout_check': 'conservative estimate, not rendered visual validation',
            'unmodified_complex_text_parts': complex_parts,
            'font_notice': ('Receiving PowerPoint also needs this font installed; native font embedding is not added.'
                            if family else 'Existing font families are preserved.')}


def edit_style_batch(source, output, source_sha256, edits):
    """Apply differently scoped formatting atomically; indices never change."""
    if not isinstance(edits, (list, tuple)) or not edits:
        raise ValueError('style_edits must be a non-empty list')
    if Path(source).resolve() == Path(output).resolve():
        raise ValueError('Output must not overwrite source')
    allowed = {'target_font', 'pages', 'slot_ids', 'text_scope', 'allow_download',
               'font_size_pt', 'bold', 'italic', 'underline', 'text_color'}
    for edit in edits:
        if not isinstance(edit, Mapping) or set(edit) - allowed:
            raise ValueError('Each style edit must contain only documented scope and formatting properties')
    reports = []
    with tempfile.TemporaryDirectory(prefix='pptx-style-') as folder:
        current, digest = Path(source), source_sha256
        for index, edit in enumerate(edits):
            candidate = Path(folder) / f'{index}.pptx'
            reports.append(edit_fonts(current, candidate, digest, **dict(edit)))
            current = candidate
            digest = hashlib.sha256(current.read_bytes()).hexdigest()
        # Destination is only changed after every operation and fit check succeeds.
        Path(output).parent.mkdir(parents=True, exist_ok=True)
        import shutil
        handle, temporary = tempfile.mkstemp(dir=Path(output).parent, suffix='.pptx')
        os.close(handle)
        try:
            shutil.copyfile(current, temporary)
            os.replace(temporary, output)
        finally:
            Path(temporary).unlink(missing_ok=True)
    return {'style_edits': reports, 'updated_style_pages': sorted({p for r in reports for p in r['updated_font_pages']}),
            'slide_count': reports[-1]['slide_count'], 'original_unchanged': True}
