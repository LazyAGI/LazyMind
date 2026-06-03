from __future__ import annotations

import os
from typing import Any, Dict, Optional

import lazyllm
from lazyllm import AutoModel
from lazyllm.components.formatter import encode_query_with_filepaths

from lazymind.chat.engine.prompts import VISION_EXTRACT_DEFAULT_INSTRUCTION
from lazymind.chat.engine.tools.infra import handle_tool_errors, tool_success
from lazymind.chat.service.utils import resolve_local_image_path


@handle_tool_errors
def vision_extractor(url: str, instruction: Optional[str] = None) -> Dict[str, Any]:
    """Extract a text description from an image reachable at the given URL.

    Uses the configured VLM endpoint (role ``vlm`` in runtime_models)
    with LazyLLM multimodal file-path encoding.

    Args:
        url: Local filesystem path under the upload root, or a ``/static-files/``
            signed path from kb results (resolved to the local file automatically).
        instruction: Optional focus for what to extract; defaults to a general
            description prompt.

    Returns:
        A unified tool payload whose ``result`` contains the extracted
        description and resolved local path.
    """
    raw = str(url or '').strip()
    if not raw:
        raise ValueError('url is required')

    local_path = resolve_local_image_path(raw)
    if not local_path or not os.path.isfile(local_path):
        raise ValueError(f'image file not found: {local_path or raw}')

    prompt_instruction = (
        str(instruction).strip() if instruction else VISION_EXTRACT_DEFAULT_INSTRUCTION
    )
    encoded_query = encode_query_with_filepaths(prompt_instruction, [local_path])

    agentic_config = lazyllm.globals['agentic_config']
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
