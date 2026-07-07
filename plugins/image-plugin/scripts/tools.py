"""Plugin-local tools for image-plugin.

Framework tools reused from Chat (declare in state.yml step tools):
  - multimodal        — vision_extractor VLM for user-uploaded images
  - web_search        — web retrieval
  - image_generator   — text-to-image (runtime_models image_generator role)
  - image_editor      — image-to-image editing (runtime_models image_editor role)

Always available on every plugin step (no declaration needed):
  - find_user_attachment / read_user_attachment — locate user uploads

image_search_tool searches the web for reference image URLs.
validate_image_ref probes URL/path accessibility without downloading the full file.
"""
from __future__ import annotations

from io import BytesIO
from pathlib import Path
from typing import Any, List, Tuple

import requests
from lazyllm.tools.tools.search import (
    BingSearch,
    BochaSearch,
    GoogleSearch,
    TavilySearch,
)

from lazymind.chat.service.utils.static_file_url import (
    local_path_from_static_file_url,
    resolve_local_image_path,
)

_SEARCH_ENGINES = [
    GoogleSearch(),
    BingSearch(),
    BochaSearch(),
    TavilySearch(),
]

_IMAGE_URL_KEYS = (
    'contentUrl', 'content_url', 'imageUrl', 'image_url',
    'thumbnailUrl', 'thumbnail_url', 'src', 'url',
)

_PROBE_BYTES = 8192
_PROBE_TIMEOUT = 20
_USER_AGENT = 'Mozilla/5.0 (compatible; LazyMind/1.0; image-probe)'


def _pick_search_engine():
    for engine in _SEARCH_ENGINES:
        try:
            if engine.__key_source__():
                return engine
        except Exception:
            continue
    return None


def _is_image_url(value: str) -> bool:
    lower = value.lower()
    if not (lower.startswith('http://') or lower.startswith('https://')):
        return False
    for ext in ('.jpg', '.jpeg', '.png', '.gif', '.webp', '.bmp', '.svg'):
        if ext in lower:
            return True
    return any(token in lower for token in ('image', 'img', 'photo', 'pic'))


def _collect_image_urls(node: Any, out: List[str], seen: set) -> None:
    if isinstance(node, dict):
        for key in _IMAGE_URL_KEYS:
            raw = node.get(key)
            if isinstance(raw, str) and _is_image_url(raw) and raw not in seen:
                seen.add(raw)
                out.append(raw)
        for value in node.values():
            _collect_image_urls(value, out, seen)
    elif isinstance(node, list):
        for item in node:
            _collect_image_urls(item, out, seen)


def _bocha_image_urls(query: str, count: int = 5) -> List[str]:
    engine = BochaSearch()
    if not engine.__key_source__():
        return []
    url = f'{engine._base_url}/v1/web-search'
    body = {'query': query, 'count': min(max(count, 1), 20)}
    try:
        resp = engine._request(
            'POST',
            url,
            headers={'Content-Type': 'application/json'},
            json=body,
            timeout=engine._timeout,
        )
        data = resp.json()
    except Exception:
        return []
    urls: List[str] = []
    _collect_image_urls(data, urls, set())
    return urls[:count]


def _tavily_image_urls(query: str, count: int = 5) -> List[str]:
    engine = TavilySearch()
    if not engine.__key_source__():
        return []
    try:
        results = engine.search(query, include_images=True, max_results=count)
    except Exception:
        return []
    urls: List[str] = []
    seen: set = set()
    for item in results or []:
        extra = item.get('extra') or {}
        images = extra.get('images') or []
        if isinstance(images, list):
            for img in images:
                if isinstance(img, str) and _is_image_url(img) and img not in seen:
                    seen.add(img)
                    urls.append(img)
    return urls[:count]


def _looks_like_image_bytes(data: bytes) -> bool:
    if len(data) < 12:
        return False
    if data[:8] == b'\x89PNG\r\n\x1a\n':
        return True
    if data[:2] == b'\xff\xd8':
        return True
    if data[:6] in (b'GIF87a', b'GIF89a'):
        return True
    if data[:4] == b'RIFF' and len(data) > 12 and data[8:12] == b'WEBP':
        return True
    if data[:2] == b'BM':
        return True
    return False


def _probe_image_dimensions(data: bytes) -> Tuple[int, int, str]:
    try:
        from PIL import Image
    except ImportError:
        return 0, 0, 'UNKNOWN'
    bio = BytesIO(data)
    with Image.open(bio) as img:
        fmt = str(img.format or 'UNKNOWN')
        return int(img.size[0]), int(img.size[1]), fmt


def _reject_content_type(content_type: str) -> None:
    ct = (content_type or '').split(';')[0].strip().lower()
    if not ct:
        return
    if ct.startswith('image/'):
        return
    if ct in ('text/html', 'application/json', 'text/plain', 'application/xml'):
        raise ValueError(f'not an image: content-type={ct}')


