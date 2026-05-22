from __future__ import annotations

import os
from functools import wraps
from typing import Any, Dict, Optional

import lazyllm
from lazyllm import AutoModel, fc_register
from lazyllm.components.formatter import encode_query_with_filepaths

from chat.components.process.query_image_rewriter import extract_text_from_model_output
from chat.utils.load_config import get_config_path
from chat.utils.static_file_url import resolve_local_image_path


_VISION_EXTRACT_DEFAULT_INSTRUCTION = (
    'Describe the image in plain text. Include visible text, objects, charts, and any '
    'details that would help answer follow-up questions about this image.'
)


def _tool_failure(tool_name: str, exc: Exception) -> Dict[str, Any]:
    return {
        'success': False,
        'reason': f'{tool_name} failed: {exc}',
        'error': str(exc),
        'error_type': type(exc).__name__,
    }


def _handle_tool_errors(func):
    @wraps(func)
    def wrapper(*args, **kwargs):
        try:
            return func(*args, **kwargs)
        except Exception as exc:
            return _tool_failure(func.__name__, exc)

    return wrapper


@fc_register('tool', execute_in_sandbox=False)
@_handle_tool_errors
def vision_extractor(url: str, instruction: Optional[str] = None) -> Dict[str, Any]:
    """Extract a text description from an image reachable at the given URL.

    Uses the configured VLM endpoint (role ``vlm`` in runtime_models)
    with the same multimodal encoding as ``QueryImageRewriter`` (file paths / URLs
    embedded in the prompt for the VLM).

    Args:
        url: Local filesystem path under the upload root, or a ``/static-files/``
            signed path from kb results (resolved to the local file automatically).
        instruction: Optional focus for what to extract; defaults to a general
            description prompt.

    Returns:
        A dict with ``success``, and on success ``description`` (plain text).
    """
    raw = str(url or '').strip()
    if not raw:
        raise ValueError('url is required')

    local_path = resolve_local_image_path(raw)
    if not local_path or not os.path.isfile(local_path):
        raise ValueError(f'image file not found: {local_path or raw}')

    prompt_instruction = (
        str(instruction).strip() if instruction else _VISION_EXTRACT_DEFAULT_INSTRUCTION
    )
    encoded_query = encode_query_with_filepaths(prompt_instruction, [local_path])

    agentic_config = lazyllm.globals.get('agentic_config') or {}
    priority = int(agentic_config.get('priority', 0) or 0)

    vlm = AutoModel(model='vlm', config=get_config_path())
    out = vlm(
        encoded_query,
        stream_output=False,
        llm_chat_history=[],
        lazyllm_files=None,
        priority=priority,
    )
    text = extract_text_from_model_output(out)
    return {'success': True, 'description': text, 'url': local_path}
