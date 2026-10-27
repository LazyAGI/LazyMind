"""Materialize inherited DrawingML typography for edited paragraphs only.

No font substitution, global theme rewrite or geometry changes. Explicitly
preserve the template's formatting instead of relying on consumer defaults.
"""
from copy import deepcopy
from functools import lru_cache
from lxml import etree
from .pptx_layout_check import NS, _related, _placeholder


def paragraph_defaults(data, name, shape, paragraph):
    layout_name, layout = _related(data, name, 'slideLayout')
    master_name, master = _related(data, layout_name, 'slideMaster') if layout_name else (None, None)
    inherited = _placeholder(shape, layout)
    ancestor = _placeholder(inherited if inherited is not None else shape, master, True)
    ppr = paragraph.find('a:pPr', NS)
    level = int(ppr.get('lvl', '0')) + 1 if ppr is not None else 1
    ph = shape.find('p:nvSpPr/p:nvPr/p:ph', NS)
    kind = ph.get('type', 'obj') if ph is not None else 'other'
    category = 'titleStyle' if kind in (
        'title', 'ctrTitle') else 'bodyStyle' if kind in (
        'body', 'obj') else 'otherStyle'
    defaults = [ppr]
    for item in (shape, inherited, ancestor):
        if item is not None:
            defaults.append(item.find(f'p:txBody/a:lstStyle/a:lvl{level}pPr', NS))
    if master is not None:
        defaults.append(master.find(f'p:txStyles/p:{category}/a:lvl{level}pPr', NS))
    presentation = etree.fromstring(
        data['ppt/presentation.xml'],
        etree.XMLParser(
            resolve_entities=False,
            no_network=True))
    defaults.append(presentation.find(f'p:defaultTextStyle/a:lvl{level}pPr', NS))
    _, theme = _related(data, master_name, 'theme') if master_name else (None, None)
    return [p for p in defaults if p is not None], theme


def _font_name(value, theme, script):
    if not value.startswith('+') or theme is None:
        return value
    family = 'majorFont' if value.startswith('+mj-') else 'minorFont'
    base = theme.find(f'a:themeElements/a:fontScheme/a:{family}', NS)
    if base is None:
        return value
    font = base.find('a:' + script, NS)
    name = font.get('typeface', '') if font is not None else ''
    if not name and script == 'ea':
        font = base.find("a:font[@script='Hans']", NS)
        name = font.get('typeface', '') if font is not None else ''
    return name or value


def materialize_paragraph(data, name, shape, paragraph):
    defaults, theme = paragraph_defaults(data, name, shape, paragraph)
    ppr = paragraph.find('a:pPr', NS)
    if ppr is None:
        ppr = etree.Element('{%s}pPr' % NS['a'])
        paragraph.insert(0, ppr)
    # Merge from least specific to most specific, preserving schema child order
    # from the source paragraph; inherited bullets/spacing remain independent.
    for source in defaults:
        for key, value in source.attrib.items():
            if key not in ppr.attrib:
                ppr.set(key, value)
    for tag in ('lnSpc', 'spcBef', 'spcAft', 'buClrTx', 'buClr', 'buSzTx', 'buSzPct', 'buSzPts',
                'buFontTx', 'buFont', 'buNone', 'buAutoNum', 'buChar', 'buBlip', 'tabLst'):
        # Bullet alternatives must not be combined across inheritance levels.
        groups = [('buNone', 'buAutoNum', 'buChar', 'buBlip'), ('buClrTx', 'buClr'),
                  ('buSzTx', 'buSzPct', 'buSzPts'), ('buFontTx', 'buFont')]
        group = next((g for g in groups if tag in g), (tag,))
        chosen = next((d.find('a:' + t, NS) for d in defaults for t in group
                       if d.find('a:' + t, NS) is not None), None)
        if chosen is not None and not any(ppr.find('a:' + t, NS) is not None for t in group):
            ppr.append(deepcopy(chosen))
    order = ['lnSpc', 'spcBef', 'spcAft', 'buClrTx', 'buClr', 'buSzTx', 'buSzPct', 'buSzPts',
             'buFontTx', 'buFont', 'buNone', 'buAutoNum', 'buChar', 'buBlip', 'tabLst', 'defRPr', 'extLst']
    ppr[:] = sorted(ppr, key=lambda n: order.index(etree.QName(n).localname)
                    if etree.QName(n).localname in order else len(order))
    families = set()
    for run in paragraph.findall('a:r', NS):
        rp = run.find('a:rPr', NS)
        if rp is None:
            rp = etree.Element('{%s}rPr' % NS['a'])
            run.insert(0, rp)
        sources = [rp] + [d.find('a:defRPr', NS) for d in defaults]
        for source in sources[1:]:
            if source is None:
                continue
            for key, value in source.attrib.items():
                if key not in rp.attrib:
                    rp.set(key, value)
        for script in ('latin', 'ea', 'cs'):
            target = rp.find('a:' + script, NS)
            source = next(
                (s.find(
                    'a:' + script,
                    NS) for s in sources if s is not None and s.find(
                    'a:' + script,
                    NS) is not None),
                None)
            value = source.get('typeface', '') if source is not None else ''
            if not value:
                value = '+mn-' + {'latin': 'lt', 'ea': 'ea', 'cs': 'cs'}[script]
            value = _font_name(value, theme, script)
            if target is None:
                target = etree.SubElement(rp, '{%s}%s' % (NS['a'], script))
            target.set('typeface', value)
            if value and not value.startswith('+'):
                families.add(value)
        # rPr font children precede hyperlinks and extension metadata.
        order_r = ['ln', 'noFill', 'solidFill', 'gradFill', 'blipFill', 'pattFill', 'grpFill', 'effectLst',
                   'effectDag', 'highlight', 'uLnTx', 'uLn', 'uFillTx', 'uFill', 'latin', 'ea', 'cs', 'sym',
                   'hlinkClick', 'hlinkMouseOver', 'rtl', 'extLst']
        rp[:] = sorted(rp, key=lambda n: order_r.index(etree.QName(n).localname)
                       if etree.QName(n).localname in order_r else len(order_r))
    return families


