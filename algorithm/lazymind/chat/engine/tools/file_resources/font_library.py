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


class FontUnavailableError(ValueError):
    """A requested family is unavailable; never silently substitute another."""
    error_code = 'FONT_UNAVAILABLE'


def ensure_font(family: str, allow_download: bool = False) -> str:
    """Resolve an installed family, optionally acquire an allowlisted OFL font."""
    from .presentation_edit import available_font_families
    requested = str(family or '').strip()
    available = {name.casefold(): name for name in available_font_families()}
    if requested.casefold() in available:
        return available[requested.casefold()]
    from lazymind.presentation_resources import font_families
    cloud_family = next((name for name in font_families() if name.casefold() == requested.casefold()), None)
    if allow_download and cloud_family:
        return _acquire_cloud_family(cloud_family)
    canonical = next((name for name in DOWNLOAD_FONTS if name.casefold() == requested.casefold()), None)
    if not allow_download or canonical is None:
        raise FontUnavailableError(f'字体 {requested!r} 不可用。请提供合法字体文件或选择已安装字体。'
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
        with TTFont(source) as font:
            weights = (400, 700) if 'fvar' in font and any(a.axisTag == 'wght' for a in font['fvar'].axes) else (400,)
        for weight in weights:
            record = register_font(source, weight=weight)
            (font_library_root() / (record['id'] + '.OFL.txt')).write_text(license_response.text, encoding='utf-8')
    return canonical


def _acquire_cloud_family(family):
    from lazymind.presentation_resources import ensure_resource
    root = ensure_resource('fonts')
    manifest = json.loads((root / 'manifest.json').read_text(encoding='utf-8'))
    entry = next(item for item in manifest['families'] if item['family'] == family)
    for item in entry['files']:
        source = (root / item['file']).resolve()
        if not source.is_relative_to(root) or hashlib.sha256(source.read_bytes()).hexdigest() != item['sha256']:
            raise ValueError('Cloud font checksum/path mismatch')
        weights = (400, 700) if item['variable'] else (item['weight'],)
        for weight in weights:
            record = register_font(source, weight=weight)
            (font_library_root() / (record['id'] + '.OFL.txt')).write_bytes((source.parent / 'OFL.txt').read_bytes())
    return family


def font_library_root() -> Path:
    # Derived static faces are runtime cache; original libraries live in cloud storage.
    return Path(os.environ.get('LAZYMIND_FONT_DIR')
                or Path.home() / '.local/share/lazymind/fonts').expanduser().resolve()


def register_font(source: str | Path, *, weight=400) -> dict:
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
                a.axisTag: min(a.maxValue, max(a.minValue, weight)) if a.axisTag == 'wght' else a.defaultValue
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


def font_resource_url(record: dict) -> str:
    """Publish immutable font bytes through the existing signed static transport."""
    from lazymind.chat.service.utils.static_file_url import _upload_root
    from .portable_fonts import font_bytes
    if (not re.fullmatch(r'[0-9a-f]{64}', record['id'])
            or record['file'] not in {record['id'] + '.ttf', record['id'] + '.otf'}):
        raise ValueError('Invalid font resource identity')
    data = font_bytes(record)
    root = Path(_upload_root()) / 'presentation-fonts'
    root.mkdir(parents=True, exist_ok=True)
    target = root / record['file']
    if not target.is_file() or hashlib.sha256(target.read_bytes()).hexdigest() != record['id']:
        temporary = root / f'.{uuid.uuid4().hex}.tmp'
        try:
            temporary.write_bytes(data)
            os.replace(temporary, target)
        finally:
            temporary.unlink(missing_ok=True)
    # Store no expiring signature. Slide previews sign this reference on opening.
    return f'/static-files/presentation-fonts/{record["file"]}'


def font_face_css(family: str, record: dict) -> str:
    metadata = {'family': family, 'weight': record['weight'], 'style': record['style'], 'sha256': record['id']}
    token = base64.b64encode(json.dumps(metadata, ensure_ascii=False).encode()).decode()
    quoted = json.dumps(family, ensure_ascii=False).replace('<', '\\3c ')
    return (f'/*lazymind-font:{token}*/@font-face{{font-family:{quoted};'
            f'font-weight:{record["weight"]};font-style:{record["style"]};'
            f'src:url({font_resource_url(record)})}}')


def upgrade_legacy_html_fonts(html: str) -> str:
    """Replace our old inline subsets only when the exact original is available."""
    if 'data:font/' not in html:
        return html
    records = {record['id']: record for record in local_fonts() if record.get('embeddable')}
    # Match the historical serializer exactly; authored descriptors stay intact.
    rule = re.compile(r'/\*lazymind-font:([A-Za-z0-9+/=]+)\*/@font-face\{'
                      r'font-family:("(?:\\.|[^"\\])*");font-weight:([0-9]+);font-style:(normal|italic);'
                      r'src:url\(data:font/(?:ttf|woff);base64,[A-Za-z0-9+/=]+\)\}')

    def replace_face(match):
        try:
            metadata = json.loads(base64.b64decode(match.group(1), validate=True))
            if not isinstance(metadata, dict) or not isinstance(metadata.get('family'), str):
                return match.group(0)
            if (json.loads(match.group(2)) != metadata['family']
                    or int(match.group(3)) != metadata.get('weight') or match.group(4) != metadata.get('style')):
                return match.group(0)
            record = records.get(metadata.get('sha256'))
            if (not record or metadata.get('weight') != record['weight']
                    or metadata.get('style') != record['style']
                    or metadata['family'].casefold() not in {alias.casefold() for alias in record['aliases']}):
                return match.group(0)
            return font_face_css(metadata['family'], record)
        except (OSError, ValueError, KeyError, TypeError):
            # Legacy documents remain editable when their original font cache is
            # absent. Never substitute a different version or download a font.
            return match.group(0)

    return re.sub(r'<style\b[^>]*\bdata-lazymind-font-required=(["\'])true\1[^>]*>[\s\S]*?</style>',
                  lambda match: rule.sub(replace_face, match.group(0)), html, flags=re.I)


def local_font_css(html: str) -> str:
    """Reference shared font resources without adding font bytes to preview HTML."""
    blocks = []
    packaged = set()
    packaged_families = set()
    for token in re.findall(r'/\*lazymind-font:([A-Za-z0-9+/=]+)\*/', html):
        try:
            metadata = json.loads(base64.b64decode(token))
            packaged.add(metadata['sha256'])
            packaged_families.add(metadata['family'].casefold())
        except (ValueError, KeyError, TypeError):
            continue
    for record in local_fonts():
        if (not record['embeddable'] or record['id'] in packaged
                or packaged_families.intersection(name.casefold() for name in record['aliases'])):
            continue
        aliases = [name for name in record['aliases'] if name.casefold() in html.casefold()]
        if not aliases:
            continue
        for family in aliases:
            blocks.append(font_face_css(family, record))
    return '\n'.join(blocks)


if __name__ == '__main__':
    import argparse
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['list', 'import'])
    parser.add_argument('paths', nargs='*')
    args = parser.parse_args()
    print(json.dumps([register_font(path) for path in args.paths] if args.action == 'import'
                     else local_fonts(), ensure_ascii=False, indent=2))
