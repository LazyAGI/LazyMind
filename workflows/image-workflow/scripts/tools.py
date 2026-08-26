"""Workflow-local tools for image-workflow.

Framework tools reused from Chat (declare in state.yml step tools):
  - multimodal        — vision_extractor VLM for user-uploaded images
  - web_search        — web retrieval
  - image_generator   — text-to-image (runtime_models image_generator role)
  - image_editor      — image-to-image editing (runtime_models image_editor role)

Always available on every workflow step (no declaration needed):
  - find_user_attachment / read_user_attachment — locate user uploads

image_search_and_validate searches and validates web image URLs in one call.
image_search_tool remains available for compatibility and returns candidates only.
validate_image_ref probes URL/path accessibility without downloading the full file.
"""
from __future__ import annotations

from concurrent.futures import ThreadPoolExecutor, as_completed
from io import BytesIO
import json
import logging
import os
from pathlib import Path
from typing import Any, List, Optional, Tuple
import unicodedata
import uuid
from urllib.parse import urlparse

import requests
from lazyllm.tools.tools.search import (
    BingSearch,
    BochaSearch,
    GoogleSearch,
    TavilySearch,
)

from lazymind.chat.service.utils.static_file_url import (
    _upload_root,
    local_path_from_static_file_url,
    resolve_local_image_path,
    static_file_url_from_any,
)

LOG = logging.getLogger(__name__)

_SEARCH_ENGINES = [
    GoogleSearch(),
    BingSearch(),
    BochaSearch(),
    TavilySearch(),
]

_PROBE_BYTES = 8192
_MAX_PROBE_BYTES = 2 * 1024 * 1024
_PROBE_TIMEOUT = 20
_VALIDATION_WORKERS = 6
_USER_AGENT = 'Mozilla/5.0 (compatible; LazyMind/1.0; image-probe)'

_DIRECT_IMAGE_EXTENSIONS = frozenset({'.jpg', '.jpeg', '.png', '.gif', '.webp', '.bmp'})

_DEFAULT_CAPTION_BOX = (0.15, 0.75, 0.85, 0.93)
_CJK_FONT_CANDIDATES = (
    '/usr/share/fonts/opentype/noto/NotoSansCJK-Bold.ttc',
    '/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc',
    '/usr/share/fonts/truetype/wqy/wqy-zenhei.ttc',
    '/usr/share/fonts/truetype/arphic/uming.ttc',
)
_LATIN_FONT_CANDIDATES = (
    '/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf',
    '/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf',
)


def _pick_search_engine():
    for engine in _SEARCH_ENGINES:
        try:
            if engine.__key_source__():
                return engine
        except Exception:
            continue
    return None


def _is_http_url(value: str) -> bool:
    return str(value or '').strip().lower().startswith(('http://', 'https://'))


def _is_image_url(value: str) -> bool:
    raw = str(value or '').strip()
    if not _is_http_url(raw):
        return False
    try:
        suffix = Path(urlparse(raw).path).suffix.lower()
    except ValueError:
        return False
    # Search result pages frequently contain words such as ``photo`` or
    # ``image`` but return HTML. Only accept URL paths that actually identify a
    # supported raster file; signed query parameters are deliberately ignored
    # for classification and preserved in the returned URL.
    return suffix in _DIRECT_IMAGE_EXTENSIONS


