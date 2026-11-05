"""Embed full font files into an existing package without rebuilding slides.

EOT 1.0 follows https://www.w3.org/submissions/EOT/ . Only the font resources,
presentation font list, relationships and content types are changed here.
"""
import io
import posixpath
import struct
import os
from pathlib import Path
import tempfile
from zipfile import ZipFile, ZIP_DEFLATED

from fontTools.ttLib import TTFont
from lxml import etree

from .portable_fonts import font_bytes

P = 'http://schemas.openxmlformats.org/presentationml/2006/main'
R = 'http://schemas.openxmlformats.org/officeDocument/2006/relationships'
PKG = 'http://schemas.openxmlformats.org/package/2006/relationships'
CT = 'http://schemas.openxmlformats.org/package/2006/content-types'


def _xml(raw):
    return etree.fromstring(raw, etree.XMLParser(resolve_entities=False, no_network=True))


def _dump(node):
    return etree.tostring(node, xml_declaration=True, encoding='UTF-8', standalone=True)


def eot_data(record):
    raw = font_bytes(record)
    with TTFont(io.BytesIO(raw)) as font:
        os2 = font['OS/2']
        header = bytearray(82)
        struct.pack_into('<III', header, 4, len(raw), 0x10000, 0)
        panose = os2.panose
        header[16:26] = bytes(getattr(panose, attr) for attr in (
            'bFamilyType', 'bSerifStyle', 'bWeight', 'bProportion', 'bContrast',
            'bStrokeVariation', 'bArmStyle', 'bLetterForm', 'bMidline', 'bXHeight'))
        codepages = getattr(os2, 'ulCodePageRange1', 0)
        charset = next((value for bit, value in ((18, 134), (17, 128), (20, 136), (19, 129))
                        if codepages & (1 << bit)), 1)
        struct.pack_into('<BBIHH', header, 26, charset, os2.fsSelection & 1,
                         os2.usWeightClass, os2.fsType, 0x504C)
        ranges = [getattr(os2, f'ulUnicodeRange{i}', 0) for i in range(1, 5)]
        ranges += [codepages, getattr(os2, 'ulCodePageRange2', 0), font['head'].checkSumAdjustment]
        struct.pack_into('<7I', header, 36, *ranges)
        chunks = [header]
        for index, name_id in enumerate((1, 2, 5, 4)):
            value = ((font['name'].getDebugName(name_id) or '') + '\0').encode('utf-16-le')
            chunks.extend([struct.pack('<H', len(value)), value])
            if index < 3:
                chunks.append(b'\0\0')
        chunks.append(raw)
        result = bytearray(b''.join(chunks))
        struct.pack_into('<I', result, 0, len(result))
        return bytes(result)


def embed_font_records(data, family, records):
    """Update a package mapping in memory; caller publishes only after success."""
    presentation = _xml(data['ppt/presentation.xml'])
    relationships = _xml(data['ppt/_rels/presentation.xml.rels'])
    content_types = _xml(data['[Content_Types].xml'])
    font_list = presentation.find(f'{{{P}}}embeddedFontLst')
    if font_list is None:
        font_list = etree.Element(f'{{{P}}}embeddedFontLst')
        later = {'custShowLst', 'photoAlbum', 'custDataLst', 'kinsoku', 'defaultTextStyle', 'modifyVerifier', 'extLst'}
        index = next((i for i, c in enumerate(presentation) if etree.QName(c).localname in later), len(presentation))
        presentation.insert(index, font_list)
    entry = next((e for e in font_list if e.find(f'{{{P}}}font') is not None
                  and e.find(f'{{{P}}}font').get('typeface', '').casefold() == family.casefold()), None)
    if entry is None:
        entry = etree.SubElement(font_list, f'{{{P}}}embeddedFont')
        etree.SubElement(entry, f'{{{P}}}font', typeface=family)
    variants = {}
    for record in sorted(records, key=lambda r: (abs(r['weight'] - (700 if r['weight'] >= 600 else 400)), r['id'])):
        variant = ('boldItalic' if record['weight'] >= 600 else 'italic') if record['style'] == 'italic' else (
            'bold' if record['weight'] >= 600 else 'regular')
        variants.setdefault(variant, record)
    ids = {r.get('Id') for r in relationships}
    report = []
    for variant, record in variants.items():
        part = f'ppt/fonts/lazymind-{record["id"]}.fntdata'
        payload = eot_data(record)
        if part in data and data[part] != payload:
            raise ValueError('Conflicting embedded font resource')
        data[part] = payload
        relation = next((r for r in relationships if r.get('Type') == R + '/font'
                         and r.get('TargetMode') != 'External'
                         and posixpath.normpath(posixpath.join('ppt', r.get('Target', ''))) == part), None)
        if relation is None:
            index = 1
            while f'rId{index}' in ids:
                index += 1
            rid = f'rId{index}'
            ids.add(rid)
            relation = etree.SubElement(relationships, f'{{{PKG}}}Relationship',
                                        Id=rid, Type=R + '/font', Target=part.removeprefix('ppt/'))
        node = entry.find(f'{{{P}}}{variant}')
        if node is None:
            node = etree.SubElement(entry, f'{{{P}}}{variant}')
        node.set(f'{{{R}}}id', relation.get('Id'))
        report.append({'family': family, 'variant': variant, 'sha256': record['id']})
    order = ['font', 'regular', 'bold', 'italic', 'boldItalic']
    entry[:] = sorted(entry, key=lambda n: order.index(etree.QName(n).localname))
    presentation.set('embedTrueTypeFonts', '1')
    presentation.set('saveSubsetFonts', '0')
    if not any(n.get('Extension') == 'fntdata' for n in content_types):
        etree.SubElement(content_types, f'{{{CT}}}Default', Extension='fntdata', ContentType='application/x-fontdata')
    data['ppt/presentation.xml'] = _dump(presentation)
    data['ppt/_rels/presentation.xml.rels'] = _dump(relationships)
    data['[Content_Types].xml'] = _dump(content_types)
    # Check the serialized relationships, not merely the number of attempted writes.
    rels = {r.get('Id'): r for r in _xml(data['ppt/_rels/presentation.xml.rels'])}
    for variant, record in variants.items():
        rid = entry.find(f'{{{P}}}{variant}').get(f'{{{R}}}id')
        target = posixpath.normpath(posixpath.join('ppt', rels[rid].get('Target')))
        if data.get(target) != eot_data(record):
            raise ValueError('Embedded font validation failed')
    return report


def embed_pptx_file(path, family, records):
    """Add verified fonts to a legacy attachment edit before its publication."""
    path = Path(path)
    with ZipFile(path) as archive:
        infos = archive.infolist()
        data = {i.filename: archive.read(i) for i in infos}
    report = embed_font_records(data, family, records)
    handle, temporary = tempfile.mkstemp(dir=path.parent, suffix='.pptx')
    os.close(handle)
    try:
        with ZipFile(temporary, 'w', compression=ZIP_DEFLATED) as archive:
            existing = {i.filename for i in infos}
            for info in infos:
                archive.writestr(info, data[info.filename])
            for name in data.keys() - existing:
                archive.writestr(name, data[name])
        os.replace(temporary, path)
    finally:
        Path(temporary).unlink(missing_ok=True)
    return report
