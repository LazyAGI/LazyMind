"""Conservative text capacity checks, not a PowerPoint rendering engine."""
import math
import posixpath
import unicodedata
from lxml import etree

NS = {'p': 'http://schemas.openxmlformats.org/presentationml/2006/main',
      'a': 'http://schemas.openxmlformats.org/drawingml/2006/main'}


def _related(data, name, kind):
    rel = posixpath.join(posixpath.dirname(name), '_rels', posixpath.basename(name) + '.rels')
    if rel not in data:
        return None, None
    root = etree.fromstring(data[rel], etree.XMLParser(resolve_entities=False, no_network=True))
    for item in root:
        if item.get('Type', '').endswith('/' + kind) and item.get('TargetMode') != 'External':
            target = item.get('Target', '')
            path = posixpath.normpath(posixpath.join(posixpath.dirname(name), target)
                                      ) if not target.startswith('/') else target[1:]
            if path in data:
                return path, etree.fromstring(data[path], etree.XMLParser(resolve_entities=False, no_network=True))
    return None, None


def _placeholder(shape, root, by_type=False):
    ph = shape.find('p:nvSpPr/p:nvPr/p:ph', NS)
    if ph is None or root is None:
        return None
    for candidate in root.findall('.//p:sp', NS):
        other = candidate.find('p:nvSpPr/p:nvPr/p:ph', NS)
        if other is not None and (other.get('type', 'obj') == ph.get('type', 'obj') if by_type
                                  else other.get('idx', '0') == ph.get('idx', '0')):
            return candidate
    return None


def _first(nodes, path):
    for node in nodes:
        found = node.find(path, NS) if node is not None else None
        if found is not None:
            return found
    return None


def _number(node, name, default):
    return float(node.get(name, default)) if node is not None else default