def _collect_image_urls(
    node: Any,
    out: List[str],
    seen: set,
    image_context: bool = False,
) -> None:
    if isinstance(node, str):
        candidate = node.strip()
        if (
            _is_http_url(candidate)
            and (image_context or _is_image_url(candidate))
            and candidate not in seen
        ):
            seen.add(candidate)
            out.append(candidate)
    elif isinstance(node, dict):
        for key, value in node.items():
            normalized_key = str(key).replace('_', '').lower()
            child_image_context = image_context or (
                'image' in normalized_key
                or 'thumbnail' in normalized_key
                or normalized_key in {'contenturl', 'src'}
            )
            _collect_image_urls(value, out, seen, child_image_context)
    elif isinstance(node, list):
        for item in node:
            _collect_image_urls(item, out, seen, image_context)


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
    except Exception as exc:
        LOG.warning('Bocha image search failed: %s', type(exc).__name__)
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
    except Exception as exc:
        LOG.warning('Tavily image search failed: %s', type(exc).__name__)
        return []
    urls: List[str] = []
    seen: set = set()
    for item in results or []:
        extra = item.get('extra') or {}
        images = extra.get('images') or []
        if isinstance(images, list):
            for img in images:
                if isinstance(img, str) and _is_http_url(img) and img not in seen:
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
    get_headers = {**headers, 'Range': f'bytes=0-{_MAX_PROBE_BYTES - 1}'}
    resp = requests.get(
        url,
        headers=get_headers,
        timeout=_PROBE_TIMEOUT,
        stream=True,
        allow_redirects=True,
    )
    if resp.status_code == 416:
        resp.close()
        resp = requests.get(
            url,
            headers=headers,
            timeout=_PROBE_TIMEOUT,
            stream=True,
            allow_redirects=True,
        )
    try:
        resp.raise_for_status()
        _reject_content_type(resp.headers.get('Content-Type', ''))
        data = bytearray()
        width, height, fmt = 0, 0, 'UNKNOWN'
        for chunk in resp.iter_content(16 * 1024):
            if not chunk:
                continue
            remaining = _MAX_PROBE_BYTES - len(data)
            if remaining <= 0:
                break
            data.extend(chunk[:remaining])
            if len(data) >= _PROBE_BYTES and _looks_like_image_bytes(data):
                try:
                    width, height, fmt = _probe_image_dimensions(bytes(data))
                    break
                except Exception:
                    # Some JPEGs carry large metadata blocks before the frame
                    # header. Continue incrementally instead of treating the
                    # first 8 KiB as a definitive truncated-file failure.
                    pass
            if len(data) >= _MAX_PROBE_BYTES:
                break
        if not _looks_like_image_bytes(data):
            raise ValueError('response body is not a recognizable image')
        if not width or not height:
            width, height, fmt = _probe_image_dimensions(bytes(data))
        return str(resp.url or url), width, height, fmt
    finally:
        resp.close()


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


def _validate_image_candidate(url: str) -> dict[str, Any]:
    raw = str(url or '').strip()
    if not raw:
        return {'status': 'invalid', 'url': raw, 'reason': 'url is required'}
    try:
        if raw.startswith(('http://', 'https://')):
            ref, width, height, fmt = _probe_remote_image(raw)
        else:
            ref, width, height, fmt = _probe_local_image(raw)
        result: dict[str, Any] = {
            'status': 'ok',
            'original_url': raw,
            'url': ref,
        }
        if width and height:
            result['width'] = width
            result['height'] = height
        if fmt != 'UNKNOWN':
            result['format'] = fmt
        return result
    except Exception as exc:
        return {
            'status': 'invalid',
            'original_url': raw,
            'url': raw,
            'reason': str(exc),
        }


def validate_image_ref(url: str) -> str:
    """Probe whether an image URL or path is accessible — no full download.

    Use BEFORE save_artifacts. If status is ok, save the returned `url` field
    (http URL or local path). If invalid, skip — do NOT add to the frontend.

    Args:
        url (str): http(s) image URL, /static-files/ path, or local filesystem path.

    Returns:
        On success: status=ok, url, optional width/height/format.
        On failure: status=invalid, reason, url.
    """
    result = _validate_image_candidate(url)
    if result['status'] != 'ok':
        return _format_result(
            False,
            reason=result.get('reason'),
            url=result.get('original_url') or result.get('url'),
        )
    return _format_result(
        True,
        url=result.get('url'),
        width=result.get('width'),
        height=result.get('height'),
        format=result.get('format'),
    )


