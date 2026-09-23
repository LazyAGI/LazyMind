from __future__ import annotations

import json
from typing import Any

from lazymind.common.token_estimation import estimate_tokens

from lazyllm.tools.agent.base import TOOL_OBSERVATION_KEY


INTERNAL_MESSAGE_FIELDS = frozenset({
    '_lazymind_meta',
    'history_seq',
    TOOL_OBSERVATION_KEY,
})


def model_facing_message(message: dict[str, Any]) -> dict[str, Any]:
    return {
        key: value for key, value in message.items()
        if key not in INTERNAL_MESSAGE_FIELDS
    }


def model_facing_history(history: list[dict[str, Any]]) -> list[dict[str, Any]]:
    return [model_facing_message(message) for message in history]


def estimate_message_tokens(message: dict[str, Any]) -> int:
    """Estimate image cost separately; base64 bytes are not text tokens.

    Image billing varies by provider and resolution. Use an approximate fixed
    allowance per image until the provider reports actual usage.
    """
    visible = model_facing_message(message)
    content = visible.get('content')
    image_count = 0
    if isinstance(content, list):
        blocks = []
        for block in content:
            if isinstance(block, dict) and block.get('type') == 'image_url':
                image_count += 1
            else:
                blocks.append(block)
        visible = {**visible, 'content': blocks}
    return estimate_tokens(json.dumps(visible, ensure_ascii=False, default=str)) + 1536 * image_count
