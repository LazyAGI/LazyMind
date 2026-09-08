from __future__ import annotations

import json
from pathlib import Path
from typing import Any, Dict, List, Optional, Union

import lazyllm
from lazyllm import AutoModel
from lazyllm.components.formatter import encode_query_with_filepaths
from lazyllm.tools.agent import ToolExecutionError

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
from lazymind.common.ffmpeg_deps import resolve_ffmpeg_binaries


def _coerce_url_list(urls: Optional[Union[str, List[str]]]) -> Optional[List[str]]:
    """Normalize tool urls so stringified JSON arrays from the LLM still validate.

    Models sometimes emit urls as a JSON-encoded string (e.g. '["/path/a.jpg"]')
    instead of a real array; pydantic then rejects Optional[List[str]].
    """
    if urls is None:
        return None
    if isinstance(urls, list):
        return [str(item).strip() for item in urls if str(item or '').strip()] or None
    text = str(urls).strip()
    if not text:
        return None
    if text.startswith('['):
        try:
            parsed = json.loads(text)
        except (TypeError, ValueError, json.JSONDecodeError):
            parsed = None
        if isinstance(parsed, list):
            return [str(item).strip() for item in parsed if str(item or '').strip()] or None
    return [text]


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
        The extracted description and resolved local path.
    """
    raw = str(url or '').strip()
    if not raw:
        raise ToolExecutionError('url is required')
    if Path(raw.split('?', 1)[0]).suffix.lower() == '.pdf':
        raise ToolExecutionError(
            'vision_extractor only supports image files; use grep then read_file, '
            'or kb_tmp_search, to read PDF content.'
        )

    local_path = resolve_tool_image_path(raw)
    if not local_path:
        raise ToolExecutionError(f'Image file not found: {raw}')

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
    return {'description': text, 'url': local_path}


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
        Returns ``prompt``, ``local_path``, optional
        ``image_url`` / ``image_markdown``, and ``images`` (list per file).
        Copy ``image_markdown`` verbatim when answering; never rewrite signed
        ``/static-files/`` paths or expose bare local filesystem paths.
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
        Same shape and rendering contract as ``image_generator``; copy
        ``image_markdown`` verbatim when it is present.
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
    urls: Optional[Union[str, List[str]]] = None,
    first_frame_url: Optional[str] = None,
    last_frame_url: Optional[str] = None,
    reference_urls: Optional[Union[str, List[str]]] = None,
    resolution: str = _DEFAULT_VIDEO_RESOLUTION,
    duration: int = _DEFAULT_VIDEO_DURATION,
    ratio: str = _DEFAULT_VIDEO_RATIO,
) -> Dict[str, Any]:
    """Generate a video from a text prompt (text-to-video).

    Uses the configured ``video_generator`` role in runtime_models (type
    ``text2video``). The current provider/model identity is injected into the
    active-tool instructions for each request. Select inputs using this matrix:

    - Qwen ``wan3.0-video`` / ``wan3.0-video-prime``: text-only, one first
      frame, first+last frames, or up to 10 ordinary reference images. Frame
      control and ordinary-reference mode are mutually exclusive. Duration is
      ``-1`` (auto) or 2-30 seconds.
    - Qwen ``wan2.6-t2v``: text-only; do not pass any image argument.
    - Qwen ``wan2.6-i2v`` / ``wan2.6-i2v-flash``: exactly one first-frame image;
      no last frame and no multi-reference input.
    - SiliconFlow ``Wan-AI/Wan2.2-T2V-A14B``: text-only. SiliconFlow
      ``Wan-AI/Wan2.2-I2V-A14B``: exactly one first-frame image.
    - Doubao Seedance: the adapter can send first/last-frame roles and ordinary
      reference roles. Exact support still depends on the configured Seedance
      variant; Seedance 2.5 supports ordinary-reference mode. Never combine
      first/last-frame control with ordinary references.
    - OpenRouter or an unlisted model: images are generic references only;
      first/last-frame semantics are not guaranteed. Prefer text-only unless
      the selected model's own capability is known.

    If the selected provider/model is absent from the active-tool instructions,
    do not assume advanced image conditioning. An unsupported combination
    returns an explicit error; explain that configuration mismatch to the user
    instead of retrying the same arguments. ``urls`` is a backward-compatible
    generic image list. Generated files are relocated under
    ``shared_upload_dir/ai_generated/`` for signed static URLs.

    To generate multiple videos (e.g. three stickers), emit multiple
    ``video_generator`` tool calls in the **same** assistant turn; the runtime
    executes them in parallel. Concurrent Seedance calls are capped at 3.
    Do not call them one turn at a time when N>1.

    Args:
        prompt: Natural-language description of the video to generate.
        urls: Backward-compatible generic image path(s). One image is treated
            as a first frame; multiple images are treated as ordinary references.
            Do not combine this argument with explicit frame arguments.
        first_frame_url: Optional first-frame path or signed static URL.
        last_frame_url: Optional last-frame path or signed static URL.
        reference_urls: Optional ordinary reference image path(s). A JSON array
            or a single path string is accepted. Images smaller than Ark's
            300px minimum are auto-upscaled before upload. Frame-control mode
            (first/last) and reference-image mode are separate task types; do
            not combine explicit frame arguments with reference_urls.
        resolution: Output resolution enum, e.g. ``480p`` / ``720p`` / ``1080p``.
        duration: Video length in seconds.
        ratio: Aspect ratio, e.g. ``16:9``.

    Returns:
        Returns ``prompt``, ``local_path``, optional
        ``video_url`` / ``video_markdown``, and ``videos`` (list per file).
        When answering the user, copy ``video_markdown`` verbatim (or
        ``video_url`` if markdown is absent); do not invent or rewrite
        ``/static-files/`` paths.
    """
    normalized_references = _coerce_url_list(reference_urls) or []
    normalized_legacy = _coerce_url_list(urls) or []
    has_first_frame = bool(str(first_frame_url or '').strip())
    has_last_frame = bool(str(last_frame_url or '').strip())
    if has_last_frame and not has_first_frame:
        raise ToolExecutionError('last_frame_url requires first_frame_url')
    if (has_first_frame or has_last_frame) and (normalized_references or normalized_legacy):
        raise ToolExecutionError(
            'Frame-control mode cannot be combined with reference_urls or legacy urls; '
            'use either first/last frames or reference images.'
        )

    ordered_urls: List[str] = []
    image_semantics: List[str] = []
    if has_first_frame:
        ordered_urls.append(str(first_frame_url).strip())
        image_semantics.append('first_frame')
    if has_last_frame:
        ordered_urls.append(str(last_frame_url).strip())
        image_semantics.append('last_frame')
    for ref in normalized_references:
        ordered_urls.append(ref)
        image_semantics.append('reference_image')
    for ref in normalized_legacy:
        ordered_urls.append(ref)
        image_semantics.append(
            'first_frame' if len(normalized_legacy) == 1 else 'reference_image'
        )

    # Preserve the first occurrence and its semantic role when the same upload
    # is also present in the generic material list.
    deduped_urls: List[str] = []
    deduped_semantics: List[str] = []
    seen_urls: set[str] = set()
    for ref, semantic in zip(ordered_urls, image_semantics):
        if ref in seen_urls:
            continue
        seen_urls.add(ref)
        deduped_urls.append(ref)
        deduped_semantics.append(semantic)

    source_files = _resolve_source_image_paths(deduped_urls) if deduped_urls else None
    return run_video_model(
        'video_generator',
        prompt,
        files=source_files,
        image_semantics=deduped_semantics or None,
        resolution=resolution,
        duration=duration,
        ratio='adaptive' if any(
            role in {'first_frame', 'last_frame'} for role in deduped_semantics
        ) else ratio,
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
        Returns ``local_path``, optional ``image_url`` /
        ``image_markdown`` (GIF is shown as an image), plus conversion params.
        Copy ``image_markdown`` verbatim when answering the user.
    """
    raw = str(url or '').strip()
    if not raw:
        raise ToolExecutionError('url is required')
    ffmpeg_path, ffprobe_path = resolve_ffmpeg_binaries()
    if not ffmpeg_path or not ffprobe_path:
        raise ToolExecutionError(
            'Animated GIF output requires FFmpeg. Configure it at '
            '/settings?section=system_tools#ffmpeg-dependency; '
            'the generated video remains available.'
        )
    local_path = resolve_tool_video_path(raw)
    if not local_path:
        raise ToolExecutionError(f'Video file not found: {raw}')
    return run_video_to_gif(
        local_path,
        fps=fps,
        width=width,
        start=start,
        duration=duration,
    )
