from __future__ import annotations

from .context_estimator import estimate_tokens
from .message_fields import estimate_message_tokens


class ContextCapacityError(ValueError):
    pass


class UnsupportedModelInput(ValueError):
    pass


def prepare_model_call(llm, input, history, files, kwargs, budget):
    return llm.prepare_request(
        input, llm_chat_history=history, lazyllm_files=files,
        output_limit=budget.reserved_output_tokens, **kwargs,
    )


def request_tokens(prepared):
    return prepared.estimate_tokens(estimate_tokens, estimate_message_tokens)
