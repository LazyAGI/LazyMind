"""Subset embedded EOT fonts to the text in a finished PPTX, atomically."""
import io
import os
from pathlib import Path
import struct
import sys
import tempfile
import xml.etree.ElementTree as ET
from zipfile import ZipFile, ZIP_DEFLATED

from fontTools import subset
from fontTools.ttLib import TTFont


def subset_pptx(path):
    path = Path(path)
    with ZipFile(path) as archive:
        data = {name: archive.read(name) for name in archive.namelist()}
    text = ''.join(node.text or '' for name, raw in data.items()
                   if name.startswith(('ppt/slides/', 'ppt/notesSlides/', 'ppt/charts/',
                                       'ppt/slideMasters/', 'ppt/slideLayouts/', 'ppt/notesMasters/'))
                   and name.endswith('.xml')
                   for node in ET.fromstring(raw).iter()
                   if node.tag.rsplit('}', 1)[-1] in {'t', 'v'})
    if not text:
        return
    for name, raw in list(data.items()):
        if not name.startswith('ppt/fonts/') or not name.endswith('.fntdata'):
            continue
        if len(raw) < 82 or struct.unpack_from('<I', raw, 8)[0] != 0x10000:
            raise ValueError('Unsupported embedded font format; cannot subset safely')
        font_size = struct.unpack_from('<I', raw, 4)[0]
        header_size = len(raw) - font_size
        with TTFont(io.BytesIO(raw[header_size:])) as font:
            if font['OS/2'].fsType & 256:
                continue  # The font license prohibits subsetting.
            options = subset.Options()
            options.layout_features = ['*']
            subsetter = subset.Subsetter(options=options)
            subsetter.populate(text=text)
            subsetter.subset(font)
            output = io.BytesIO()
            font.save(output)
            payload = output.getvalue()
        header = bytearray(raw[:header_size])
        struct.pack_into('<II', header, 0, len(header) + len(payload), len(payload))
        with TTFont(io.BytesIO(payload)) as font:
            struct.pack_into('<I', header, 60, font['head'].checkSumAdjustment)
        data[name] = bytes(header) + payload
    # Keep existing namespace prefixes and all unrelated XML unchanged.
    import re
    xml = data['ppt/presentation.xml'].decode('utf-8')
    xml = re.sub(r'\s+saveSubsetFonts="[^"]*"', '', xml)
    xml = re.sub(r'(<(?:\w+:)?presentation)\b', r'\1 saveSubsetFonts="1"', xml, count=1)
    data['ppt/presentation.xml'] = xml.encode('utf-8')
    handle, temporary = tempfile.mkstemp(dir=path.parent, suffix='.pptx')
    os.close(handle)
    try:
        with ZipFile(temporary, 'w', ZIP_DEFLATED) as archive:
            for name, raw in data.items():
                archive.writestr(name, raw)
        os.replace(temporary, path)
    finally:
        Path(temporary).unlink(missing_ok=True)


if __name__ == '__main__':
    subset_pptx(sys.argv[1])
