from __future__ import annotations

from functools import partial
import json

import lazyllm
from lazyllm.common import HandledException
from lazyllm.module.llms.onlinemodule.base.onlineChatModuleBase import LazyLLMOnlineChatModuleBase

from .budget import parse_token_limit
from .context_estimator import estimate_tokens
from .message_fields import estimate_message_tokens


class ContextCapacityError(HandledException):
    pass


class UnsupportedModelInput(HandledException):
    pass


def _tool_call_stream_parser(parser_type, supplier, **settings):
    emit = settings.get('emit_message')
    if emit is not None:
        def emit_channels(message):
            emit(message)
            for choice in message.get('choices', []):
                delta = choice.get('message', choice.get('delta', {}))
                if delta.get('tool_calls'):
                    supplier._stream_output(json.dumps(delta['tool_calls'], ensure_ascii=False),
                                            cls='tool_call_delta')
        settings['emit_message'] = emit_channels
    return parser_type(**settings)


def enable_tool_call_stream(client):
    if not isinstance(client, lazyllm.OnlineChatModule):
        return
    supplier = client
    if not isinstance(client, LazyLLMOnlineChatModuleBase):
        with lazyllm.globals.stack_enter(client.identities):
            supplier = client._get_supplier()
    # Configure only this call's supplier, keeping the provider's parser intact.
    supplier.RESPONSE_PARSER_CLASS = partial(_tool_call_stream_parser, supplier.RESPONSE_PARSER_CLASS, supplier)


def request_tokens(prepared):
    return prepared.estimate_tokens(estimate_tokens, estimate_message_tokens)


def configured_output_limit(llm, kwargs):
    """Preserve explicit provider limits independently of context compression reserves."""
    sources = (getattr(llm, '_static_params', {}) or {}, kwargs)
    limits = [limit for source in sources for key in
              ('max_tokens', 'max_completion_tokens', 'output_limit')
              if (limit := parse_token_limit(source.get(key))) is not None]
    return min(limits) if limits else None