def _append_unique_urls(
    target: List[str],
    values: List[str],
    limit: int,
    require_direct_path: bool = False,
) -> None:
    seen = set(target)
    for value in values:
        candidate = str(value or '').strip()
        if (
            not candidate
            or candidate in seen
            or not _is_http_url(candidate)
            or (require_direct_path and not _is_image_url(candidate))
        ):
            continue
        seen.add(candidate)
        target.append(candidate)
        if len(target) >= limit:
            return


def _normalize_candidate_urls(candidate_urls: Any, limit: int) -> List[str]:
    """Normalize model-provided image candidates without changing URL bytes."""
    raw = candidate_urls
    if isinstance(raw, str):
        text = raw.strip()
        if not text:
            return []
        try:
            parsed = json.loads(text)
        except (TypeError, ValueError, json.JSONDecodeError):
            parsed = [line.strip() for line in text.splitlines() if line.strip()]
        raw = parsed

    discovered: List[str] = []
    if isinstance(raw, list) and all(isinstance(item, str) for item in raw):
        # A plain list is the explicit tool contract. Keep every HTTP candidate
        # so HTML/error responses appear in the returned diagnostics instead of
        # silently disappearing before validation.
        discovered = [item.strip() for item in raw]
    else:
        # For a complete web-search payload, only descend into image-labelled
        # fields. A normal result's generic ``url`` remains excluded unless its
        # path is itself a direct raster filename.
        _collect_image_urls(raw, discovered, set())
    urls: List[str] = []
    _append_unique_urls(urls, discovered, limit)
    return urls


def _search_image_candidates(query: str, limit: int) -> List[str]:
    urls: List[str] = []
    _append_unique_urls(urls, _tavily_image_urls(query, count=limit), limit)
    if len(urls) < limit:
        _append_unique_urls(urls, _bocha_image_urls(query, count=limit - len(urls)), limit)
    if len(urls) < min(3, limit):
        engine = _pick_search_engine()
        if engine is not None:
            try:
                image_query = f'{query} reference image illustration'
                results = engine.search(image_query)
                fallback = [str(item.get('url') or '') for item in (results or [])]
                _append_unique_urls(urls, fallback, limit, require_direct_path=True)
            except Exception as exc:
                LOG.warning('Fallback image search failed: %s', type(exc).__name__)
    return urls[:limit]


def _candidate_quality_key(item: dict[str, Any]) -> tuple[int, int, int]:
    width = int(item.get('width') or 0)
    height = int(item.get('height') or 0)
    search_rank = int(item.get('search_rank') or 0)
    # Search rank is the relevance signal. Resolution only separates equally
    # relevant usable candidates, and tiny thumbnails always rank last.
    usable_resolution = int(min(width, height) >= 512)
    return usable_resolution, -search_rank, width * height


