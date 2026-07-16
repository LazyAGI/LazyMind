from __future__ import annotations

import shutil
import subprocess
import threading
import uuid
from pathlib import Path
from typing import Any, Dict, List, Optional

import lazyllm
from lazyllm import AutoModel
from lazyllm.components.formatter import decode_query_with_filepaths

from lazymind.chat.engine.tools.infra.image_generation_support import (
    _build_image_payload,
    _register_generated_image_paths,
    resolve_tool_image_path,
)
from lazymind.chat.service.utils.static_file_url import (
    _upload_root,
    basename_from_path,
    static_file_url_from_any,
)

_DEFAULT_VIDEO_RESOLUTION = '480p'
_DEFAULT_VIDEO_DURATION = 5
_DEFAULT_VIDEO_RATIO = '16:9'
_DEFAULT_GIF_FPS = 10
_DEFAULT_GIF_WIDTH = 480
_VIDEO_SUFFIXES = ('.mp4', '.webm', '.mov', '.mkv', '.avi', '.m4v')
_UPLOAD_SUBDIR = 'ai_generated'
# Cap concurrent Seedance / ffmpeg calls when the agent emits many tool_calls in one turn.
_VIDEO_MAX_PARALLEL = 3
_VIDEO_SEMAPHORE = threading.Semaphore(_VIDEO_MAX_PARALLEL)
_GIF_SEMAPHORE = threading.Semaphore(_VIDEO_MAX_PARALLEL)


def _agentic_priority() -> int:
    agentic_config = lazyllm.globals.get('agentic_config') or {}
    return int(agentic_config.get('priority', 0) or 0)


def _parse_generated_files(result: Any) -> List[str]:
    decoded = decode_query_with_filepaths(result)
    if not isinstance(decoded, dict):
        return []
    files = decoded.get('files') or []
    return [str(item).strip() for item in files if str(item or '').strip()]


def _relocate_generated_video_to_upload(source_path: str) -> str:
    dest_dir = Path(_upload_root()).resolve() / _UPLOAD_SUBDIR
    dest_dir.mkdir(parents=True, exist_ok=True)
    src = Path(source_path)
    suffix = src.suffix if src.suffix.lower() in _VIDEO_SUFFIXES else '.mp4'
    dest = dest_dir / f'{uuid.uuid4().hex}{suffix}'
    shutil.move(str(src), str(dest))
    return str(dest)


def _build_video_payload(local_path: str, *, label: str) -> Dict[str, str]:
    signed = static_file_url_from_any(local_path)
    payload = {'local_path': local_path}
    if signed:
        payload['video_url'] = signed
        file_label = label or basename_from_path(signed) or 'generated video'
        payload['video_markdown'] = f'[{file_label}]({signed})'
    return payload


def run_video_model(
    role: str,
    prompt: str,
    *,
    files: Optional[List[str]] = None,
    resolution: str = _DEFAULT_VIDEO_RESOLUTION,
    duration: int = _DEFAULT_VIDEO_DURATION,
    ratio: str = _DEFAULT_VIDEO_RATIO,
) -> Dict[str, Any]:
    text = str(prompt or '').strip()
    if not text:
        raise ValueError('prompt is required')

    res = str(resolution or _DEFAULT_VIDEO_RESOLUTION).strip() or _DEFAULT_VIDEO_RESOLUTION
    dur = int(duration or _DEFAULT_VIDEO_DURATION)
    # Minimum duration varies by model (e.g. Seedance 1.0: 2s, Seedance 2.0: 4s).
    # Use 2 as a shared lower bound; providers may reject unsupported values themselves.
    if dur < 2:
        raise ValueError('duration must be at least 2')
    aspect = str(ratio or _DEFAULT_VIDEO_RATIO).strip() or _DEFAULT_VIDEO_RATIO

    call_kwargs: Dict[str, Any] = {
        'resolution': res,
        'duration': dur,
        'ratio': aspect,
        'priority': _agentic_priority(),
    }
    if files:
        call_kwargs['files'] = files

    with _VIDEO_SEMAPHORE:
        model = AutoModel(model=role)
        raw = model(text, stream_output=False, **call_kwargs)
        temp_paths = _parse_generated_files(raw)
        if not temp_paths:
            raise ValueError('model returned no generated video files')
        paths = [_relocate_generated_video_to_upload(path) for path in temp_paths]
        _register_generated_image_paths(paths)
        videos = [_build_video_payload(path, label=basename_from_path(path)) for path in paths]
        primary = videos[0]
        return {
            'success': True,
            'prompt': text,
            'resolution': res,
            'duration': dur,
            'ratio': aspect,
            'videos': videos,
            **primary,
        }


