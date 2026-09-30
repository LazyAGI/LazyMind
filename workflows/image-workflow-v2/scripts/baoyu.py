from __future__ import annotations

import json
import math
from pathlib import Path
import re
from typing import Any, Dict, List, Optional, Union

import lazyllm
from lazyllm.tools.agent import ToolExecutionError
from lazyllm.tools.tool_config_inject import effective_env_value
from PIL import Image

from lazymind.chat.engine.tools.infra.image_generation_support import (
    _resolve_source_image_paths,
    run_image_model_instance,
)

_BAOYU_ADAPTER = 'baoyu-image-gen'
_BAOYU_PROVIDER = 'seedream'
_BAOYU_SOURCE = 'doubao'
_BAOYU_MODEL = 'doubao-seedream-5-0-260128'
_TARGET_PIXELS = 2048 * 2048
_MIN_DIMENSION = 512
_MAX_DIMENSION = 4096
_DIMENSION_MULTIPLE = 8

_ASPECT_RATIO_SIZES = {
    '1:1': '2048x2048',
    # Seedream 5.0 requires more than 3,686,400 pixels. 2560x1440 is exactly
    # that boundary and is rejected by the Ark API, so use the next exact 16:9
    # dimensions that are also multiples of eight.
    '16:9': '2688x1512',
    '9:16': '1512x2688',
    '4:3': '2304x1728',
    '3:4': '1728x2304',
    '3:2': '2496x1664',
    '2:3': '1664x2496',
    '21:9': '3024x1296',
}

_SUPPORTED_RATIO_RE = re.compile(
    r'(?<!\d)(21\s*[:：]\s*9|16\s*[:：]\s*9|9\s*[:：]\s*16|'
    r'4\s*[:：]\s*3|3\s*[:：]\s*4|3\s*[:：]\s*2|2\s*[:：]\s*3|'
    r'1\s*[:：]\s*1)(?!\d)',
    re.IGNORECASE,
)


def _coerce_url_list(urls: Optional[Union[str, List[str]]]) -> List[str]:
    if urls is None:
        return []
    if isinstance(urls, list):
        return [str(item).strip() for item in urls if str(item or '').strip()]
    text = str(urls).strip()
    if not text:
        return []
    if text.startswith('['):
        try:
            parsed = json.loads(text)
        except (TypeError, ValueError, json.JSONDecodeError):
            parsed = None
        if isinstance(parsed, list):
            return [str(item).strip() for item in parsed if str(item or '').strip()]
    return [text]


def _seedream_api_key() -> str:
    key = effective_env_value('ARK_API_KEY').strip()
    if not key:
        try:
            key = str(lazyllm.config['doubao_api_key'] or '').strip()
        except (KeyError, TypeError):
            key = ''
    if not key:
        raise ToolExecutionError.with_missing_env(
            'Baoyu Seedream image generation requires ARK_API_KEY.',
            ['ARK_API_KEY'],
        )
    return key


def _seedream_model(*, editing: bool) -> Any:
    return lazyllm.OnlineMultiModalModule(
        source=_BAOYU_SOURCE,
        model=_BAOYU_MODEL,
        type='image_editing' if editing else 'text2image',
        api_key=_seedream_api_key(),
    )


def _size_for_aspect_ratio(aspect_ratio: str) -> str:
    ratio = str(aspect_ratio or '').strip()
    try:
        return _ASPECT_RATIO_SIZES[ratio]
    except KeyError as exc:
        supported = ', '.join(_ASPECT_RATIO_SIZES)
        raise ToolExecutionError(
            f'Unsupported aspect_ratio {ratio!r}. Supported values: {supported}.'
        ) from exc


def _round_dimension(value: float) -> int:
    rounded = int(round(value / _DIMENSION_MULTIPLE) * _DIMENSION_MULTIPLE)
    return max(_MIN_DIMENSION, min(_MAX_DIMENSION, rounded))


