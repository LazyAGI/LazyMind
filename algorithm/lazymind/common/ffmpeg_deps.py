"""Resolve ffmpeg/ffprobe paths at call time.

Local runtime installs land under ``deps/ffmpeg/bin`` and are recorded in
``config/system-dependencies.json``. Process PATH is only updated on runtime
restart, so callers must resolve the absolute binary paths here instead of
relying solely on ``shutil.which``.
"""

from __future__ import annotations

import json
import os
import shutil
from pathlib import Path
from typing import Optional, Tuple


def _is_local_runtime() -> bool:
    return (os.environ.get('LAZYMIND_RUNTIME_MODE') or '').strip().lower() == 'local'


def _runtime_root() -> Optional[Path]:
    raw = (os.environ.get('LAZYMIND_RUNTIME_ROOT') or '').strip()
    if raw:
        return Path(raw).resolve()
    upload = (
        (os.environ.get('LAZYMIND_UPLOAD_ROOT') or '').strip()
        or (os.environ.get('LAZYMIND_UPLOAD_DIR') or '').strip()
        or (os.environ.get('LAZYMIND_SHARED_UPLOAD_DIR') or '').strip()
    )
    if not upload:
        return None
    # .../data/core/uploads -> runtime root (same as Go RuntimeRootFromEnv)
    try:
        return Path(upload).resolve().parents[2]
    except IndexError:
        return None


def _binary_names() -> Tuple[str, str]:
    if os.name == 'nt':
        return 'ffmpeg.exe', 'ffprobe.exe'
    return 'ffmpeg', 'ffprobe'


def _binaries_in_dir(bin_dir: Path) -> Tuple[str, str]:
    ffmpeg_name, ffprobe_name = _binary_names()
    ffmpeg_path = bin_dir / ffmpeg_name
    ffprobe_path = bin_dir / ffprobe_name
    if ffmpeg_path.is_file() and ffprobe_path.is_file():
        return str(ffmpeg_path.resolve()), str(ffprobe_path.resolve())
    return '', ''


def _sibling_probe(ffmpeg_path: Path) -> str:
    _, probe_name = _binary_names()
    candidate = ffmpeg_path.with_name(probe_name)
    if candidate.is_file():
        return str(candidate.resolve())
    return ''


def _load_local_config(runtime_root: Path) -> Tuple[str, str, Path]:
    bundled = runtime_root / 'deps' / 'ffmpeg' / 'bin'
    source = 'auto'
    custom_path = ''
    cfg_path = runtime_root / 'config' / 'system-dependencies.json'
    if not cfg_path.is_file():
        return source, custom_path, bundled
    try:
        data = json.loads(cfg_path.read_text(encoding='utf-8'))
    except (OSError, json.JSONDecodeError, TypeError, ValueError):
        return source, custom_path, bundled
    ffmpeg_cfg = data.get('ffmpeg') if isinstance(data, dict) else None
    if not isinstance(ffmpeg_cfg, dict):
        return source, custom_path, bundled
    source = str(ffmpeg_cfg.get('source') or 'auto').strip().lower() or 'auto'
    custom_path = str(ffmpeg_cfg.get('customPath') or '').strip()
    bundled_raw = str(ffmpeg_cfg.get('bundledBinDir') or '').strip()
    if bundled_raw:
        bundled = Path(bundled_raw)
    return source, custom_path, bundled


def _resolve_local_binaries() -> Tuple[str, str]:
    runtime_root = _runtime_root()
    if runtime_root is None:
        return '', ''
    source, custom_path, bundled = _load_local_config(runtime_root)
    if source == 'custom' and custom_path:
        candidate = Path(custom_path)
        if candidate.is_dir():
            return _binaries_in_dir(candidate)
        if candidate.is_file():
            probe = _sibling_probe(candidate)
            if probe:
                return str(candidate.resolve()), probe
        return '', ''
    return _binaries_in_dir(bundled)


def resolve_ffmpeg_binaries() -> Tuple[Optional[str], Optional[str]]:
    """Return absolute ``(ffmpeg, ffprobe)`` paths when available.

    For local runtime, prefer the configured bundled/custom install so a
    freshly downloaded binary works for new tasks without restarting
    process-compose. Fall back to PATH for cloud/system installs.
    """
    if _is_local_runtime():
        ffmpeg_path, ffprobe_path = _resolve_local_binaries()
        if ffmpeg_path and ffprobe_path:
            return ffmpeg_path, ffprobe_path

    ffmpeg_path = shutil.which('ffmpeg')
    ffprobe_path = shutil.which('ffprobe')
    if ffmpeg_path and ffprobe_path:
        return ffmpeg_path, ffprobe_path
    return None, None
