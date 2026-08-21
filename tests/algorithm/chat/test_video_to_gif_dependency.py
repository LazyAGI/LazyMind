import json
import os
from pathlib import Path

import pytest
from lazyllm.tools.agent import ToolExecutionError
from lazymind.chat.engine.tools import multimodal
from lazymind.common import ffmpeg_deps


def _fake_binary_names():
    return ('ffmpeg.exe', 'ffprobe.exe') if os.name == 'nt' else ('ffmpeg', 'ffprobe')


def test_windows_ffmpeg_binary_names_can_be_checked_cross_platform(monkeypatch):
    monkeypatch.setattr(ffmpeg_deps.os, 'name', 'nt')

    assert ffmpeg_deps._binary_names() == ('ffmpeg.exe', 'ffprobe.exe')


def test_video_to_gif_returns_dependency_error_when_ffmpeg_is_missing(monkeypatch):
    monkeypatch.setattr(multimodal, 'resolve_ffmpeg_binaries', lambda: (None, None))

    with pytest.raises(ToolExecutionError, match='Animated GIF output requires FFmpeg') as captured:
        multimodal.video_to_gif('/tmp/generated-video.mp4')

    assert '/model-providers/tools#ffmpeg-dependency' in str(captured.value)
    assert 'generated video remains available' in str(captured.value)


def test_resolve_ffmpeg_binaries_reads_bundled_install_without_path(monkeypatch, tmp_path):
    runtime_root = tmp_path
    upload_root = runtime_root / 'data' / 'core' / 'uploads'
    upload_root.mkdir(parents=True)
    bin_dir = runtime_root / 'deps' / 'ffmpeg' / 'bin'
    bin_dir.mkdir(parents=True)
    ffmpeg_name, ffprobe_name = _fake_binary_names()
    ffmpeg = bin_dir / ffmpeg_name
    ffprobe = bin_dir / ffprobe_name
    ffmpeg.write_text('#!/bin/sh\n')
    ffprobe.write_text('#!/bin/sh\n')
    ffmpeg.chmod(0o755)
    ffprobe.chmod(0o755)
    cfg_dir = runtime_root / 'config'
    cfg_dir.mkdir(parents=True)
    (cfg_dir / 'system-dependencies.json').write_text(
        json.dumps({
            'ffmpeg': {
                'source': 'bundled',
                'bundledBinDir': str(bin_dir),
            },
        }),
        encoding='utf-8',
    )

    monkeypatch.setenv('LAZYMIND_RUNTIME_MODE', 'local')
    monkeypatch.setenv('LAZYMIND_UPLOAD_ROOT', str(upload_root))
    monkeypatch.delenv('LAZYMIND_RUNTIME_ROOT', raising=False)
    monkeypatch.setattr(ffmpeg_deps.shutil, 'which', lambda _name: None)

    got_ffmpeg, got_ffprobe = ffmpeg_deps.resolve_ffmpeg_binaries()

    assert got_ffmpeg == str(ffmpeg.resolve())
    assert got_ffprobe == str(ffprobe.resolve())


def test_resolve_ffmpeg_binaries_prefers_custom_path(monkeypatch, tmp_path):
    runtime_root = tmp_path
    upload_root = runtime_root / 'data' / 'core' / 'uploads'
    upload_root.mkdir(parents=True)
    custom_dir = runtime_root / 'custom'
    custom_dir.mkdir()
    ffmpeg_name, ffprobe_name = _fake_binary_names()
    ffmpeg = custom_dir / ffmpeg_name
    ffprobe = custom_dir / ffprobe_name
    ffmpeg.write_text('#!/bin/sh\n')
    ffprobe.write_text('#!/bin/sh\n')
    ffmpeg.chmod(0o755)
    ffprobe.chmod(0o755)
    cfg_dir = runtime_root / 'config'
    cfg_dir.mkdir(parents=True)
    (cfg_dir / 'system-dependencies.json').write_text(
        json.dumps({
            'ffmpeg': {
                'source': 'custom',
                'customPath': str(ffmpeg),
            },
        }),
        encoding='utf-8',
    )

    monkeypatch.setenv('LAZYMIND_RUNTIME_MODE', 'local')
    monkeypatch.setenv('LAZYMIND_UPLOAD_ROOT', str(upload_root))
    monkeypatch.setattr(ffmpeg_deps.shutil, 'which', lambda _name: None)

    got_ffmpeg, got_ffprobe = ffmpeg_deps.resolve_ffmpeg_binaries()

    assert Path(got_ffmpeg).resolve() == ffmpeg.resolve()
    assert Path(got_ffprobe).resolve() == ffprobe.resolve()
