"""LazyMind-owned request lifecycle for product-specific tool credentials."""

from __future__ import annotations

from typing import Any, Optional

import lazyllm
from lazyllm.tools.tool_config_inject import inject_tool_config as _inject_tool_config

MAIL_AUTH_NAME = 'mail'


def clear_mail_tool_auth() -> None:
    existing = lazyllm.globals.config['dynamic_tool_auth'] or {}
    if not isinstance(existing, dict) or MAIL_AUTH_NAME not in existing:
        return
    cleaned = dict(existing)
    cleaned.pop(MAIL_AUTH_NAME, None)
    lazyllm.globals.config['dynamic_tool_auth'] = cleaned


def inject_tool_config(tool_config: Optional[dict[str, Any]]) -> None:
    """Clear leftover mail auth, then inject this request's tool_config."""
    clear_mail_tool_auth()
    _inject_tool_config(tool_config)
