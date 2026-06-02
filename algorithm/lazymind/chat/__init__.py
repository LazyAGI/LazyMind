"""LazyMind chat package."""

from lazymind.chat.app import app
from lazymind.chat.service import handle_chat

__all__: list[str] = ["app", "handle_chat"]
