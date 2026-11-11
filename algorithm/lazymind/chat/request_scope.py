"""Own the LazyLLM state used by one stateless request."""

from collections.abc import Iterator
from contextlib import contextmanager
from typing import Any
from uuid import uuid4

import lazyllm

from lazymind.chat.engine.tool_auth import inject_tool_config
from lazymind.model_config import inject_model_config


@contextmanager
def request_configuration_scope(
    *,
    model_config: dict[str, Any] | None = None,
    tool_config: dict[str, Any] | None = None,
) -> Iterator[str]:
    """Bind request configuration and restore the caller's LazyLLM session."""
    sid = f'lazymind-request-{uuid4().hex}'
    with lazyllm.globals._bind_sid(sid), lazyllm.locals._bind_sid(sid):
        try:
            inject_model_config(model_config)
            inject_tool_config(tool_config)
            yield sid
        finally:
            lazyllm.locals._clear_sid(sid)
            lazyllm.globals._clear_sid(sid)