def image_search_and_validate(
    query: str,
    target_valid: int = 3,
    max_candidates: int = 15,
    candidate_urls: Optional[List[str]] = None,
) -> dict[str, Any]:
    """Search image URLs and validate the exact candidates in one deterministic call.

    Pass the direct image URLs from ``web_search(include_images=True)`` through
    ``candidate_urls``. This tool preserves signed query parameters, rejects HTML
    result pages, probes every candidate concurrently, and returns factual counters
    for material reports. If no candidates are provided, it tries configured local
    search providers as a compatibility fallback. Use ``selected[*].url`` verbatim.

    Args:
        query (str): Descriptive image-search query including subject and desired composition.
        target_valid (int): Number of usable images to select, from 1 to 5. Default 3.
        max_candidates (int): Maximum candidates to search and validate, from 1 to 30.
        candidate_urls (list[str]): Exact direct-image URLs returned in web-search
            image fields. Preserve query/signature parameters; do not pass result-page URLs.

    Returns:
        Structured search statistics, every candidate and validation reason, plus
        up to target_valid quality-ranked entries in ``selected``.
    """
    normalized_query = str(query or '').strip()
    if not normalized_query:
        return {
            'status': 'empty',
            'query': normalized_query,
            'candidate_count': 0,
            'validated_count': 0,
            'valid_count': 0,
            'invalid_count': 0,
            'selected_count': 0,
            'selected': [],
            'candidates': [],
            'reason': 'query is required',
        }
    try:
        target = min(max(int(target_valid), 1), 5)
    except (TypeError, ValueError):
        target = 3
    try:
        limit = min(max(int(max_candidates), 1), 30)
    except (TypeError, ValueError):
        limit = 15

    urls = _normalize_candidate_urls(candidate_urls, limit)
    candidate_source = 'provided' if urls else 'local_search'
    if not urls:
        urls = _search_image_candidates(normalized_query, limit)
    LOG.info('Image search candidates query=%r urls=%s', normalized_query, urls)
    if not urls:
        return {
            'status': 'empty',
            'query': normalized_query,
            'candidate_count': 0,
            'validated_count': 0,
            'valid_count': 0,
            'invalid_count': 0,
            'selected_count': 0,
            'selected': [],
            'candidates': [],
            'reason': 'no direct image URLs found',
            'candidate_source': candidate_source,
        }

    validations: list[dict[str, Any] | None] = [None] * len(urls)
    workers = min(_VALIDATION_WORKERS, len(urls))
    with ThreadPoolExecutor(max_workers=workers) as executor:
        futures = {
            executor.submit(_validate_image_candidate, url): index
            for index, url in enumerate(urls)
        }
        for future in as_completed(futures):
            index = futures[future]
            try:
                validations[index] = future.result()
            except Exception as exc:
                validations[index] = {
                    'status': 'invalid',
                    'original_url': urls[index],
                    'url': urls[index],
                    'reason': str(exc),
                }

    candidates = [item for item in validations if isinstance(item, dict)]
    for index, item in enumerate(candidates, start=1):
        item['search_rank'] = index
    valid = [item for item in candidates if item.get('status') == 'ok']
    ranked = sorted(
        valid,
        key=lambda item: (
            _candidate_quality_key(item),
        ),
        reverse=True,
    )
    selected = ranked[:target]
    status = 'ok' if len(selected) >= target else ('partial' if selected else 'empty')
    result = {
        'status': status,
        'query': normalized_query,
        'candidate_source': candidate_source,
        'target_valid': target,
        'candidate_count': len(urls),
        'validated_count': len(candidates),
        'valid_count': len(valid),
        'invalid_count': len(candidates) - len(valid),
        'selected_count': len(selected),
        'selected': selected,
        'candidates': candidates,
    }
    LOG.info('Image search validation result=%s', result)
    return result


def image_search_tool(query: str) -> str:
    """Search for reference images matching a visual concept.

    Tries Tavily (include_images) and Bocha image fields first, then falls back
    to a web search scoped for reference images.

    IMPORTANT: URLs are candidates only. Call validate_image_ref on each URL
    before save_artifacts. Save only when status is ok (use the returned url).

    Args:
        query (str): A descriptive phrase for the type of reference image needed.

    Returns:
        A newline-separated list of image URLs.
    """
    urls = _search_image_candidates(query, 5)
    if not urls:
        return f'No image URLs found for "{query}". Try a more specific query.'
    return '\n'.join(urls[:5])


def _contains_cjk(text: str) -> bool:
    return any(
        '\u2e80' <= char <= '\u9fff'
        or '\uf900' <= char <= '\ufaff'
        or '\u3040' <= char <= '\u30ff'
        or '\uac00' <= char <= '\ud7af'
        for char in text
    )


