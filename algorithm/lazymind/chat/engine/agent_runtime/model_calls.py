from __future__ import annotations

from lazyllm.common import HandledException

from .budget import parse_token_limit
from .context_estimator import estimate_tokens
from .message_fields import estimate_message_tokens


class ContextCapacityError(HandledException):
    pass


class UnsupportedModelInput(HandledException):
    pass


def request_tokens(prepared):
    return prepared.estimate_tokens(estimate_tokens, estimate_message_tokens)


def configured_output_limit(llm, kwargs, reserved_output_tokens):
    """Clamp an explicit provider limit without turning a context reserve into one."""
    sources = (getattr(llm, '_static_params', {}) or {}, kwargs)
    limits = [limit for source in sources for key in
              ('max_tokens', 'max_completion_tokens', 'output_limit')
              if (limit := parse_token_limit(source.get(key))) is not None]
    return min(reserved_output_tokens, *limits) if limits else None
