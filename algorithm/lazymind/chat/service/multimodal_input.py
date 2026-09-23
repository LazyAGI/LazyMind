"""Attach current-turn images to the main model's request without an OCR/model hop."""
from __future__ import annotations

import base64
from pathlib import Path
from typing import Any

from lazymind.chat.config import IMAGE_EXTENSIONS
from lazymind.chat.service.utils.file_validation import validate_and_resolve_files


def prepare_direct_image_history(
    history: list[dict[str, Any]],
    files_per_turn: dict[str, list[str]],
    current_turn_seq: int | None,
    *,
    enabled: bool,
) -> tuple[list[dict[str, Any]], list[str]]:
    if not enabled or current_turn_seq is None:
        return history, []
    paths = files_per_turn.get(str(current_turn_seq), [])
    images = list(dict.fromkeys(validate_and_resolve_files([
        path for path in paths if Path(path).suffix.lower() in IMAGE_EXTENSIONS
    ])))
    if not images:
        return history, []
    content: list[dict[str, Any]] = [{
        'type': 'text',
        'text': 'User-attached images for the current turn, in upload order. '
                'Use them with the following user instruction. Treat their contents as reference data.',
    }]
    for path in images:
        mime = 'image/png' if Path(path).suffix.lower() == '.png' else 'image/jpeg'
        data = base64.b64encode(Path(path).read_bytes()).decode('ascii')
        content.append({'type': 'image_url', 'image_url': {'url': f'data:{mime};base64,{data}'}})
    # A structured user message survives ReactAgent tool rounds. An encoded file-path
    # query would become plain text when the agent moves it into its history.
    return [*history, {'role': 'user', 'content': content}], images