def _size_for_source_image(source_path: str) -> str:
    try:
        with Image.open(Path(source_path)) as image:
            width, height = image.size
    except (OSError, ValueError) as exc:
        raise ToolExecutionError(
            f'Cannot inspect source image dimensions: {source_path}'
        ) from exc
    if width < 1 or height < 1:
        raise ToolExecutionError('Source image has invalid dimensions.')
    # Use one scale for both dimensions. Independent clamps change composition.
    lower = max(_MIN_DIMENSION / width, _MIN_DIMENSION / height)
    upper = min(_MAX_DIMENSION / width, _MAX_DIMENSION / height)
    scale = min(math.sqrt(_TARGET_PIXELS / (width * height)), upper)
    if scale < lower or (width * scale) * (height * scale) <= 3_686_400:
        raise ToolExecutionError(
            'Cannot preserve this source aspect ratio within Seedream size limits. '
            'Choose an explicit supported aspect ratio before editing.'
        )
    out_width, out_height = _round_dimension(width * scale), _round_dimension(height * scale)
    if out_width * out_height <= 3_686_400 or abs((out_width / out_height) / (width / height) - 1) > 0.01:
        raise ToolExecutionError('Cannot preserve source aspect ratio; choose an explicit supported ratio.')
    return f'{out_width}x{out_height}'


def _run_seedream(
    prompt: str,
    *,
    files: Optional[List[str]],
    image_size: str,
) -> Dict[str, Any]:
    model = _seedream_model(editing=bool(files))
    return run_image_model_instance(
        model,
        prompt,
        files=files,
        image_size=image_size,
        batch_size=1,
        # Seedream 5.0 rejects the legacy guidance_scale field. LazyLLM's
        # Doubao adapter still defaults it to 2.5, so explicitly pass None;
        # the Ark SDK omits null optionals from the request payload.
        # LazyLLM's Doubao adapter names the provider field ``size`` and ignores
        # the generic ``image_size`` kwarg. Pass both through the normalizer so
        # the Ark request does not silently fall back to 1024x1024.
        model_call_options={
            'size': image_size,
            'watermark': False,
            'guidance_scale': None,
        },
        metadata={
            'adapter': _BAOYU_ADAPTER,
            'provider': _BAOYU_PROVIDER,
            'model': _BAOYU_MODEL,
            'attempts': 1,
        },
    )


def _artifact_rows(key: str) -> List[Dict[str, Any]]:
    """Return normalized artifact rows from the current workflow binding."""
    from lazymind.chat.engine.subagent.tools import get_artifact

    result = get_artifact(key)
    if not isinstance(result, dict) or result.get('status') != 'ok':
        return []
    return [row for row in (result.get('artifacts') or []) if isinstance(row, dict)]


def _artifact_text(key: str) -> str:
    rows = _artifact_rows(key)
    if not rows:
        raise ToolExecutionError(f'Required workflow artifact {key!r} is missing.')
    value = rows[-1].get('value') or {}
    if isinstance(value, dict):
        if value.get('text') is not None:
            return str(value['text']).strip()
        if value.get('data') is not None:
            data = value['data']
            return data if isinstance(data, str) else json.dumps(data, ensure_ascii=False)
    if isinstance(value, str):
        return value.strip()
    if isinstance(value, dict) and value.get('path'):
        return Path(value['path']).read_text(encoding='utf-8').strip()
    raise ToolExecutionError(f'Workflow artifact {key!r} contains no readable text.')


def _artifact_json(key: str) -> Dict[str, Any]:
    rows = _artifact_rows(key)
    if not rows:
        raise ToolExecutionError(f'Required workflow artifact {key!r} is missing.')
    value = rows[-1].get('value') or {}
    data = value.get('data') if isinstance(value, dict) else value
    if data is None and isinstance(value, dict) and value.get('path'):
        data = Path(value['path']).read_text(encoding='utf-8')
    if isinstance(data, str):
        try:
            data = json.loads(data)
        except json.JSONDecodeError as exc:
            raise ToolExecutionError(
                f'Workflow artifact {key!r} is not valid JSON.'
            ) from exc
    if not isinstance(data, dict):
        raise ToolExecutionError(f'Workflow artifact {key!r} must be a JSON object.')
    return data


