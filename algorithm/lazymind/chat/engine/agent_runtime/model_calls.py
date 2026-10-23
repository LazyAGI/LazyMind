from __future__ import annotations

from lazyllm.common import HandledException

from .context_estimator import estimate_tokens
from .message_fields import estimate_message_tokens


class ContextCapacityError(HandledException):
    pass


class UnsupportedModelInput(HandledException):
    pass


def request_tokens(prepared):
    return prepared.estimate_tokens(estimate_tokens, estimate_message_tokens)
