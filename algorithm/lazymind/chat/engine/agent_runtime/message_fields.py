from __future__ import annotations

from typing import Any

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
