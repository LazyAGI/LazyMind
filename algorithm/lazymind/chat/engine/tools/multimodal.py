from __future__ import annotations

from typing import Any, Dict, List, Optional

import lazyllm
from lazyllm import AutoModel
from lazyllm.components.formatter import encode_query_with_filepaths

from lazymind.chat.engine.tools.infra import tool_error, tool_success
from lazymind.chat.engine.tools.infra.image_generation_support import (
    _DEFAULT_BATCH_SIZE,
    _DEFAULT_IMAGE_SIZE,
    _resolve_source_image_paths,
    resolve_tool_image_path,
    run_image_model,
)
from lazymind.chat.engine.tools.infra.video_generation_support import (
    _DEFAULT_GIF_FPS,
    _DEFAULT_GIF_WIDTH,
    _DEFAULT_VIDEO_DURATION,
    _DEFAULT_VIDEO_RATIO,
    _DEFAULT_VIDEO_RESOLUTION,
    resolve_tool_video_path,
    run_video_model,
    run_video_to_gif,
)

_VISION_EXTRACT_DEFAULT_INSTRUCTION = (
    'Describe the image in plain text. Include visible text, objects, charts, and any '
    'details that would help answer follow-up questions about this image.'
)


def vision_extractor(url: str, instruction: Optional[str] = None) -> Dict[str, Any]:
    """Extract a text description from an image reachable at the given URL.

    Supports common image formats (JPEG, PNG, GIF, WebP, BMP, TIFF).
    Uses a vision-language model to describe visual content in natural language.
    Use this for visual content from knowledge-base results or attached images
    before answering questions that depend on what is visible in the image.

    Prefer passing the short filename shown in tool results or under Attached
    Files, or a ``local_path`` field from the source result. Avoid passing
    ``/static-files/`` signed URLs when a short ref or local path is available.

    Args:
        url: Short image ref (filename), local filesystem path, or a
            ``/static-files/`` signed path from kb results.
        instruction: Optional focus for what to extract; defaults to a general
            description prompt.

    Returns:
        A unified tool payload whose result contains the extracted
        description and resolved local path.
    """
    raw = str(url or '').strip()
    if not raw:
        return tool_error('vision_extractor', 'url is required')

    local_path = resolve_tool_image_path(raw)
    if not local_path:
        raise ValueError(f'image file not found: {raw}')

    prompt_instruction = (
        str(instruction).strip() if instruction else _VISION_EXTRACT_DEFAULT_INSTRUCTION
    )
    encoded_query = encode_query_with_filepaths(prompt_instruction, [local_path])

    agentic_config = lazyllm.globals.get('agentic_config') or {}
    priority = int(agentic_config.get('priority', 0) or 0)

    vlm = AutoModel(model='vlm')
    out = vlm(
        encoded_query,
        stream_output=False,
        llm_chat_history=[],
        lazyllm_files=None,
        priority=priority,
    )
    text = str(out).strip()
    return tool_success('vision_extractor', {'description': text, 'url': local_path})


def image_generator(
    prompt: str,
    image_size: str = _DEFAULT_IMAGE_SIZE,
    batch_size: int = _DEFAULT_BATCH_SIZE,
) -> Dict[str, Any]:
    """Generate an image from a text prompt (text-to-image).

    Uses the configured ``image_generator`` role in runtime_models (type
    ``text2image``). Model files are written under lazyllm temp first, then
    moved into ``shared_upload_dir/ai_generated/`` for signed static URLs.

    Args:
        prompt: Natural-language description of the image to generate.
        image_size: Output resolution, e.g. ``1024x1024``.
        batch_size: Number of images to generate (default 1).

    Returns:
        On success: ``success``, ``prompt``, ``local_path``, optional
        ``image_url`` / ``image_markdown``, and ``images`` (list per file).
    """
    return run_image_model(
        'image_generator',
        prompt,
        image_size=image_size,
        batch_size=batch_size,
    )