def _probe_remote_image(url: str) -> Tuple[str, int, int, str]:
    headers = {'User-Agent': _USER_AGENT}
    head = requests.head(
        url,
        headers=headers,
        timeout=_PROBE_TIMEOUT,
        allow_redirects=True,
    )
    if head.status_code >= 400 or head.status_code == 405:
        get_headers = {**headers, 'Range': f'bytes=0-{_PROBE_BYTES - 1}'}
        resp = requests.get(
            url,
            headers=get_headers,
            timeout=_PROBE_TIMEOUT,
            stream=True,
            allow_redirects=True,
        )
    else:
        head.raise_for_status()
        _reject_content_type(head.headers.get('Content-Type', ''))
        get_headers = {**headers, 'Range': f'bytes=0-{_PROBE_BYTES - 1}'}
        resp = requests.get(
            url,
            headers=get_headers,
            timeout=_PROBE_TIMEOUT,
            stream=True,
            allow_redirects=True,
        )
    resp.raise_for_status()
    _reject_content_type(resp.headers.get('Content-Type', ''))
    data = b''.join(resp.iter_content(1024))
    if not _looks_like_image_bytes(data):
        raise ValueError('response body is not a recognizable image')
    width, height, fmt = _probe_image_dimensions(data)
    return url, width, height, fmt


def _resolve_local_file(path: str) -> str:
    static_local = local_path_from_static_file_url(path)
    local = resolve_local_image_path(path)
    candidates = [static_local, local, path.split('?', 1)[0]]
    seen: set[str] = set()
    for candidate in candidates:
        key = (candidate or '').split('?', 1)[0]
        if not key or key in seen:
            continue
        seen.add(key)
        file_path = Path(key)
        if file_path.is_file():
            return str(file_path.resolve())
    raise ValueError(f'local image file not found: {path}')


def _probe_local_image(path: str) -> Tuple[str, int, int, str]:
    file_path = _resolve_local_file(path)
    with open(file_path, 'rb') as fh:
        data = fh.read(_PROBE_BYTES)
    if not _looks_like_image_bytes(data):
        raise ValueError('local file is not a recognizable image')
    width, height, fmt = _probe_image_dimensions(data)
    return file_path, width, height, fmt


def _format_result(ok: bool, **fields: Any) -> str:
    lines = [f'status: {"ok" if ok else "invalid"}']
    for key, value in fields.items():
        if value is not None and value != '':
            lines.append(f'{key}: {value}')
    return '\n'.join(lines)


def validate_image_ref(url: str) -> str:
    """Probe whether an image URL or path is accessible — no full download.

    Use BEFORE save_artifact. If status is ok, save the returned `url` field
    (http URL or local path). If invalid, skip — do NOT add to the frontend.

    Args:
        url (str): http(s) image URL, /static-files/ path, or local filesystem path.

    Returns:
        On success: status=ok, url, optional width/height/format.
        On failure: status=invalid, reason, url.
    """
    raw = str(url or '').strip()
    if not raw:
        return _format_result(False, reason='url is required')

    try:
        if raw.startswith('http://') or raw.startswith('https://'):
            ref, width, height, fmt = _probe_remote_image(raw)
            fields: dict[str, Any] = {'url': ref}
            if width and height:
                fields['width'] = width
                fields['height'] = height
            if fmt != 'UNKNOWN':
                fields['format'] = fmt
            return _format_result(True, **fields)

        ref, width, height, fmt = _probe_local_image(raw)
        fields = {'url': ref}
        if width and height:
            fields['width'] = width
            fields['height'] = height
        if fmt != 'UNKNOWN':
            fields['format'] = fmt
        return _format_result(True, **fields)
    except Exception as exc:
        return _format_result(False, reason=str(exc), url=raw)


def image_search_tool(query: str) -> str:
    """Search for reference images matching a visual concept.

    Tries Tavily (include_images) and Bocha image fields first, then falls back
    to a web search scoped for reference images.

    IMPORTANT: URLs are candidates only. Call validate_image_ref on each URL
    before save_artifact. Save only when status is ok (use the returned url).

    Args:
        query (str): A descriptive phrase for the type of reference image needed.

    Returns:
        A newline-separated list of image URLs.
    """
    urls = _tavily_image_urls(query, count=5)
    if not urls:
        urls = _bocha_image_urls(query, count=5)
    if not urls:
        engine = _pick_search_engine()
        if engine is not None:
            try:
                image_query = f'{query} reference image illustration'
                results = engine.search(image_query)
                for item in results or []:
                    candidate = str(item.get('url') or '').strip()
                    if _is_image_url(candidate):
                        urls.append(candidate)
                    if len(urls) >= 5:
                        break
            except Exception:
                pass
    if not urls:
        return (
            f'No image URLs found for "{query}". '
            'Configure Tavily or Bocha for better image results, or try a '
            'more specific query.'
        )
    return '\n'.join(urls[:5])
