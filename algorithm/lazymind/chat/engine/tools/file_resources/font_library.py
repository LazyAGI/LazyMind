"""Shared local font packages for PPT preview, replacement and export.

Administrator CLI: python font_library.py import /path/to/font.ttf
Set LAZYMIND_FONT_DIR identically in the algorithm and export services.
"""
from __future__ import annotations

import base64
import hashlib
import io
import json
import os
from pathlib import Path
import re
import uuid


# Deliberately no model-supplied URLs: these families are distributed under OFL.
DOWNLOAD_FONTS = {
    'Noto Sans SC': 'notosanssc/NotoSansSC%5Bwght%5D.ttf',
    'Noto Serif SC': 'notoserifsc/NotoSerifSC%5Bwght%5D.ttf',
}


def ensure_font(family: str, allow_download: bool = False) -> str:
    """Resolve an installed family, optionally acquire an allowlisted OFL font."""
    from .presentation_edit import available_font_families
    requested = str(family or '').strip()
    available = {name.casefold(): name for name in available_font_families()}
    if requested.casefold() in available:
        return available[requested.casefold()]
    canonical = next((name for name in DOWNLOAD_FONTS if name.casefold() == requested.casefold()), None)
    if not allow_download or canonical is None:
        raise ValueError(f'字体 {requested!r} 不可用。请提供合法字体文件或选择已安装字体。'
                         f'可按明确请求下载的开源字体：{", ".join(DOWNLOAD_FONTS)}。不会自动替换字体。')
    import tempfile
    import httpx
    relative = DOWNLOAD_FONTS[canonical]
    base = 'https://raw.githubusercontent.com/google/fonts/main/ofl/'
    with tempfile.TemporaryDirectory(prefix='lazymind-font-') as folder:
        source = Path(folder) / 'font.ttf'
        # Bound downloads; redirects to arbitrary hosts are deliberately disabled.
        with httpx.Client(timeout=60, follow_redirects=False) as client:
            license_response = client.get(base + relative.split('/')[0] + '/OFL.txt')
            license_response.raise_for_status()
            if 'SIL OPEN FONT LICENSE' not in license_response.text:
                raise ValueError('Font license could not be verified')
            with client.stream('GET', base + relative) as response:
                response.raise_for_status()
                size = 0
                with source.open('wb') as stream:
                    for chunk in response.iter_bytes():
                        size += len(chunk)
                        if size > 40 * 1024 * 1024:
                            raise ValueError('Font download exceeds 40 MiB')
                        stream.write(chunk)
        from fontTools.ttLib import TTFont
        with TTFont(source) as font:
            names = {n.toUnicode().casefold() for n in font['name'].names if n.nameID in {1, 16}}
            if canonical.casefold() not in names or int(getattr(font.get('OS/2'), 'fsType', 0)) & (2 | 512):
                raise ValueError('Downloaded font family or embedding permission mismatch')
        record = register_font(source)
        (font_library_root() / (record['id'] + '.OFL.txt')).write_text(license_response.text, encoding='utf-8')
    return canonical


def font_library_root() -> Path:
    # TODO: replace this local storage boundary with cloud object storage plus
    # a local cache. Keep content-hash font IDs and catalog metadata unchanged.
    return Path(os.environ.get('LAZYMIND_FONT_DIR')
                or Path.home() / '.local/share/lazymind/fonts').expanduser().resolve()


def register_font(source: str | Path) -> dict:
    from fontTools.ttLib import TTFont

    source = Path(source)
    if source.suffix.lower() not in {'.ttf', '.otf'}:
        raise ValueError('Local font packages support TTF and OTF')
    with TTFont(source, recalcTimestamp=False) as font:
        # Store a static default instance: variable fonts are not reliably
        # supported by PowerPoint's embedded font consumer.
        if 'fvar' in font:
            from fontTools.varLib.instancer import instantiateVariableFont
            font = instantiateVariableFont(font, {
                a.axisTag: min(a.maxValue, max(a.minValue, 400)) if a.axisTag == 'wght' else a.defaultValue
                for a in font['fvar'].axes
            })
        names = font['name']
        family = names.getDebugName(16) or names.getDebugName(1)
        if not family:
            raise ValueError('Font has no family name')
        aliases = sorted({n.toUnicode() for n in names.names if n.nameID in {1, 16}})
        os2 = font.get('OS/2')
        fs_type = int(getattr(os2, 'fsType', 0))
        output = io.BytesIO()
        font.save(output)
        data = output.getvalue()
        digest = hashlib.sha256(data).hexdigest()
        filename = digest + source.suffix.lower()
        record = {
            'id': digest, 'file': filename, 'family': family, 'aliases': aliases,
            'weight': int(getattr(os2, 'usWeightClass', 400)),
            'style': 'italic' if int(getattr(os2, 'fsSelection', 0)) & 1 else 'normal',
            'fs_type': fs_type, 'embeddable': not bool(fs_type & (2 | 512)),
            'license': names.getDebugName(13) or '',
            'license_url': names.getDebugName(14) or '', 'source_name': source.name,
        }
    root = font_library_root()
    root.mkdir(parents=True, exist_ok=True)
    for path, content in ((root / filename, data),
                          (root / f'{digest}.json', json.dumps(record, ensure_ascii=False, indent=2).encode())):
        temporary = root / f'.{uuid.uuid4().hex}.tmp'
        temporary.write_bytes(content)
        os.replace(temporary, path)
    return record


def local_fonts() -> list[dict]:
    records = []
    root = font_library_root()
    for path in sorted(root.glob('*.json')):
        try:
            record = json.loads(path.read_text(encoding='utf-8'))
            if (not re.fullmatch(r'[0-9a-f]{64}', record['id'])
                    or record['file'] not in {record['id'] + '.ttf', record['id'] + '.otf'}):
                continue
            data = (root / record['file']).read_bytes()
            if hashlib.sha256(data).hexdigest() == record['id']:
                records.append(record)
        except (OSError, ValueError, KeyError, TypeError):
            continue
    return records


def local_font_css(html: str) -> str:
    """Self-contained fonts for browser previews; no expiring cloud URLs."""
    blocks = []
    for record in local_fonts():
        if not record['embeddable']:
            continue
        aliases = [name for name in record['aliases'] if name.casefold() in html.casefold()]
        if not aliases:
            continue
        data = (font_library_root() / record['file']).read_bytes()
        encoded = base64.b64encode(data).decode()
        for family in aliases:
            quoted = json.dumps(family, ensure_ascii=False).replace('<', '\\3c ')
            blocks.append(f'@font-face{{font-family:{quoted};font-weight:{record["weight"]};'
                          f'font-style:{record["style"]};src:url(data:font/ttf;base64,{encoded})}}')
    return '\n'.join(blocks)


if __name__ == '__main__':
    import argparse
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['list', 'import'])
    parser.add_argument('paths', nargs='*')
    args = parser.parse_args()
    print(json.dumps([register_font(path) for path in args.paths] if args.action == 'import'
                     else local_fonts(), ensure_ascii=False, indent=2))
