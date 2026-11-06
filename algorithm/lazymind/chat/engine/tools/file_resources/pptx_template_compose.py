"""Compose native template pages, remove sample shapes, and add editable charts."""
import hashlib
import io
import math
import os
import posixpath
import tempfile
from copy import deepcopy
from pathlib import Path
from zipfile import ZipFile, ZIP_DEFLATED

from lxml import etree

from .pptx_template_fill import NS, _load, _xml, _slots, fill_template
from .pptx_content_edit import edit_content, _serialized, REL, OFFICE, CT
from .pptx_tables import add_tables, recover_text_tables


def _visual_box(prs, slide, box, kind):
    from pptx.util import Inches
    if (not isinstance(box, list) or len(box) != 4
            or any(type(v) not in (int, float) or not math.isfinite(v) for v in box)):
        raise ValueError(f'{kind} box must be [x, y, width, height] in inches')
    # Placement quality must not abort a deliverable. Clamp requested geometry
    # to the canvas; overlapping content can be improved by screenshot review.
    slide_w, slide_h = prs.slide_width / 914400, prs.slide_height / 914400
    x = min(max(0, box[0]), slide_w - 1 / 72)
    y = min(max(0, box[1]), slide_h - 1 / 72)
    w = min(max(1 / 72, box[2]), slide_w - x)
    h = min(max(1 / 72, box[3]), slide_h - y)
    x, y, w, h = [Inches(v) for v in (x, y, w, h)]
    return x, y, w, h


def _page_images(spec, assets):
    images = spec.get('images', [])
    if not isinstance(images, list) or len(images) > 12:
        raise ValueError('images must contain at most 12 registered image placements per page')
    result = []
    for placement in images:
        if (not isinstance(placement, dict) or type(placement.get('material_index')) is not int
                or placement['material_index'] not in assets):
            raise ValueError('Image material_index must reference the current conversation material pool')
        asset = assets[placement['material_index']]
        if not isinstance(asset.get('source'), str) or not asset['source'].strip():
            raise ValueError('Image requires a source citation')
        result.append((placement, asset))
    return result


def _add_editable_diagrams(prs, slide, diagrams):
    """Business flow/architecture diagrams; malformed optional visuals never block delivery."""
    from pptx.dml.color import RGBColor
    from pptx.enum.shapes import MSO_CONNECTOR, MSO_SHAPE
    from pptx.enum.text import MSO_ANCHOR, PP_ALIGN
    from pptx.util import Pt

    count, warnings = 0, []
    if not isinstance(diagrams, list):
        return 0, ['diagrams must be a list; optional diagrams skipped']
    for index, diagram in enumerate(diagrams[:8]):
        try:
            nodes = {}
            for item in diagram.get('nodes', [])[:24]:
                identity = str(item['id'])
                if identity in nodes or not isinstance(item.get('text'), str):
                    continue
                x, y, w, h = _visual_box(prs, slide, item['box'], 'Diagram')
                shape = slide.shapes.add_shape(MSO_SHAPE.ROUNDED_RECTANGLE, x, y, w, h)
                shape.name = 'Diagram node ' + identity
                shape.fill.solid()
                shape.fill.fore_color.rgb = RGBColor.from_string('EDF5FA')
                shape.line.color.rgb = RGBColor.from_string('32738C')
                shape.text = item['text']
                shape.text_frame.word_wrap = True
                shape.text_frame.vertical_anchor = MSO_ANCHOR.MIDDLE
                for paragraph in shape.text_frame.paragraphs:
                    paragraph.alignment = PP_ALIGN.CENTER
                    paragraph.font.name = 'Microsoft YaHei'
                    paragraph.font.size = Pt(20)
                    paragraph.font.color.rgb = RGBColor.from_string('173B4B')
                nodes[identity] = shape
            for edge in diagram.get('edges', [])[:40]:
                source, target = nodes.get(str(edge.get('from'))), nodes.get(str(edge.get('to')))
                if source is None or target is None:
                    continue
                horizontal = abs(target.left - source.left) >= abs(target.top - source.top)
                start, end = ((3, 1) if target.left >= source.left else (1, 3)) if horizontal else (
                    (2, 0) if target.top >= source.top else (0, 2))
                connector = slide.shapes.add_connector(MSO_CONNECTOR.STRAIGHT, 0, 0, 1, 1)
                connector.begin_connect(source, start)
                connector.end_connect(target, end)
                connector.line.color.rgb = RGBColor.from_string('32738C')
                connector.line.width = Pt(2)
                line = connector.element.find('p:spPr/a:ln', NS)
                etree.SubElement(line, '{%s}tailEnd' % NS['a'], type='triangle')
            count += bool(nodes)
        except (ValueError, KeyError, TypeError, AttributeError) as exc:
            warnings.append(f'diagram[{index}] best-effort layout: {exc}')
    return count, warnings