def _font_missing_characters(font_path: str, text: str) -> List[str]:
    """Return visible characters that Pillow would render with the .notdef box."""
    from PIL import ImageFont

    font = ImageFont.truetype(font_path, size=48)

    def glyph_signature(char: str) -> tuple[Any, Any, bytes]:
        mask = font.getmask(char, mode='L')
        return font.getbbox(char), getattr(mask, 'size', None), bytes(mask)

    # U+0378 and U+0380 are permanently unassigned Unicode code points. FreeType
    # renders them with the font's .notdef glyph, which is the same square that
    # users otherwise see for unsupported Chinese characters.
    missing_signatures = {glyph_signature('\u0378'), glyph_signature('\u0380')}
    missing: List[str] = []
    seen: set[str] = set()
    for char in text:
        if char in seen or char.isspace() or unicodedata.category(char).startswith('C'):
            continue
        seen.add(char)
        if glyph_signature(char) in missing_signatures:
            missing.append(char)
    return missing


def _caption_font_path(caption: str) -> str:
    candidates = list(_CJK_FONT_CANDIDATES if _contains_cjk(caption) else _LATIN_FONT_CANDIDATES)

    attempted: List[str] = []
    seen_paths: set[str] = set()
    for candidate in candidates:
        path = Path(candidate).expanduser()
        normalized = str(path.resolve()) if path.is_file() else str(path)
        if normalized in seen_paths:
            continue
        seen_paths.add(normalized)
        if not path.is_file():
            attempted.append(f'{candidate} (missing)')
            continue
        try:
            missing = _font_missing_characters(str(path), caption)
        except (OSError, ValueError) as exc:
            attempted.append(f'{candidate} (unreadable: {exc})')
            continue
        if not missing:
            return normalized
        attempted.append(f'{candidate} (missing glyphs: {"".join(missing)})')

    attempted_summary = '; '.join(attempted)
    if _contains_cjk(caption):
        raise RuntimeError(
            'CJK caption font is unavailable or does not cover every caption character; '
            'install fonts-noto-cjk or set LAZYMIND_MEME_FONT_PATH to a Chinese-capable '
            f'.ttf/.ttc font. Tried: {attempted_summary}'
        )
    raise RuntimeError(
        'caption font is unavailable or does not cover every caption character; '
        f'install a suitable TrueType/OpenType font. Tried: {attempted_summary}'
    )


def _normalize_caption_box(caption_box: List[float] | None) -> Tuple[float, float, float, float]:
    raw = list(caption_box or _DEFAULT_CAPTION_BOX)
    if len(raw) != 4:
        raise ValueError('caption_box must contain [left, top, right, bottom]')
    try:
        left, top, right, bottom = (float(value) for value in raw)
    except (TypeError, ValueError) as exc:
        raise ValueError('caption_box values must be numbers') from exc
    if not (0 <= left < right <= 1 and 0 <= top < bottom <= 1):
        raise ValueError('caption_box values must satisfy 0 <= left < right <= 1 and 0 <= top < bottom <= 1')
    return left, top, right, bottom


def _caption_text_bbox(draw: Any, text: str, font: Any, stroke_width: int, spacing: int) -> Tuple[int, int, int, int]:
    return draw.multiline_textbbox(
        (0, 0),
        text,
        font=font,
        spacing=spacing,
        align='center',
        stroke_width=stroke_width,
    )


def _wrap_caption(draw: Any, caption: str, font: Any, max_width: int, stroke_width: int) -> str:
    paragraphs = caption.splitlines() or [caption]
    lines: List[str] = []
    for paragraph in paragraphs:
        if not paragraph:
            lines.append('')
            continue
        current = ''
        for char in paragraph:
            candidate = current + char
            bounds = draw.textbbox((0, 0), candidate, font=font, stroke_width=stroke_width)
            width = bounds[2] - bounds[0]
            if current and width > max_width:
                lines.append(current.rstrip())
                current = char.lstrip() if char.isspace() else char
            else:
                current = candidate
        lines.append(current.rstrip())
    return '\n'.join(lines)