def resolve_tool_video_path(path_or_ref: str) -> str:
    raw = str(path_or_ref or '').strip()
    if not raw:
        return ''
    local_path = resolve_tool_image_path(raw)
    if local_path and Path(local_path).is_file():
        return local_path
    path = Path(raw.split('?', 1)[0])
    if path.is_file():
        return str(path.resolve())
    return ''


def run_video_to_gif(
    video_path: str,
    *,
    fps: int = _DEFAULT_GIF_FPS,
    width: int = _DEFAULT_GIF_WIDTH,
    start: Optional[float] = None,
    duration: Optional[float] = None,
) -> Dict[str, Any]:
    src = Path(str(video_path or '').strip())
    if not src.is_file():
        raise ValueError(f'video file not found: {video_path}')
    if src.suffix.lower() not in _VIDEO_SUFFIXES:
        raise ValueError(f'unsupported video format: {src.suffix}')

    out_fps = int(fps or _DEFAULT_GIF_FPS)
    out_width = int(width or _DEFAULT_GIF_WIDTH)
    if out_fps < 1:
        raise ValueError('fps must be at least 1')
    if out_width < 16:
        raise ValueError('width must be at least 16')

    dest_dir = Path(_upload_root()).resolve() / _UPLOAD_SUBDIR
    dest_dir.mkdir(parents=True, exist_ok=True)
    dest = dest_dir / f'{uuid.uuid4().hex}.gif'
    palette = dest_dir / f'{uuid.uuid4().hex}_palette.png'

    # Two-pass palette conversion: better colors and clearer frame diffs than single-pass gif.
    # Put -ss/-t before -i so they are input options (required for paletteuse's second -i).
    vf = f'fps={out_fps},scale={out_width}:-1:flags=lanczos'
    input_opts = ['ffmpeg', '-y', '-hide_banner', '-loglevel', 'error']
    if start is not None:
        input_opts.extend(['-ss', str(float(start))])
    if duration is not None:
        input_opts.extend(['-t', str(float(duration))])
    input_opts.extend(['-i', str(src)])

    with _GIF_SEMAPHORE:
        try:
            gen = subprocess.run(
                [*input_opts, '-vf', f'{vf},palettegen=stats_mode=diff', str(palette)],
                capture_output=True, text=True, timeout=300)
            if gen.returncode != 0 or not palette.is_file():
                err = (gen.stderr or gen.stdout or '').strip() or f'exit={gen.returncode}'
                raise RuntimeError(f'ffmpeg palettegen failed: {err}')

            use = subprocess.run(
                [*input_opts, '-i', str(palette),
                 '-lavfi', f'{vf}[x];[x][1:v]paletteuse=dither=bayer:bayer_scale=5',
                 '-an', str(dest)],
                capture_output=True, text=True, timeout=300)
            if use.returncode != 0 or not dest.is_file():
                err = (use.stderr or use.stdout or '').strip() or f'exit={use.returncode}'
                raise RuntimeError(f'ffmpeg paletteuse failed: {err}')
        finally:
            if palette.exists():
                palette.unlink(missing_ok=True)

        local_path = str(dest)
        _register_generated_image_paths([local_path])
        payload = _build_image_payload(local_path, label=basename_from_path(local_path) or 'converted gif')
        return {
            'success': True,
            'source': str(src),
            'fps': out_fps,
            'width': out_width,
            'start': start,
            'duration': duration,
            **payload,
        }