def _artifact_image_refs(*keys: str) -> List[str]:
    refs: List[str] = []
    for key in keys:
        for row in _artifact_rows(key):
            value = row.get('value') or {}
            if isinstance(value, dict):
                ref = value.get('path') or value.get('image_url') or value.get('url')
            else:
                ref = value
            text = str(ref or '').strip()
            if text and text not in refs:
                refs.append(text)
    return refs


def _requested_aspect_ratio(request: str) -> str:
    update = str(request or '').rpartition('User update (overrides conflicting launch requirements):\n')[2]
    if update and (_SUPPORTED_RATIO_RE.search(update) or re.search(
        r'竖版|竖向|纵向|portrait|vertical|横版|横向|宽屏|landscape|widescreen|方图|正方形|square', update, re.I,
    )):
        request = update
    matches = list(_SUPPORTED_RATIO_RE.finditer(str(request or '')))
    match = matches[-1] if matches else None
    if match:
        return re.sub(r'\s+', '', match.group(1)).replace('：', ':')
    text = str(request or '')
    if re.search(r'(?:竖版|竖向|纵向|portrait|vertical)', text, re.IGNORECASE):
        return '9:16'
    if re.search(r'(?:横版|横向|宽屏|landscape|widescreen)', text, re.IGNORECASE):
        return '16:9'
    if re.search(r'(?:方图|正方形|square)', text, re.IGNORECASE):
        return '1:1'
    return '1:1'


def _immutable_request() -> str:
    from lazymind.chat.engine.subagent.context import require_context

    ctx = require_context()
    from lazymind.chat.workflow.image_request import effective_image_request

    return effective_image_request(ctx.params or {})


def _publish_generated_images(slot: str, result: Dict[str, Any], source_tool: str) -> Dict[str, Any]:
    """Publish only files returned by the in-process image provider call."""
    from lazymind.chat.engine.subagent.tools import _save_artifact

    images = result.get('images') if isinstance(result, dict) else None
    if not isinstance(images, list) or not images:
        raise ToolExecutionError('Seedream returned no normalized image payloads.')
    saved = []
    for index, image in enumerate(images):
        if not isinstance(image, dict):
            raise ToolExecutionError('Seedream returned an invalid image payload.')
        local_path = str(image.get('local_path') or '').strip()
        if not local_path or not Path(local_path).is_file():
            raise ToolExecutionError('Seedream image payload has no real local file.')
        save_options: Dict[str, Any] = {
            'content_type': 'image',
            'source_tool': source_tool,
            'internal_publish': True,
        }
        if slot == 'ordinary_output':
            save_options['publisher_list_index'] = index
        saved.append(_save_artifact(slot, {'path': local_path}, **save_options))
    return {
        'status': 'ok',
        'slot': slot,
        'saved_count': len(saved),
        'provider': result.get('provider'),
        'model': result.get('model'),
        'image_size': result.get('image_size'),
    }


def baoyu_image_generator(
    prompt: str,
    aspect_ratio: str = '1:1',
    urls: Optional[Union[str, List[str]]] = None,
) -> Dict[str, Any]:
    """Generate one image through Baoyu's Seedream routing contract.

    This is LazyMind's deployable, in-process adapter for the Baoyu image
    workflow. It pins Seedream 5.0 and never launches Bun, npx, or a shell.

    Args:
        prompt: Detailed prompt describing the requested final image.
        aspect_ratio: One of 1:1, 16:9, 9:16, 4:3, 3:4, 3:2, 2:3, or 21:9.
        urls: Optional reference image path(s) for reference-guided generation.

    Returns:
        A single generated image using LazyMind's signed image payload contract.
    """
    references = _coerce_url_list(urls)
    files = _resolve_source_image_paths(references) if references else None
    return _run_seedream(
        prompt,
        files=files,
        image_size=_size_for_aspect_ratio(aspect_ratio),
    )