def _caption_layout(
    image_size: Tuple[int, int],
    caption: str,
    caption_box: List[float] | None = None,
    font_path: str = '',
    stroke_width_ratio: float = 0.08,
) -> dict[str, Any]:
    from PIL import Image, ImageDraw, ImageFont

    if not str(caption or '').strip():
        raise ValueError('caption is required')
    try:
        normalized_stroke_ratio = float(stroke_width_ratio)
    except (TypeError, ValueError) as exc:
        raise ValueError('stroke_width_ratio must be a number') from exc
    if not 0.01 <= normalized_stroke_ratio <= 0.25:
        raise ValueError('stroke_width_ratio must be between 0.01 and 0.25')
    width, height = image_size
    left_n, top_n, right_n, bottom_n = _normalize_caption_box(caption_box)
    box = (
        round(width * left_n),
        round(height * top_n),
        round(width * right_n),
        round(height * bottom_n),
    )
    box_width = box[2] - box[0]
    box_height = box[3] - box[1]
    padding = max(2, round(min(box_width, box_height) * 0.05))
    usable_width = max(1, box_width - 2 * padding)
    usable_height = max(1, box_height - 2 * padding)
    selected_font_path = (
        _caption_font_path(caption)
        if not font_path
        else str(Path(font_path).expanduser().resolve())
    )
    missing = _font_missing_characters(selected_font_path, caption)
    if missing:
        raise RuntimeError(
            f'caption font does not cover required characters: {"".join(missing)}'
        )
    measure = ImageDraw.Draw(Image.new('RGBA', (width, height)))

    best: dict[str, Any] | None = None
    low = 6
    high = max(low, min(usable_height, round(height * 0.25)))
    while low <= high:
        size = (low + high) // 2
        font = ImageFont.truetype(selected_font_path, size=size)
        stroke_width = max(1, round(size * normalized_stroke_ratio))
        spacing = max(1, round(size * 0.18))
        wrapped = _wrap_caption(measure, caption.strip(), font, usable_width, stroke_width)
        bounds = _caption_text_bbox(measure, wrapped, font, stroke_width, spacing)
        text_width = bounds[2] - bounds[0]
        text_height = bounds[3] - bounds[1]
        if text_width <= usable_width and text_height <= usable_height:
            best = {
                'font': font,
                'font_size': size,
                'stroke_width': stroke_width,
                'spacing': spacing,
                'text': wrapped,
                'bounds': bounds,
                'text_size': (text_width, text_height),
            }
            low = size + 1
        else:
            high = size - 1
    if best is None:
        raise ValueError('caption is too long to fit inside caption_box')

    bounds = best['bounds']
    text_width, text_height = best['text_size']
    x = box[0] + (box_width - text_width) / 2 - bounds[0]
    y = box[1] + (box_height - text_height) / 2 - bounds[1]
    best.update({
        'caption_box_px': box,
        'position': (round(x, 2), round(y, 2)),
        'text_bbox_px': (
            round(x + bounds[0], 2),
            round(y + bounds[1], 2),
            round(x + bounds[2], 2),
            round(y + bounds[3], 2),
        ),
        'font_path': selected_font_path,
        'stroke_width_ratio': normalized_stroke_ratio,
    })
    return best


def _render_caption_frame(
    frame: Any,
    caption: str,
    caption_box: List[float] | None,
    text_color: str,
    stroke_color: str,
    stroke_width_ratio: float,
) -> Tuple[Any, dict[str, Any]]:
    from PIL import ImageColor, ImageDraw

    rendered = frame.convert('RGBA')
    layout = _caption_layout(
        rendered.size,
        caption,
        caption_box,
        '',
        stroke_width_ratio,
    )
    draw = ImageDraw.Draw(rendered)
    draw.multiline_text(
        layout['position'],
        layout['text'],
        font=layout['font'],
        fill=ImageColor.getcolor(text_color, 'RGBA'),
        stroke_width=layout['stroke_width'],
        stroke_fill=ImageColor.getcolor(stroke_color, 'RGBA'),
        spacing=layout['spacing'],
        align='center',
    )
    return rendered, layout


