from __future__ import annotations

from typing import Any, Dict, List, Optional

import lazyllm
from lazyllm import AutoModel, fc_register
from lazyllm.components.formatter import encode_query_with_filepaths

from lazymind.chat.engine.tools.infra import tool_error, tool_success
from lazymind.chat.engine.tools.infra.image_generation_support import (
    _DEFAULT_BATCH_SIZE,
    _DEFAULT_IMAGE_SIZE,
    _resolve_source_image_paths,
    resolve_tool_image_path,
    run_image_model,
)

_VISION_EXTRACT_DEFAULT_INSTRUCTION = (
    'Describe the image in plain text. Include visible text, objects, charts, and any '
    'details that would help answer follow-up questions about this image.'
)


@fc_register('tool', execute_in_sandbox=False)
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


@fc_register('tool', execute_in_sandbox=False)
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


@fc_register('tool', execute_in_sandbox=False)
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
