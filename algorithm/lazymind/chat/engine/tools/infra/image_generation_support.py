from __future__ import annotations

import shutil
import uuid
from pathlib import Path
from typing import Any, Dict, List, Optional

import lazyllm
from lazyllm import AutoModel
from lazyllm.components.formatter import decode_query_with_filepaths

from lazymind.chat.service.utils import register_image_url, resolve_local_image_path
from lazymind.chat.service.utils.static_file_url import (
    _upload_root,
    basename_from_path,
    static_file_url_from_any,
)

_DEFAULT_IMAGE_SIZE = '1024x1024'
_DEFAULT_BATCH_SIZE = 1
_IMAGE_SUFFIXES = ('.png', '.jpg', '.jpeg', '.webp', '.gif', '.bmp')
_UPLOAD_SUBDIR = 'ai_generated'


def _image_url_registry() -> dict[str, Any]:
    agentic_config = lazyllm.globals.get('agentic_config') or {}
    citation_state = agentic_config.get('citation_state')
    if isinstance(citation_state, dict):
        return citation_state
    return {}


def resolve_tool_image_path(path_or_ref: str) -> str:
    raw = str(path_or_ref or '').strip()
    if not raw:
        return ''
    registry = _image_url_registry().get('_image_url_registry') or {}
    mapped = registry.get(raw)
    if mapped:
        return resolve_local_image_path(str(mapped)) or resolve_local_image_path(raw)
    return resolve_local_image_path(raw)


def _agentic_priority() -> int:
    agentic_config = lazyllm.globals.get('agentic_config') or {}
    return int(agentic_config.get('priority', 0) or 0)


def _parse_generated_files(result: Any) -> List[str]:
    decoded = decode_query_with_filepaths(result)
    if not isinstance(decoded, dict):
        return []
    files = decoded.get('files') or []
    return [str(item).strip() for item in files if str(item or '').strip()]


def _relocate_generated_image_to_upload(source_path: str) -> str:
    dest_dir = Path(_upload_root()).resolve() / _UPLOAD_SUBDIR
    dest_dir.mkdir(parents=True, exist_ok=True)
    src = Path(source_path)
    suffix = src.suffix if src.suffix.lower() in _IMAGE_SUFFIXES else '.png'
    dest = dest_dir / f'{uuid.uuid4().hex}{suffix}'
    shutil.move(str(src), str(dest))
    return str(dest)


def _relocate_generated_images(paths: List[str]) -> List[str]:
    return [_relocate_generated_image_to_upload(path) for path in paths]


def _build_image_payload(local_path: str, *, label: str) -> Dict[str, str]:
    signed = static_file_url_from_any(local_path)
    payload = {'local_path': local_path}
    if signed:
        payload['image_url'] = signed
        file_label = label or basename_from_path(signed) or 'generated image'
        payload['image_markdown'] = f'![{file_label}]({signed})'
    return payload


def _register_generated_image_paths(local_paths: List[str]) -> None:
    citation_state = _image_url_registry()
    if not isinstance(citation_state, dict):
        return
    for path in local_paths:
        register_image_url(citation_state, path)


def _resolve_source_image_paths(urls: List[str]) -> List[str]:
    candidates = [str(item).strip() for item in urls if str(item or '').strip()]
    if not candidates:
        raise ValueError('at least one source image url is required for image editing')

    resolved: List[str] = []
    seen: set[str] = set()
    for raw in candidates:
        local_path = resolve_tool_image_path(raw)
        if local_path and local_path not in seen:
            seen.add(local_path)
            resolved.append(local_path)
    if not resolved:
        raise ValueError('no valid source image files resolved')
    return resolved


def run_image_model(
    role: str,
    prompt: str,
    *,
    files: Optional[List[str]] = None,
    image_size: str = _DEFAULT_IMAGE_SIZE,
    batch_size: int = _DEFAULT_BATCH_SIZE,
) -> Dict[str, Any]:
    text = str(prompt or '').strip()
    if not text:
        raise ValueError('prompt is required')

    size = str(image_size or _DEFAULT_IMAGE_SIZE).strip() or _DEFAULT_IMAGE_SIZE
    count = int(batch_size or _DEFAULT_BATCH_SIZE)
    if count < 1:
        raise ValueError('batch_size must be at least 1')

    call_kwargs: Dict[str, Any] = {
        'image_size': size,
        'batch_size': count,
        'priority': _agentic_priority(),
    }
    if files:
        call_kwargs['files'] = files

    model = AutoModel(model=role)
    raw = model(text, stream_output=False, **call_kwargs)
    temp_paths = _parse_generated_files(raw)
    if not temp_paths:
        raise ValueError('model returned no generated image files')
    paths = _relocate_generated_images(temp_paths)
    _register_generated_image_paths(paths)
    images = [_build_image_payload(path, label=basename_from_path(path)) for path in paths]
    primary = images[0]
    return {
        'success': True,
        'prompt': text,
        'image_size': size,
        'batch_size': count,
        'images': images,
        **primary,
    }