def _open_caption_source(image_url: str) -> Any:
    from PIL import Image

    raw = str(image_url or '').strip()
    if not raw:
        raise ValueError('image_url is required')
    if raw.startswith(('http://', 'https://')):
        response = requests.get(raw, headers={'User-Agent': _USER_AGENT}, timeout=60)
        response.raise_for_status()
        return Image.open(BytesIO(response.content))
    return Image.open(_resolve_local_file(raw))


def meme_add_caption(
    image_url: str,
    caption: str,
    caption_box: List[float] | None = None,
    text_color: str = '#FFFFFF',
    stroke_color: str = '#000000',
    stroke_width_ratio: float = 0.08,
) -> dict[str, Any]:
    """Deterministically center a caption inside a normalized rectangle on a meme.

    This is a meme-only postprocessor. Call it after image_generator/image_editor
    for a static meme, or after video_to_gif for an animated meme. The default
    caption rectangle is [0.15, 0.75, 0.85, 0.93], matching a centered lower
    banner. The rectangle is used only for layout calculation and is not drawn.
    Font size and line wrapping are calculated to fit; the result is centered
    horizontally and vertically. Chinese captions require a CJK-capable font.

    Args:
        image_url: Local path, signed /static-files/ URL, or HTTP(S) image/GIF.
        caption: Exact text to render. Do not translate or rewrite it.
        caption_box: Optional [left, top, right, bottom] values normalized to 0..1.
        text_color: Pillow-compatible text color, default white.
        stroke_color: Pillow-compatible outline color, default black.
        stroke_width_ratio: Outline width divided by font size, from 0.01 to 0.25.

    Returns:
        Result containing local_path, signed image_url, calculated font size,
        pixel caption box, text bounding box, and final wrapped text.
    """
    from PIL import Image

    source = _open_caption_source(image_url)
    output_dir = Path(_upload_root()).resolve() / 'ai_generated'
    output_dir.mkdir(parents=True, exist_ok=True)
    animated = bool(getattr(source, 'is_animated', False) and getattr(source, 'n_frames', 1) > 1)
    suffix = '.gif' if animated else '.png'
    output_path = output_dir / f'{uuid.uuid4().hex}_captioned{suffix}'
    layout: dict[str, Any] | None = None

    if animated:
        frames = []
        durations = []
        for index in range(source.n_frames):
            source.seek(index)
            frame, current_layout = _render_caption_frame(
                source.copy(), caption, caption_box, text_color, stroke_color,
                stroke_width_ratio,
            )
            if layout is None:
                layout = current_layout
            frames.append(frame.convert('P', palette=Image.Palette.ADAPTIVE))
            durations.append(int(source.info.get('duration') or 100))
        frames[0].save(
            output_path,
            format='GIF',
            save_all=True,
            append_images=frames[1:],
            duration=durations,
            loop=int(source.info.get('loop') or 0),
            disposal=2,
            optimize=False,
        )
    else:
        rendered, layout = _render_caption_frame(
            source, caption, caption_box, text_color, stroke_color,
            stroke_width_ratio,
        )
        rendered.save(output_path, format='PNG')
    source.close()

    if layout is None or not output_path.is_file() or output_path.stat().st_size == 0:
        raise RuntimeError('caption postprocessing did not produce a valid output')
    signed_url = static_file_url_from_any(str(output_path))
    result = {
        'success': True,
        'local_path': str(output_path),
        'caption': caption,
        'caption_box': list(_normalize_caption_box(caption_box)),
        'caption_box_px': list(layout['caption_box_px']),
        'text_bbox_px': list(layout['text_bbox_px']),
        'font_size': layout['font_size'],
        'text_color': text_color,
        'stroke_color': stroke_color,
        'stroke_width': layout['stroke_width'],
        'stroke_width_ratio': layout['stroke_width_ratio'],
        'wrapped_text': layout['text'],
        'animated': animated,
    }
    if signed_url:
        result['image_url'] = signed_url
        result['image_markdown'] = f'![captioned meme]({signed_url})'
    return result
