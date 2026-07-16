from __future__ import annotations

import json
import os
import unicodedata
import uuid
from typing import Any, Dict, Optional

from lazyllm.tools.agent.base import _write_agent_data

from lazymind.chat.engine.tools.infra import tool_success

_MAX_ARTIFACT_BYTES = 2 * 1024 * 1024
def _safe_filename(filename: str, content_type: str) -> str:
    name = str(filename or '').strip()
    if (not name or name in {'.', '..'} or '/' in name or '\\' in name
            or os.path.basename(name) != name):
        raise ValueError('filename must be a plain file name without a directory path')
    if len(name) > 255 or any(unicodedata.category(char) == 'Cc' for char in name):
        raise ValueError('filename is invalid or too long')
    if '.' not in name:
        name += '.json' if content_type == 'json' else '.txt'
    return name


def save_chat_artifact(
    filename: str,
    content: Any,
    content_type: str = 'text',
    caption: Optional[str] = None,
) -> Dict[str, Any]:
    """Save a downloadable text or JSON artifact in the current main-chat turn.

    Use this when the user asks the main Agent to create a downloadable TXT/JSON file.
    Call it once for each requested file. This is a direct main-chat capability and does
    not create a SubAgent task.

    Args:
        filename: Download filename, for example ``notes.txt``. Directory paths are rejected.
        content: Complete text content, or a JSON-compatible value for ``content_type='json'``.
        content_type: ``text`` or ``json``.
        caption: Optional short human-readable description.
    """
    normalized_type = str(content_type or 'text').strip().lower()
    if normalized_type not in {'text', 'json'}:
        raise ValueError("content_type must be 'text' or 'json'")
    safe_name = _safe_filename(filename, normalized_type)
    normalized_caption = str(caption).strip() if caption else None
    if normalized_caption and len(normalized_caption) > 2000:
        raise ValueError('caption exceeds the 2000 character limit')
    if normalized_type == 'json':
        value = {'data': content}
    else:
        text = str(content if content is not None else '')
        value = {'text': text}
    # Measure the actual event value rather than only the raw content: JSON escaping
    # can make the persisted payload larger than its source string.
    encoded_value = json.dumps(
        value, ensure_ascii=False, separators=(',', ':'),
    ).encode('utf-8')
    if len(encoded_value) > _MAX_ARTIFACT_BYTES:
        raise ValueError('artifact content exceeds the 2 MiB limit')

    artifact_id = str(uuid.uuid4())
    _write_agent_data(
        'artifact_created',
        artifact_id=artifact_id,
        filename=safe_name,
        content_type=normalized_type,
        value=value,
        caption=normalized_caption,
    )
    return tool_success('save_chat_artifact', {
        'artifact_id': artifact_id,
        'filename': safe_name,
        'content_type': normalized_type,
        'message': f"Saved downloadable artifact '{safe_name}'.",
    })