def _add_visuals(path, pages, image_assets):
    from pptx import Presentation
    from pptx.chart.data import CategoryChartData
    from pptx.enum.chart import XL_CHART_TYPE, XL_LEGEND_POSITION
    from pptx.dml.color import RGBColor
    from pptx.util import Pt
    from PIL import Image

    prs = Presentation(path)
    count, image_count, diagram_count, diagram_warnings = 0, 0, 0, []
    table_reports = []
    kinds = {'column': XL_CHART_TYPE.COLUMN_CLUSTERED, 'bar': XL_CHART_TYPE.BAR_CLUSTERED,
             'line': XL_CHART_TYPE.LINE_MARKERS, 'pie': XL_CHART_TYPE.PIE,
             'doughnut': XL_CHART_TYPE.DOUGHNUT}
    for page_number, (slide, spec) in enumerate(zip(prs.slides, pages), 1):
        for field in slide.element.xpath('.//a:fld[@type="slidenum"]'):
            for text in field.xpath('.//a:t'):
                text.text = str(page_number)
        added, warnings = _add_editable_diagrams(prs, slide, spec.get('diagrams', []))
        diagram_count += added
        diagram_warnings.extend(warnings)
        boxes, recovered = recover_text_tables(spec.get('text_boxes', []))
        declared = spec.get('tables', [])
        if not isinstance(declared, list):
            raise ValueError('tables must be a list')
        try:
            reports = add_tables(prs, slide, declared + recovered)
        except ValueError as exc:
            raise ValueError(f'Page {page_number}: {exc}') from exc
        table_reports.extend({**report, 'page': page_number, 'recovered': index >= len(declared)}
                             for index, report in enumerate(reports))
        if not isinstance(boxes, list) or len(boxes) > 20:
            raise ValueError('text_boxes must contain at most 20 editable text blocks; use tables for cell grids')
        for box in boxes:
            if not isinstance(box, dict) or not isinstance(box.get('text'), str) or not box['text'].strip():
                raise ValueError('Each text box requires non-empty plain text')
            size = box.get('font_size', 22)
            if type(size) not in (int, float) or not math.isfinite(size):
                raise ValueError('text_boxes font_size must be a finite number')
            size = min(44, max(18, size))
            x, y, w, h = _visual_box(prs, slide, box.get('box'), 'Text')
            shape = slide.shapes.add_textbox(x, y, w, h)
            frame = shape.text_frame
            frame.word_wrap = True
            frame.margin_left = frame.margin_right = Pt(6)
            frame.margin_top = frame.margin_bottom = Pt(6)
            frame.text = box['text']
            color = box.get('color', '1F2937')
            if not isinstance(color, str) or len(color) != 6 or any(c not in '0123456789abcdefABCDEF' for c in color):
                raise ValueError('Text color must be a six-digit RGB hex string')
            for paragraph in frame.paragraphs:
                paragraph.font.name = box.get('font_family', 'Microsoft YaHei')
                paragraph.font.size = Pt(size)
                paragraph.font.color.rgb = RGBColor.from_string(color)
                paragraph.font.bold = bool(box.get('bold', False))
        charts = spec.get('charts', [])
        if not isinstance(charts, list) or len(charts) > 8:
            raise ValueError('charts must be a list of at most 8 charts per slide')
        for chart in charts:
            if not isinstance(chart, dict) or chart.get('type') not in kinds:
                raise ValueError('Chart type must be column, bar, line, pie or doughnut')
            if not isinstance(chart.get('source'), str) or not chart['source'].strip():
                raise ValueError('Every chart requires a source citation from collected evidence')
            categories, series = chart.get('categories'), chart.get('series')
            if (not isinstance(categories, list) or not 1 <= len(categories) <= 30
                    or any(not isinstance(c, str) or not c.strip() for c in categories)
                    or not isinstance(series, list) or not 1 <= len(series) <= 8):
                raise ValueError('Charts require 1-30 category labels and 1-8 series')
            parts_of_whole = chart['type'] in ('pie', 'doughnut')
            if parts_of_whole and len(series) != 1:
                raise ValueError('Pie/doughnut requires one series representing parts of the same whole')
            x, y, w, h = _visual_box(prs, slide, chart.get('box'), 'Chart')
            data = CategoryChartData()
            data.categories = categories
            for item in series:
                if not isinstance(item, dict) or not isinstance(item.get('name'), str) or not item['name'].strip():
                    raise ValueError('Chart series requires a name')
                values = item.get('values')
                if (not isinstance(values, list) or len(values) != len(categories)
                        or any(type(v) not in (int, float) or not math.isfinite(v) for v in values)):
                    raise ValueError('Chart values must be finite numbers matching categories')
                if parts_of_whole and (any(v < 0 for v in values) or sum(values) <= 0):
                    raise ValueError('Pie/doughnut requires nonnegative values with a positive total')
                data.add_series(item['name'], values)
            native = slide.shapes.add_chart(kinds[chart['type']], x, y, w, h, data).chart
            from .pptx_chart_style import style_chart
            style_chart(native._chartSpace, chart.get('palette'), count)
            # Keep axis IDs in the shared positive signed/unsigned range. Some
            # readers reject python-pptx's negative defaults; converting those
            # to uint32 makes PowerPoint repair (and discard) the whole slide.
            axis_ids = {}
            for element in native._chartSpace.iter():
                if element.tag in (
                    '{http://schemas.openxmlformats.org/drawingml/2006/chart}axId',
                    '{http://schemas.openxmlformats.org/drawingml/2006/chart}crossAx',
                ):
                    original = element.get('val')
                    axis_ids.setdefault(original, str(100 + len(axis_ids)))
                    element.set('val', axis_ids[original])
            native.has_legend = parts_of_whole or len(series) > 1
            if native.has_legend:
                native.legend.position = XL_LEGEND_POSITION.BOTTOM
                native.legend.include_in_layout = False
            native.has_title = True
            title = chart.get('title', '')
            unit = chart.get('unit', '')
            if not isinstance(title, str) or not isinstance(unit, str):
                raise ValueError('Chart title and unit must be text')
            native.chart_title.text_frame.text = title + (f' ({unit})' if unit else '')
            native.font.size = Pt(12)
            native.plots[0].has_data_labels = True
            native.plots[0].data_labels.font.size = Pt(11)
            if parts_of_whole:
                native.plots[0].data_labels.show_percentage = True
                native.plots[0].data_labels.show_value = False
                if chart['type'] == 'doughnut':
                    # python-pptx has no public hole-size property; persist OOXML.
                    holes = native._chartSpace.xpath('.//c:doughnutChart/c:holeSize')
                    if holes:
                        holes[0].set('val', '60')
                    else:
                        etree.SubElement(native._chartSpace.xpath('.//c:doughnutChart')[0],
                                         '{http://schemas.openxmlformats.org/drawingml/2006/chart}holeSize', val='60')
            native.font.color.rgb = RGBColor.from_string('334155')
            if not parts_of_whole:
                native.value_axis.has_major_gridlines = True
                native.value_axis.major_gridlines.format.line.color.rgb = RGBColor.from_string('E2E8F0')
                native.value_axis.major_gridlines.format.line.width = Pt(.5)
                for axis in (native.value_axis, native.category_axis):
                    axis.format.line.color.rgb = RGBColor.from_string('CBD5E1')
                    axis.format.line.width = Pt(.5)
            # A common zero baseline avoids exaggerating performance differences.
            if chart['type'] in ('bar', 'column'):
                native.value_axis.minimum_scale = min(0, min(v for s in series for v in s['values']))
            count += 1
        for placement, asset in _page_images(spec, image_assets):
            raw = Path(asset['path']).read_bytes()
            if hashlib.sha256(raw).hexdigest() != asset.get('sha256'):
                raise ValueError('Registered image changed; collect and register the material again')
            x, y, w, h = _visual_box(prs, slide, placement.get('box'), 'Image')
            with Image.open(io.BytesIO(raw)) as picture:
                if picture.width * picture.height > 36_000_000:
                    raise ValueError('Image exceeds 36 megapixels')
                scale = min(w / picture.width, h / picture.height)
                width, height = max(1, round(picture.width * scale)), max(1, round(picture.height * scale))
                stream = io.BytesIO()
                picture.convert('RGBA').save(stream, format='PNG')
            stream.seek(0)
            shape = slide.shapes.add_picture(stream, x + (w - width) // 2, y + (h - height) // 2, width, height)
            shape.name = f'Material {placement["material_index"]}'
            props = shape.element.find('.//p:cNvPr', NS)
            props.set('descr', str(asset.get('caption') or asset['source']))
            image_count += 1
    # Serialize the reachable graph to discard hidden original sample slides/notes.
    prs.save(path)
    return count, image_count, diagram_count, diagram_warnings, table_reports


def compose_template(source: Path, output: Path, source_sha256: str, pages: list[dict],
                     image_assets: dict | None = None) -> dict:
    """Each output page selects a source_page and explicit edits using ORIGINAL slot IDs."""
    if source.resolve() == output.resolve():
        raise ValueError('Output must not overwrite the uploaded template')
    if hashlib.sha256(source.read_bytes()).hexdigest() != source_sha256:
        raise ValueError('Template changed; inspect it again before editing')
    if not isinstance(pages, list) or not 1 <= len(pages) <= 200:
        raise ValueError('pages must contain 1-200 output pages')
    image_assets = image_assets or {}
    _, data, slides = _load(source)
    presentation = _xml(data['ppt/presentation.xml'])
    listing = presentation.find('p:sldIdLst', NS)
    listing.clear()
    for custom in presentation.findall('p:custShowLst', NS):
        presentation.remove(custom)
    # Section IDs and custom shows refer to the source slide order.
    for section in presentation.xpath('.//*[local-name()="sectionLst"]'):
        section.getparent().remove(section)
    rels = _xml(data['ppt/_rels/presentation.xml.rels'])
    for rel in list(rels):
        if rel.get('Type') == OFFICE + 'slide':
            rels.remove(rel)
    types = _xml(data['[Content_Types].xml'])
    edits, notes = [], []
    removed_count = 0
    for index, spec in enumerate(pages, 1):
        if (not isinstance(spec, dict) or type(spec.get('source_page')) is not int
                or not 1 <= spec['source_page'] <= len(slides)):
            raise ValueError('source_page must identify an inspected template page')
        source_page = spec['source_page']
        original_name, original = slides[source_page - 1]
        root = deepcopy(original)
        original_slots = {node: slot for node, slot in _slots(root, source_page)}
        removals = spec.get('remove_shape_ids', [])
        shapes = {s.find('.//p:cNvPr', NS).get('id'): s for s in root.xpath(
            './/p:sp | .//p:pic | .//p:graphicFrame | .//p:grpSp | .//p:cxnSp', namespaces=NS)
            if s.find('.//p:cNvPr', NS) is not None}
        if (not isinstance(removals, list) or any(not isinstance(i, str) for i in removals)
                or len(set(removals)) != len(removals) or set(removals) - shapes.keys()):
            raise ValueError('remove_shape_ids must contain unique inspected shape IDs')
        for shape_id in removals:
            shape = shapes[shape_id]
            shape.getparent().remove(shape)
        removed_count += len(removals)
        placements = spec.get('shape_boxes', [])
        if not isinstance(placements, list):
            raise ValueError('shape_boxes must be a list of shape_id/box placements')
        seen = set()
        slide_size = presentation.find('p:sldSz', NS)
        for placement in placements:
            if not isinstance(placement, dict):
                raise ValueError('Invalid shape_boxes placement')
            shape_id, box = placement.get('shape_id'), placement.get('box')
            if shape_id not in shapes or shape_id in removals or shape_id in seen:
                raise ValueError('shape_boxes requires unique surviving inspected shape IDs')
            seen.add(shape_id)
            shape = shapes[shape_id]
            if shape.tag != '{%s}sp' % NS['p'] or shape.getparent() != root.find('p:cSld/p:spTree', NS):
                raise ValueError('Only top-level text shapes may be repositioned')
            if (not isinstance(box, list) or len(box) != 4
                    or any(type(v) not in (int, float) or not math.isfinite(v) for v in box)):
                raise ValueError('shape_boxes box must be [x,y,width,height] inches')
            slide_w, slide_h = int(slide_size.get('cx')) / 914400, int(slide_size.get('cy')) / 914400
            x = min(max(0, box[0]), slide_w - 1 / 72)
            y = min(max(0, box[1]), slide_h - 1 / 72)
            w = min(max(1 / 72, box[2]), slide_w - x)
            h = min(max(1 / 72, box[3]), slide_h - y)
            x, y, w, h = [round(v * 914400) for v in (x, y, w, h)]
            props = shape.find('p:spPr', NS)
            if props is None:
                props = etree.SubElement(shape, '{%s}spPr' % NS['p'])
            transform = props.find('a:xfrm', NS)
            if transform is None:
                transform = etree.SubElement(props, '{%s}xfrm' % NS['a'])
            for child in list(transform):
                transform.remove(child)
            etree.SubElement(transform, '{%s}off' % NS['a'], x=str(x), y=str(y))
            etree.SubElement(transform, '{%s}ext' % NS['a'], cx=str(w), cy=str(h))
        actions = spec.get('edits')
        if not isinstance(actions, list) or any(not isinstance(e, dict) for e in actions):
            raise ValueError('Each output page requires an edits list')
        ids = [e.get('id') for e in actions]
        survivors = {original_slots[node]['id']: slot['id'] for node, slot in _slots(root, index)}
        if (any(not isinstance(i, str) for i in ids) or len(set(ids)) != len(ids) or set(ids) != set(survivors)):
            raise ValueError('Provide one keep/replace action per surviving ORIGINAL text slot; omit removed shapes')
        edits.extend({**e, 'id': survivors[e['id']]} for e in actions)
        number = index
        while f'ppt/slides/slide{number}.xml' in data:
            number += 1
        name = f'ppt/slides/slide{number}.xml'
        data[name] = _serialized(root)
        original_rels = posixpath.join(posixpath.dirname(original_name), '_rels',
                                       posixpath.basename(original_name) + '.rels')
        if original_rels in data:
            page_rels = _xml(data[original_rels])
            # Sample notes and navigation must not point at hidden original pages.
            removed_rids = set()
            used_rids = {value for node in root.iter() for key, value in node.attrib.items()
                         if key.startswith('{%s}' % NS['r'])}
            for rel in list(page_rels):
                if (rel.get('Type') in (OFFICE + 'notesSlide', OFFICE + 'slide')
                        or (rel.get('Type') != OFFICE + 'slideLayout' and rel.get('Id') not in used_rids)):
                    removed_rids.add(rel.get('Id'))
                    page_rels.remove(rel)
            for link in root.xpath('.//a:hlinkClick | .//a:hlinkMouseOver', namespaces=NS):
                if link.get('{%s}id' % NS['r']) in removed_rids:
                    link.getparent().remove(link)
            data[name] = _serialized(root)
            data[f'ppt/slides/_rels/slide{number}.xml.rels'] = _serialized(page_rels)
        rid = f'rIdComposed{index}'
        while any(r.get('Id') == rid for r in rels):
            rid += '_'
        etree.SubElement(rels, '{%s}Relationship' % REL, Id=rid, Type=OFFICE + 'slide',
                         Target=f'slides/slide{number}.xml')
        etree.SubElement(listing, '{%s}sldId' % NS['p'], id=str(255 + index), **{'{%s}id' % NS['r']: rid})
        etree.SubElement(types, '{%s}Override' % CT, PartName='/' + name,
                         ContentType='application/vnd.openxmlformats-officedocument.presentationml.slide+xml')
        note = spec.get('notes', '')
        if not isinstance(note, str):
            raise ValueError('notes must be plain text')
        charts = spec.get('charts', [])
        if not isinstance(charts, list) or any(not isinstance(c, dict) for c in charts):
            raise ValueError('charts must be a list of chart objects')
        citations = [c['source'] for c in charts if isinstance(c.get('source'), str)]
        for _, asset in _page_images(spec, image_assets):
            citation = asset['source']
            provenance = asset.get('pdf_provenance') or {}
            if provenance.get('filename') and provenance.get('page'):
                citation += f' [{provenance["filename"]}, p. {provenance["page"]}]'
            citations.append(citation)
        notes.append({'page': index, 'text': '\n'.join([note, *citations]).strip()})
    data['ppt/presentation.xml'] = _serialized(presentation)
    data['ppt/_rels/presentation.xml.rels'] = _serialized(rels)
    data['[Content_Types].xml'] = _serialized(types)
    # All validation occurs in staging; failures never create/replace output.
    output.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(dir=output.parent, prefix='compose-') as temporary:
        draft, filled, final = [Path(temporary) / f'{name}.pptx' for name in ('draft', 'filled', 'final')]
        with ZipFile(draft, 'w', compression=ZIP_DEFLATED) as archive:
            for name, raw in data.items():
                archive.writestr(name, raw)
        stats = fill_template(draft, filled, hashlib.sha256(draft.read_bytes()).hexdigest(), edits)
        edit_content(filled, final, hashlib.sha256(filled.read_bytes()).hexdigest(), speaker_notes=notes)
        chart_count, image_count, diagram_count, diagram_warnings, table_reports = _add_visuals(
            final, pages, image_assets)
        os.replace(final, output)
    return {**stats, 'template_mode': 'composed', 'source_pages': [s['source_page'] for s in pages],
            'removed_shape_count': removed_count, 'editable_chart_count': chart_count,
            'inserted_image_count': image_count, 'editable_diagram_count': diagram_count,
            'diagram_warnings': diagram_warnings, 'editable_table_count': len(table_reports),
            'table_layouts': table_reports, 'sample_notes_cleared': True}
