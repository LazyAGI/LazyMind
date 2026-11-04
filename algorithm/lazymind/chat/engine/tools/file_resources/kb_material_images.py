"""Read image references from authorized stored chunks, without the RAG runtime."""
from __future__ import annotations

import json
import re
from html import unescape
from pathlib import Path
from urllib.parse import quote, urlsplit

from lazymind.chat.engine.tools.infra.core_api_client import get_core_api
from lazymind.chat.service.utils.static_file_url import _upload_root, local_path_from_static_file_url

_IMAGE_SUFFIXES = {'.png', '.jpg', '.jpeg', '.webp', '.gif', '.bmp', '.tif', '.tiff'}
_MARKDOWN_IMAGE = re.compile(r'!\[([^\]\n]*)\]\(\s*(?:<([^>\n]+)>|([^\n]+?))\s*\)')
_HTML_IMAGE = re.compile(r'<img\b[^>]*\bsrc\s*=\s*[\"\']([^\"\']+)[\"\']', re.I)


def _metadata(segment: dict) -> dict:
    value = segment.get('metadata') or segment.get('meta') or {}
    if isinstance(value, str):
        try:
            value = json.loads(value)
        except ValueError:
            return {}
    return value if isinstance(value, dict) else {}


def _references(segment: dict, metadata: dict):
    for obj in (metadata, segment):
        for key in ('normalized_source_path', 'source_path', 'image_uri', 'image_url', 'image_key'):
            if isinstance(obj.get(key), str):
                yield obj[key], str(metadata.get('caption') or '')
        for key in ('image_keys', 'images'):
            items = obj.get(key) or []
            if not isinstance(items, list):
                items = [items]
            for item in items:
                if isinstance(item, str):
                    yield item, ''
                elif isinstance(item, dict):
                    yield (item.get('local_path') or item.get('url') or item.get('path') or ''), str(
                        item.get('caption') or item.get('alt') or '')
    for key in ('text', 'content', 'display_content'):
        text = segment.get(key)
        if not isinstance(text, str):
            continue
        # Base ingestion keeps image nodes as paths, including paths with spaces.
        for line in text.splitlines():
            yield line.strip(), ''
        for match in _MARKDOWN_IMAGE.finditer(text):
            ref = match[2] or re.sub(r'\s+[\"\'][^\n]*[\"\']$', '', match[3])
            yield ref, match[1]
        for match in _HTML_IMAGE.finditer(text):
            yield unescape(match[1]), ''


def _image_reference(raw: str) -> tuple[str, str] | None:
    if not isinstance(raw, str) or not raw or len(raw) > 8192:
        return None
    ref = unescape(raw.strip())
    try:
        parsed = urlsplit(ref)
        if Path(parsed.path).suffix.lower() not in _IMAGE_SUFFIXES:
            return None
        if parsed.scheme in ('http', 'https') and '/static-files/' not in parsed.path:
            if parsed.hostname and not parsed.username and not parsed.password and not any(c.isspace() for c in ref):
                return 'url', ref
            return None
        if parsed.scheme and parsed.scheme not in ('http', 'https'):
            return None
        local = local_path_from_static_file_url(parsed.path if parsed.path.startswith('/static-files/') else ref)
        if not local:
            return None
        path = Path(local).resolve()
        path.relative_to(Path(_upload_root()).resolve())
        if path.is_file() and 0 < path.stat().st_size <= 12 * 1024 * 1024:
            return 'local_path', str(path)
    except (ValueError, OSError):
        pass
    return None


def collect_document_images(knowledge_base_id: str, document_id: str,
                            page_token: str = '', page_size: int = 50) -> dict:
    """Read one bounded page through Core's document ACL and base-chunk fallback."""
    for value in (knowledge_base_id, document_id):
        if not isinstance(value, str) or not value.strip() or len(value) > 256:
            raise ValueError('knowledge_base_id and document_id must be nonempty resource IDs')
    if type(page_size) is not int or not 1 <= page_size <= 100:
        raise ValueError('page_size must be an integer between 1 and 100')
    if not isinstance(page_token, str) or len(page_token) > 2048:
        raise ValueError('invalid page_token')
    path = f'/datasets/{quote(knowledge_base_id, safe="")}/documents/{quote(document_id, safe="")}'
    document = get_core_api(path)
    page = get_core_api(path + '/segments', params={'page_size': page_size, 'page_token': page_token})
    segments = page.get('segments') or []
    if not isinstance(segments, list):
        raise ValueError('Core returned an invalid segment page')
    next_token = str(page.get('next_page_token') or '')
    if next_token and next_token == page_token:
        raise ValueError('Core returned a repeated page token')
    filename = str(document.get('display_name') or document.get('file_name') or document.get('name') or document_id)
    images, seen = [], set()
    for segment in segments:
        if not isinstance(segment, dict) or segment.get('enabled') is False or segment.get('is_active') is False:
            continue
        metadata = _metadata(segment)
        provenance = {'knowledge_base_id': knowledge_base_id, 'document_id': document_id, 'file_name': filename,
                      'chunk_id': segment.get('segment_id') or segment.get('id'), 'chunk_number': segment.get('number')}
        for key in ('page', 'page_number', 'bbox'):
            if key in metadata:
                provenance[key] = metadata[key]
        for ref, caption in _references(segment, metadata):
            candidate = _image_reference(ref)
            if candidate is None or candidate in seen:
                continue
            seen.add(candidate)
            key, value = candidate
            images.append({key: value, 'caption': caption[:1000] or filename, 'alt': caption[:1000] or filename,
                           'source': f'kb:{knowledge_base_id}/{document_id}', 'kb_provenance': provenance})
    return {'images': images, 'count': len(images), 'scanned_chunks': len(segments),
            'knowledge_base_id': knowledge_base_id, 'document_id': document_id,
            'next_page_token': next_token, 'has_more': bool(next_token),
            'total_chunks': page.get('total_size', len(segments)),
            'note': 'Only stored image references are returned. Empty pages may have a next_page_token; '
                    'missing files are skipped. Register selected images for visual inspection before use.'}
