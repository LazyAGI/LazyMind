"""Named, offline presentation assets shared by HTML/PDF and native PPTX.

Only the cloud-hosted, checksummed catalog is accepted. Icons are visual aids, not
document evidence, and never consume the one-evidence-image slot.
"""
from copy import deepcopy
from functools import lru_cache
from hashlib import sha256
from html import escape
from html.parser import HTMLParser
import json
from pathlib import Path
import re
import tempfile


def asset_root():
    from lazymind.presentation_resources import ensure_resource
    return ensure_resource('assets')


@lru_cache(maxsize=1)
def catalog():
    return json.loads((asset_root() / 'catalog.json').read_text(encoding='utf-8'))


def search(query='', collection='', limit=12):
    if type(limit) is not int or not 1 <= limit <= 40:
        raise ValueError('limit must be 1–40')
    data = catalog()
    if collection and collection not in data['sources']:
        raise ValueError('Unknown collection; use one of ' + ', '.join(data['sources']))
    query = str(query).casefold().strip()
    words = re.findall(r'[a-z0-9-]+|[\u4e00-\u9fff]+', query)
    matches = []
    for entry in data['assets']:
        if collection and entry['collection'] != collection:
            continue
        terms = [entry['name'], *entry['tags']]
        score = sum(3 if term.casefold() in query else 1 if any(w in term.casefold() for w in words) else 0
                    for term in terms)
        if query and not score:
            continue
        source = data['sources'][entry['collection']]
        matches.append((score, {'name': entry['name'], 'style': entry['style'], 'tags': entry['tags'],
                                'width': entry['width'], 'height': entry['height'], 'license': source['license']}))
    matches.sort(key=lambda item: (-item[0], item[1]['name']))
    return {'collections': {key: {'name': v['name'], 'style': v['style'], 'license': v['license']}
                            for key, v in data['sources'].items()},
            'total': len(matches), 'assets': [entry for _, entry in matches[:limit]]}


def resolve(name):
    if not isinstance(name, str) or not re.fullmatch(r'[a-z0-9-]+:[a-z0-9-]+', name):
        raise ValueError('Asset name must be collection:icon-name; paths and URLs are not accepted')
    entry = next((a for a in catalog()['assets'] if a['name'] == name), None)
    if entry is None:
        raise ValueError(f'Unknown presentation asset {name!r}; use ppt_search_assets for exact names')
    return entry


def _verified_file(entry, kind):
    path = (asset_root() / entry[kind]).resolve()
    if not path.is_relative_to(asset_root().resolve()):
        raise ValueError('Asset must stay inside the bundled library')
    data = path.read_bytes()
    if sha256(data).hexdigest() != entry[kind + '_sha256']:
        raise ValueError('Bundled asset checksum mismatch: ' + entry['name'])
    return path, data


def _write(path, data):
    path.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.NamedTemporaryFile(dir=path.parent, delete=False) as stream:
        stream.write(data)
        temp = Path(stream.name)
    try:
        temp.replace(path)
    finally:
        temp.unlink(missing_ok=True)


def stage(name, directory, kind='html'):
    entry = resolve(name)
    kind = 'svg' if kind == 'html' and entry.get('svg') else 'png'
    path, raw = _verified_file(entry, kind)
    directory = Path(directory).resolve()
    target = (directory / ('asset-' + path.name)).resolve()
    if target.parent != directory:
        raise ValueError('Asset destination escapes the selected directory')
    _write(target, raw)
    # Retain every bundled notice deterministically, including offline exports.
    notices = []
    for source in catalog()['sources'].values():
        license_path = asset_root() / source['license_file']
        notices.append(source['name'] + '\n' + source['source_url'] + '\n' + license_path.read_text(encoding='utf-8'))
    _write(directory / 'ASSET-LICENSES.txt', '\n\n'.join(notices).encode())
    source = catalog()['sources'][entry['collection']]
    return {'name': name, 'path': str(target), 'width': entry['width'], 'height': entry['height'],
            'sha256': sha256(raw).hexdigest(), 'source': source['source_url'], 'license': source['license'],
            'license_path': str(directory / 'ASSET-LICENSES.txt')}


class _ImageTag(HTMLParser):
    def handle_starttag(self, tag, attrs):
        self.attrs = dict(attrs)


def resolve_html(html, deck):
    """Resolve explicit named placeholders to offline files, without changing other images."""
    used = []

    def replace(match):
        tag = match.group(0)
        if 'data-ppt-asset' not in tag.lower():
            return tag
        parser = _ImageTag()
        parser.feed(tag)
        attrs = parser.attrs
        name = attrs.get('data-ppt-asset')
        if name is None:
            return tag
        if len(used) >= 16:
            raise ValueError('At most 16 named icons per page; simplify repeated decoration')
        asset = stage(name, Path(deck) / 'images')
        attrs['src'] = '../images/' + Path(asset['path']).name
        attrs.setdefault('alt', '')
        attrs['data-ppt-kind'] = 'icon'
        style = re.sub(r'(?:^|;)\s*(?:object-fit|aspect-ratio)\s*:[^;]*', '', attrs.get('style') or '', flags=re.I)
        attrs['style'] = style.rstrip(';') + ';object-fit:contain;aspect-ratio:auto;'
        used.append(name)
        return '<img ' + ' '.join(f'{k}="{escape(v or "", quote=True)}"' for k, v in attrs.items()) + '>'

    return re.sub(r'<img\b(?:[^>"\']|"[^"]*"|\'[^\']*\')*>', replace, html, flags=re.I), used


def prompt_catalog(query):
    preferred = ('fluent-3d' if any(word in query.casefold() for word in ('3d', '立体')) else
                 'fluent-color' if any(word in query for word in ('彩色', '质感', '渐变')) else '')
    entries = search(query, preferred, limit=24)['assets']
    if not entries:
        entries = search('需求 素材 大纲 图片 编辑 方法 图表', 'fluent-color', limit=12)['assets']
    return '\nNAMED PRESENTATION ASSETS (offline; optional, choose one consistent family):\n' + json.dumps(
        [{'name': e['name'], 'style': e['style'], 'meaning': ' '.join(e['tags'])} for e in entries], ensure_ascii=False
    ) + '\nInsert <img data-ppt-asset="exact-name" alt="short label">; keep object-fit:contain. '


def native_pages(pages, assets, directory):
    """Translate named icons to the existing aspect-preserving native image path."""
    pages = deepcopy(pages)
    assets = dict(assets)
    for page in pages or []:
        icons = page.pop('icons', [])
        if not isinstance(icons, list) or len(icons) > 8:
            raise ValueError('icons must be a list of at most 8 {name,box} placements per page')
        for icon in icons:
            if not isinstance(icon, dict):
                raise ValueError('Each icon requires name and box')
            entry = stage(icon.get('name'), directory, 'png')
            index = -1 - len(assets)
            while index in assets:
                index -= 1
            assets[index] = entry
            page.setdefault('images', []).append({'material_index': index, 'box': icon.get('box')})
    return pages, assets