def inspect_layout(data, slide_name, root, page):
    layout_name, layout = _related(data, slide_name, 'slideLayout')
    _, master = _related(data, layout_name, 'slideMaster') if layout_name else (None, None)
    presentation = etree.fromstring(
        data['ppt/presentation.xml'],
        etree.XMLParser(
            resolve_entities=False,
            no_network=True))
    size = presentation.find('p:sldSz', NS)
    reports = []
    for shape in root.findall('.//p:sp', NS):
        body = shape.find('p:txBody', NS)
        if body is None:
            continue
        props = shape.find('p:nvSpPr/p:cNvPr', NS)
        inherited = _placeholder(shape, layout)
        inherited_master = _placeholder(inherited if inherited is not None else shape, master, True)
        chain = [shape, inherited, inherited_master]
        xfrm = _first(chain, 'p:spPr/a:xfrm')
        off = xfrm.find('a:off', NS) if xfrm is not None else None
        ext = xfrm.find('a:ext', NS) if xfrm is not None else None
        report = {'page': page, 'shape_id': props.get('id') if props is not None else None,
                  'text': '\n'.join(''.join('\n' if n.tag == '{%s}br' % NS['a'] else n.text or ''
                                            for n in p.iter() if n.tag in ('{%s}br' % NS['a'], '{%s}t' % NS['a']))
                                    for p in body.findall('a:p', NS)),
                  'status': 'unknown', 'method': 'conservative_estimate'}
        reports.append(report)
        # Group transforms/rotations require a renderer; do not pretend local
        # coordinates are slide coordinates.
        if ext is None or off is None or any(a.tag == '{%s}grpSp' %
                                             NS['p'] for a in shape.iterancestors()) or _number(xfrm, 'rot', 0):
            report['reason'] = 'unresolved geometry, group or rotated text'
            continue
        bp = _first(chain, 'p:txBody/a:bodyPr')
        width = (_number(ext, 'cx', 0) - _number(bp, 'lIns', 91440) - _number(bp, 'rIns', 91440)) / 12700
        height = (_number(ext, 'cy', 0) - _number(bp, 'tIns', 45720) - _number(bp, 'bIns', 45720)) / 12700
        if width <= 0 or height <= 0:
            report['reason'] = 'non-positive text area; auto-sized table/shape requires renderer'
            continue
        ph = shape.find('p:nvSpPr/p:nvPr/p:ph', NS)
        kind = ph.get('type', 'obj') if ph is not None else 'other'
        category = 'titleStyle' if kind in (
            'title', 'ctrTitle') else 'bodyStyle' if kind in (
            'body', 'obj') else 'otherStyle'
        needed, largest = 0.0, 0.0
        for paragraph in body.findall('a:p', NS):
            ppr = paragraph.find('a:pPr', NS)
            level = int(_number(ppr, 'lvl', 0)) + 1
            defaults = [ppr] + [n.find(f'p:txBody/a:lstStyle/a:lvl{level}pPr', NS) for n in chain if n is not None]
            if master is not None:
                defaults.append(master.find(f'p:txStyles/p:{category}/a:lvl{level}pPr', NS))
            defaults.append(presentation.find(f'p:defaultTextStyle/a:lvl{level}pPr', NS))
            default_sizes = [n.find('a:defRPr', NS) for n in defaults if n is not None]
            default_size = next((float(n.get('sz')) / 100 for n in default_sizes
                                 if n is not None and n.get('sz') is not None), 24)
            runs = paragraph.findall('a:r', NS) + paragraph.findall('a:fld', NS)
            font = max([_number(r.find('a:rPr', NS), 'sz', default_size * 100) / 100 for r in runs] or [default_size])
            largest = max(largest, font)
            margin = next((_number(n, 'marL', 0)
                           / 12700 for n in defaults if n is not None and n.get('marL') is not None), 0)
            available = max(1, width - margin)
            text = ''.join(
                ('\n' if n.tag == '{%s}br' %
                 NS['a'] else (
                     n.text or '') if n.tag == '{%s}t' %
                    NS['a'] else '') for n in paragraph.iter())
            lines = sum(max(1, math.ceil(sum(1 if unicodedata.east_asian_width(c) in ('W', 'F')
                        else .55 for c in line) * font / available)) for line in text.split('\n'))
            line_spacing = _first(defaults, 'a:lnSpc')
            pts = line_spacing.find('a:spcPts', NS) if line_spacing is not None else None
            pct = line_spacing.find('a:spcPct', NS) if line_spacing is not None else None
            line_height = _number(pts, 'val', font * 120) / 100 if pts is not None else font * \
                1.2 * _number(pct, 'val', 100000) / 100000
            # Line spacing separates baselines; it is not an extra full gap
            # after the last line (important for one-line template subtitles).
            needed += font * 1.15 + (lines - 1) * line_height
            for key in ('spcBef', 'spcAft'):
                # Paragraph-after spacing separates paragraphs, not the final
                # glyph from the box edge. Counting it rejects valid subtitles.
                if key == 'spcAft' and paragraph is body.findall('a:p', NS)[-1]:
                    continue
                if key == 'spcBef' and paragraph is body.findall('a:p', NS)[0]:
                    continue
                spacing = _first(defaults, 'a:' + key)
                if spacing is not None:
                    needed += _number(spacing.find('a:spcPts', NS), 'val', 0) / 100
                    needed += font * _number(spacing.find('a:spcPct', NS), 'val', 0) / 100000
        outside = size is not None and (_number(off, 'x', 0) < 0 or _number(off, 'y', 0) < 0
                                        or _number(off, 'x', 0) + _number(ext, 'cx', 0) > _number(size, 'cx', 0)
                                        or _number(off, 'y', 0) + _number(ext, 'cy', 0) > _number(size, 'cy', 0))
        report.update(status='overflow' if outside or needed > height * 1.02 else 'estimated_fit',
                      x_pt=round(_number(off, 'x', 0) / 12700, 1),
                      y_pt=round(_number(off, 'y', 0) / 12700, 1),
                      width_pt=round(width, 1), height_pt=round(height, 1), font_size_pt=largest,
                      estimated_height_pt=round(needed, 1),
                      suggested_max_cjk_chars=max(0, int(width / max(1, largest) * height / max(1, largest * 1.2) * .8)),
                      outside_slide=outside)
    # Table cells are separate capacity units. Merged cells need renderer-level
    # handling; expose them explicitly instead of claiming they passed.
    for table_no, table in enumerate(root.findall('.//a:tbl', NS), 1):
        columns = table.findall('a:tblGrid/a:gridCol', NS)
        for row_no, row in enumerate(table.findall('a:tr', NS), 1):
            for col_no, cell in enumerate(row.findall('a:tc', NS), 1):
                text = ''.join('\n' if n.tag == '{%s}br' % NS['a'] else n.text or ''
                               for n in cell.iter() if n.tag in ('{%s}br' % NS['a'], '{%s}t' % NS['a']))
                cell_id = f'table:{table_no}:{row_no}:{col_no}'
                if any(cell.get(k) not in (None, '0', '1') for k in ('gridSpan', 'rowSpan')) or cell.get(
                        'hMerge') == '1' or cell.get('vMerge') == '1' or col_no > len(columns):
                    reports.append(
                        dict(
                            page=page,
                            shape_id=cell_id,
                            text=text,
                            status='unknown',
                            reason='merged table cell'))
                    continue
                from copy import deepcopy
                fake = etree.Element('{%s}sld' % NS['p'])
                sp = etree.SubElement(fake, '{%s}sp' % NS['p'])
                nv = etree.SubElement(sp, '{%s}nvSpPr' % NS['p'])
                etree.SubElement(nv, '{%s}cNvPr' % NS['p'], id=cell_id)
                pr = etree.SubElement(sp, '{%s}spPr' % NS['p'])
                xf = etree.SubElement(pr, '{%s}xfrm' % NS['a'])
                etree.SubElement(xf, '{%s}off' % NS['a'], x='0', y='0')
                etree.SubElement(xf, '{%s}ext' % NS['a'], cx=columns[col_no - 1].get('w', '0'), cy=row.get('h', '0'))
                body = cell.find('a:txBody', NS)
                if body is None:
                    continue
                body = deepcopy(body)
                body.tag = '{%s}txBody' % NS['p']
                sp.append(body)
                bp = body.find('a:bodyPr', NS)
                if bp is None:
                    bp = etree.Element('{%s}bodyPr' % NS['a'])
                    body.insert(0, bp)
                cp = cell.find('a:tcPr', NS)
                for src, dst, default in [('marL', 'lIns', 91440), ('marR', 'rIns', 91440),
                                          ('marT', 'tIns', 45720), ('marB', 'bIns', 45720)]:
                    bp.set(dst, str(int(_number(cp, src, default))))
                cell_reports = inspect_layout(data, slide_name, fake, page)
                for r in cell_reports:
                    r['text'] = text
                reports.extend(cell_reports)
    return reports