def generate_ordinary_image() -> Dict[str, Any]:
    """Generate and atomically publish the ordinary-route image.

    The prompt and optional references come only from bound workflow artifacts;
    the aspect ratio respects bound request revisions and the current update. A failed provider
    call cannot create ``ordinary_output``.
    """
    prompt = _artifact_text('ordinary_prompt')
    request = _immutable_request()
    update = request.partition('User update (overrides conflicting launch requirements):\n')[2]
    # A later approval must not undo a requirement already saved by preparation.
    marker = 'User update (overrides conflicting launch requirements):\n'
    ratio_request = prompt.split('\nReference evidence', 1)[0] if marker in prompt else request or prompt
    if update:
        prompt += '\nCurrent user requirements (override conflicting prompt details):\n' + update
        ratio_request += '\n' + marker + update
    references = _artifact_image_refs('source_image', 'material_images')
    result = baoyu_image_generator(
        prompt,
        aspect_ratio=_requested_aspect_ratio(ratio_request),
        urls=references or None,
    )
    return _publish_generated_images(
        'ordinary_output', result, 'generate_ordinary_image',
    )


def baoyu_image_editor(
    prompt: str,
    urls: Union[str, List[str]],
    aspect_ratio: Optional[str] = None,
) -> Dict[str, Any]:
    """Edit exactly one authoritative image through Baoyu and Seedream 5.0.

    The source image's aspect ratio is preserved unless ``aspect_ratio`` is
    explicitly provided by the user.

    Args:
        prompt: Precise edit contract, including what must remain unchanged.
        urls: Exactly one authoritative source image path or signed URL.
        aspect_ratio: Optional explicit output ratio; omitted preserves source ratio.

    Returns:
        A new edited image; the source file is never overwritten.
    """
    references = _coerce_url_list(urls)
    if len(references) != 1:
        raise ToolExecutionError(
            'baoyu_image_editor requires exactly one authoritative source image.'
        )
    files = _resolve_source_image_paths(references)
    if len(files) != 1:
        raise ToolExecutionError(
            'Exactly one authoritative source image must resolve successfully.'
        )
    image_size = (
        _size_for_aspect_ratio(aspect_ratio)
        if str(aspect_ratio or '').strip()
        else _size_for_source_image(files[0])
    )
    return _run_seedream(prompt, files=files, image_size=image_size)


def generate_edit_candidate() -> Dict[str, Any]:
    """Generate and atomically publish one real local-edit candidate."""
    contract = _artifact_json('edit_contract')
    source_refs = _artifact_image_refs('authoritative_image')
    if len(source_refs) != 1:
        raise ToolExecutionError(
            'The edit route requires exactly one authoritative_image artifact.'
        )
    required = ('requested_edit', 'edit_scope', 'preserve', 'do_not')
    missing = [key for key in required if not str(contract.get(key) or '').strip()]
    if missing:
        raise ToolExecutionError(
            f'edit_contract is missing required fields: {", ".join(missing)}.'
        )
    prompt = '\n'.join((
        f'Requested edit: {contract["requested_edit"]}',
        f'Edit scope: {contract["edit_scope"]}',
        f'Preserve: {contract["preserve"]}',
        f'Do not: {contract["do_not"]}',
    ))
    request = _immutable_request()
    update = request.partition('User update (overrides conflicting launch requirements):\n')[2]
    request = str(contract.get('user_request') or request)
    if update:
        prompt += '\nCurrent user requirements (preserve everything else):\n' + update
        request += '\nUser update (overrides conflicting launch requirements):\n' + update
    explicit_ratio = _SUPPORTED_RATIO_RE.search(request)
    aspect_ratio = _requested_aspect_ratio(request) if explicit_ratio else None
    result = baoyu_image_editor(
        prompt,
        source_refs,
        aspect_ratio=aspect_ratio,
    )
    return _publish_generated_images(
        'edit_candidate', result, 'generate_edit_candidate',
    )