def font_availability(families):
    """Report availability without silently changing the template's font.

    Local font packages are live; the (larger) installed-font scan is cached.
    Availability on this server does not guarantee availability on the client.
    """
    from .font_library import local_fonts
    available = set(_installed_fonts())
    for record in local_fonts():
        available.update(n.casefold() for n in [record['family'], *record['aliases']])
    return {'available_locally': sorted(f for f in families if f.casefold() in available),
            'unavailable_locally': sorted(f for f in families if f.casefold() not in available),
            'client_availability': 'unknown', 'substitution_applied': False}


@lru_cache(maxsize=1)
def _installed_fonts():
    import os
    from pathlib import Path
    from fontTools.ttLib import TTFont, TTCollection, TTLibError
    roots = [Path('/System/Library/Fonts'), Path('/Library/Fonts'), Path.home() / 'Library/Fonts',
             Path('/usr/share/fonts'), Path('/usr/local/share/fonts'), Path.home() / '.local/share/fonts',
             Path('/Applications/Microsoft PowerPoint.app/Contents/Resources/DFonts')]
    if os.environ.get('WINDIR'):
        roots.append(Path(os.environ['WINDIR']) / 'Fonts')
    names = set()
    for root in roots:
        if not root.is_dir():
            continue
        for path in root.rglob('*'):
            if path.suffix.lower() not in ('.ttf', '.otf', '.ttc'):
                continue
            try:
                resource = TTCollection(path, lazy=True) if path.suffix.lower() == '.ttc' else TTFont(path, lazy=True)
                try:
                    for font in resource.fonts if isinstance(resource, TTCollection) else [resource]:
                        names.update(n.toUnicode().casefold() for n in font['name'].names if n.nameID in (1, 16))
                finally:
                    resource.close()
            except (OSError, ValueError, KeyError, TTLibError):
                continue
    return frozenset(names)


def split_paragraph(paragraph):
    """Promote line breaks to paragraphs, retaining styled runs and list level."""
    body = paragraph.getparent()
    cursor = paragraph
    for child in list(paragraph):
        if child.tag == '{%s}br' % NS['a']:
            following = etree.Element('{%s}p' % NS['a'])
            ppr = paragraph.find('a:pPr', NS)
            if ppr is not None:
                following.append(deepcopy(ppr))
            body.insert(body.index(cursor) + 1, following)
            paragraph.remove(child)
            cursor = following
        elif cursor is not paragraph and child.tag != '{%s}pPr' % NS['a']:
            cursor.append(child)
    return list(body)[list(body).index(paragraph):list(body).index(cursor) + 1]