def image_editor(
    prompt: str,
    urls: List[str],
    image_size: str = _DEFAULT_IMAGE_SIZE,
    batch_size: int = _DEFAULT_BATCH_SIZE,
) -> Dict[str, Any]:
    """Edit reference image(s) according to a text instruction (image-to-image).

    Uses the configured ``image_editor`` role in runtime_models (type
    ``image_editing``). Pass short refs, ``local_path`` from kb results, or
    filesystem paths; ``/static-files/`` signed URLs are resolved automatically.
    The first entry in ``urls`` is the primary reference; additional entries are
    extra references when the model supports them.

    Args:
        prompt: Edit instruction, e.g. change colors or add text.
        urls: One or more reference image paths or signed static URLs.
        image_size: Output resolution, e.g. ``1024x1024``.
        batch_size: Number of variants to generate (default 1).

    Returns:
        Same shape as ``image_generator``.
    """
    source_files = _resolve_source_image_paths(urls)
    return run_image_model(
        'image_editor',
        prompt,
        files=source_files,
        image_size=image_size,
        batch_size=batch_size,
    )


def video_generator(
    prompt: str,
    urls: Optional[List[str]] = None,
    resolution: str = _DEFAULT_VIDEO_RESOLUTION,
    duration: int = _DEFAULT_VIDEO_DURATION,
    ratio: str = _DEFAULT_VIDEO_RATIO,
) -> Dict[str, Any]:
    """Generate a video from a text prompt (text-to-video).

    Uses the configured ``video_generator`` role in runtime_models (type
    ``text2video``). Optionally pass first-frame reference image(s) via ``urls``
    for image-to-video. Generated files are relocated under
    ``shared_upload_dir/ai_generated/`` for signed static URLs.

    To generate multiple videos (e.g. three stickers), emit multiple
    ``video_generator`` tool calls in the **same** assistant turn; the runtime
    executes them in parallel. Concurrent Seedance calls are capped at 3.
    Do not call them one turn at a time when N>1.

    Args:
        prompt: Natural-language description of the video to generate.
        urls: Optional first-frame / reference image paths or signed static URLs.
        resolution: Output resolution enum, e.g. ``480p`` / ``720p`` / ``1080p``.
        duration: Video length in seconds.
        ratio: Aspect ratio, e.g. ``16:9``.

    Returns:
        On success: ``success``, ``prompt``, ``local_path``, optional
        ``video_url`` / ``video_markdown``, and ``videos`` (list per file).
        When answering the user, copy ``video_markdown`` verbatim (or
        ``video_url`` if markdown is absent); do not invent or rewrite
        ``/static-files/`` paths.
    """
    source_files = _resolve_source_image_paths(urls) if urls else None
    return run_video_model(
        'video_generator',
        prompt,
        files=source_files,
        resolution=resolution,
        duration=duration,
        ratio=ratio,
    )


def video_to_gif(
    url: str,
    fps: int = _DEFAULT_GIF_FPS,
    width: int = _DEFAULT_GIF_WIDTH,
    start: Optional[float] = None,
    duration: Optional[float] = None,
) -> Dict[str, Any]:
    """Convert a local video file to an animated GIF with ffmpeg.

    Use this after video generation or when the user asks for a GIF preview.
    Prefer short refs / ``local_path`` / ``video_url`` from tool results over
    inventing paths. Large videos should pass ``duration`` (and optionally
    ``start``) to keep the GIF small.

    To convert multiple videos, emit multiple ``video_to_gif`` tool calls in
    the **same** assistant turn; they run in parallel. Concurrent GIF
    conversions are capped at 3.

    Args:
        url: Short video ref, local filesystem path, or ``/static-files/`` URL.
        fps: Output frame rate (default 10).
        width: Output width in pixels; height scales to keep aspect ratio.
        start: Optional start time in seconds.
        duration: Optional clip length in seconds from ``start``.

    Returns:
        On success: ``success``, ``local_path``, optional ``image_url`` /
        ``image_markdown`` (GIF is shown as an image), plus conversion params.
        Copy ``image_markdown`` verbatim when answering the user.
    """
    raw = str(url or '').strip()
    if not raw:
        return tool_error('video_to_gif', 'url is required')
    local_path = resolve_tool_video_path(raw)
    if not local_path:
        raise ValueError(f'video file not found: {raw}')
    return run_video_to_gif(
        local_path,
        fps=fps,
        width=width,
        start=start,
        duration=duration,
    )
