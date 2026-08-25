"""Workflow-local tools for image-workflow.

Framework tools reused from Chat (declare in state.yml step tools):
  - multimodal        — vision_extractor VLM for user-uploaded images
  - web_search        — web retrieval
  - image_generator   — text-to-image (runtime_models image_generator role)
  - image_editor      — image-to-image editing (runtime_models image_editor role)

Always available on every workflow step (no declaration needed):
  - find_user_attachment / read_user_attachment — locate user uploads

image_search_tool searches the web for reference image URLs.
validate_image_ref probes URL/path accessibility without downloading the full file.
"""
from __future__ import annotations

from io import BytesIO
import logging
import os
from pathlib import Path
from typing import Any, List, Tuple
import uuid

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

_IMAGE_URL_KEYS = (
    'contentUrl', 'content_url', 'imageUrl', 'image_url',
    'thumbnailUrl', 'thumbnail_url', 'src', 'url',
)

_PROBE_BYTES = 8192
_PROBE_TIMEOUT = 20
_USER_AGENT = 'Mozilla/5.0 (compatible; LazyMind/1.0; image-probe)'

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

    Use BEFORE save_artifacts. If status is ok, save the returned `url` field
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
    before save_artifacts. Save only when status is ok (use the returned url).

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


def _caption_font_path(caption: str, requested: str = '') -> str:
    configured = str(requested or os.getenv('LAZYMIND_MEME_FONT_PATH') or '').strip()
    if configured:
        path = Path(configured).expanduser()
        if not path.is_file():
            raise ValueError(f'caption font file not found: {configured}')
        return str(path.resolve())

    candidates = _CJK_FONT_CANDIDATES if _contains_cjk(caption) else (
        *_CJK_FONT_CANDIDATES,
        *_LATIN_FONT_CANDIDATES,
    )
    for candidate in candidates:
        if Path(candidate).is_file():
            return candidate
    if _contains_cjk(caption):
        raise RuntimeError(
            'CJK caption font is unavailable; install fonts-noto-cjk or set '
            'LAZYMIND_MEME_FONT_PATH to a Chinese-capable .ttf/.ttc font'
        )
    raise RuntimeError('caption font is unavailable; install a TrueType/OpenType font')


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
    selected_font_path = _caption_font_path(caption, font_path)
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
    font_path: str,
    stroke_width_ratio: float,
) -> Tuple[Any, dict[str, Any]]:
    from PIL import ImageColor, ImageDraw

    rendered = frame.convert('RGBA')
    layout = _caption_layout(
        rendered.size,
        caption,
        caption_box,
        font_path,
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
    font_path: str = '',
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
        font_path: Optional explicit .ttf/.ttc/.otf font path.

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
                source.copy(), caption, caption_box, text_color, stroke_color, font_path,
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
            source, caption, caption_box, text_color, stroke_color, font_path,
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
