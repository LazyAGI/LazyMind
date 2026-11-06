"""Font resources shared by sparse HTML edits and native PPTX edits.

Font names alone are never a portable delivery. Resolve actual licensed bytes
before changing a document, and keep those bytes with the document afterwards.
"""
import base64
import hashlib
import io
import json
import os
from html.parser import HTMLParser
from pathlib import Path
import re
import shutil
import subprocess
import unicodedata

from fontTools.ttLib import TTFont

from .font_library import ensure_font, font_library_root, local_fonts, register_font


def editable_embedding(fs_type):
    return not (fs_type & (2 | 512)) and (not (fs_type & 4) or bool(fs_type & 8))


def font_bytes(record):
    data = (font_library_root() / record['file']).read_bytes()
    if hashlib.sha256(data).hexdigest() != record['id']:
        raise ValueError('Font resource changed; import the font again')
    with TTFont(io.BytesIO(data)) as font:
        if not editable_embedding(int(getattr(font.get('OS/2'), 'fsType', 0))):
            raise ValueError(f'字体 {record["family"]!r} 不允许嵌入可编辑文档；请选择其他字体')
    return data


def _system_font_paths():
    fc_list = shutil.which('fc-list')
    if fc_list:
        try:
            result = subprocess.run([fc_list, '--format=%{file}\n'], capture_output=True,
                                    text=True, check=True, timeout=15)
            yield from (Path(p) for p in sorted(set(result.stdout.splitlines())))
        except (OSError, subprocess.SubprocessError):
            pass
    for root in (Path('/System/Library/Fonts'), Path('/Library/Fonts'), Path.home() / 'Library/Fonts',
                 Path('/usr/share/fonts'), Path('/usr/local/share/fonts'),
                 Path(os.environ.get('WINDIR', 'C:/Windows')) / 'Fonts'):
        if root.is_dir():
            yield from root.rglob('*')


def portable_font_faces(family, allow_download=False):
    family = ensure_font(family, allow_download)
    records = [r for r in local_fonts() if family.casefold() in {a.casefold() for a in r['aliases']}]
    if not records:
        # Resolve by actual name-table entries, never by filename or a fallback.
        for source in dict.fromkeys(_system_font_paths()):
            if source.suffix.lower() not in {'.ttf', '.otf', '.ttc'}:
                continue
            try:
                from fontTools.ttLib import TTCollection
                collection = TTCollection(source) if source.suffix.lower() == '.ttc' else None
                fonts = collection.fonts if collection else [TTFont(source)]
                try:
                    for font in fonts:
                        aliases = {n.toUnicode().casefold() for n in font['name'].names if n.nameID in {1, 16}}
                        if family.casefold() not in aliases:
                            continue
                        if not editable_embedding(int(getattr(font.get('OS/2'), 'fsType', 0))):
                            continue
                        if collection:
                            import tempfile
                            with tempfile.TemporaryDirectory(prefix='font-import-') as folder:
                                extracted = Path(folder) / 'face.ttf'
                                font.save(extracted)
                                records.append(register_font(extracted))
                        else:
                            records.append(register_font(source))
                finally:
                    for font in fonts:
                        font.close()
            except (OSError, ValueError, KeyError):
                continue
    if not records:
        raise ValueError(f'字体 {family!r} 没有可嵌入的字体文件；请导入允许嵌入的 TTF/OTF，或下载支持的开源字体')
    for record in records:
        font_bytes(record)
    # PowerPoint has four face slots. Resolve intermediate weights consistently
    # before packaging so preview and export use the same bytes per slot.
    variants = {}
    for record in sorted(records, key=lambda r: (abs(r['weight'] - (700 if r['weight'] >= 600 else 400)), r['id'])):
        variants.setdefault((record['style'], record['weight'] >= 600), record)
    return family, list(variants.values())


def check_glyphs(records, text):
    supported = None
    for record in records:
        with TTFont(io.BytesIO(font_bytes(record))) as font:
            glyphs = set((font.getBestCmap() or {}).keys())
            supported = glyphs if supported is None else supported & glyphs
    missing = sorted({c for c in text if not c.isspace() and not unicodedata.category(c).startswith('C')
                      and ord(c) not in (supported or set())})
    if missing:
        raise ValueError('目标字体缺少所选文字的字形：' + ''.join(missing[:24]) + '；未修改文档')


class _PageText(HTMLParser):
    def __init__(self):
        super().__init__(convert_charrefs=True)
        self.hidden = 0
        self.text = []

    def handle_starttag(self, tag, attrs):
        if tag in {'script', 'style'}:
            self.hidden += 1

    def handle_endtag(self, tag):
        if tag in {'script', 'style'}:
            self.hidden = max(0, self.hidden - 1)

    def handle_data(self, data):
        if not self.hidden:
            self.text.append(data)


def _html_font_bytes(record, text):
    from fontTools import subset
    with TTFont(io.BytesIO(font_bytes(record))) as font:
        # OS/2 bit 8 prohibits subsetting. Preserve that restriction.
        if not int(getattr(font.get('OS/2'), 'fsType', 0)) & 256:
            options = subset.Options()
            options.layout_features = ['*']
            subsetter = subset.Subsetter(options=options)
            subsetter.populate(text=text)
            subsetter.subset(font)
        font.flavor = 'woff'
        output = io.BytesIO()
        font.save(output)
        return output.getvalue()


def package_html_fonts(html, family, records, text=''):
    """Embed page glyphs for HTML; retain full font identity for editable PPTX export."""
    check_glyphs(records, text)
    # One package per family: repeated edits refresh bytes without duplicating
    # resources or dropping earlier edits to another scope/family.
    key = hashlib.sha256(family.casefold().encode()).hexdigest()[:24]
    pattern = rf'<style\b[^>]*\bid=["\']lazymind-portable-font-{key}["\'][^>]*>[\s\S]*?</style>'
    html = re.sub(pattern, '', html, flags=re.I)
    page = _PageText()
    page.feed(html)
    glyph_text = ''.join(page.text) + text
    blocks = []
    for record in records:
        metadata = {'family': family, 'weight': record['weight'], 'style': record['style'], 'sha256': record['id']}
        token = base64.b64encode(json.dumps(metadata, ensure_ascii=False).encode()).decode()
        data = base64.b64encode(_html_font_bytes(record, glyph_text)).decode()
        quoted = json.dumps(family, ensure_ascii=False).replace('<', '\\3c ')
        blocks.append(f'/*lazymind-font:{token}*/@font-face{{font-family:{quoted};'
                      f'font-weight:{record["weight"]};font-style:{record["style"]};'
                      f'src:url(data:font/woff;base64,{data})}}')
    block = (f'<style id="lazymind-portable-font-{key}" data-lazymind-font-required="true">'
             + ''.join(blocks) + '</style>')
    closing = re.search(r'</head\s*>', html, re.I)
    if closing:
        return html[:closing.start()] + block + html[closing.start():]
    opening = re.search(r'<html\b[^>]*>', html, re.I)
    if opening:
        return html[:opening.end()] + '<head>' + block + '</head>' + html[opening.end():]
    return block + html
