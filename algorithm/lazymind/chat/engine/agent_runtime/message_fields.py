from __future__ import annotations

import json
from typing import Any

from lazymind.common.token_estimation import estimate_tokens

from lazyllm.tools.agent.base import TOOL_OBSERVATION_KEY
from lazyllm.module.llms.onlinemodule.base.protocol_ir import MessageIR
from .env_redaction import redact_session_env_arguments


INTERNAL_MESSAGE_FIELDS = frozenset({
    '_lazymind_meta',
    'history_seq',
    TOOL_OBSERVATION_KEY,
})


def model_response_text(response: Any) -> str:
    """Project only semantic text, excluding reasoning and provider envelopes."""
    if isinstance(response, MessageIR):
        return response.text.strip()
    if isinstance(response, (str, dict)):
        return MessageIR.from_chat(response).text.strip()
    return ''


def canonical_history_message(message: dict[str, Any]) -> dict[str, Any]:
    """Persist semantic messages with the same tool-argument redaction as UI history."""
    def arguments(name, value):
        original = value
        if isinstance(value, str):
            try:
                value = json.loads(value)
            except ValueError:
                pass
        redacted = redact_session_env_arguments(name, value)
        if isinstance(original, str) and redacted == value:
            return original
        return json.dumps(redacted, ensure_ascii=False)

    def project(value):
        if isinstance(value, list):
            return [project(item) for item in value]
        if not isinstance(value, dict):
            return value
        result = {key: project(item) for key, item in value.items()}
        function = result.get('function')
        if isinstance(function, dict):
            function['arguments'] = arguments(function.get('name'), function.get('arguments', {}))
        # Opaque adapter snapshots must never restore an unredacted copy of
        # arguments on replay. Encrypted/signature strings remain opaque.
        if result.get('type') == 'function_call':
            result['arguments'] = arguments(result.get('name'), result.get('arguments', {}))
        return result

    return project(MessageIR.from_chat(message).to_chat())


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
    return MessageIR.from_chat(model_facing_message(message)).estimate_tokens(estimate_tokens)
