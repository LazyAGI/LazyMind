from __future__ import annotations

from typing import Any, Dict

from lazymind.chat.engine.tools.infra import tool_error, tool_success
from lazymind.review.memory_review.errors import MemoryStoreError


def memory_write_error(tool_name: str, exc: Exception) -> Dict[str, Any]:
    message = str(exc).strip()
    if message.lower() == 'conflict':
        return tool_error(
            tool_name,
            'There are pending changes. Please resolve them before modifying.',
        )
    return tool_error(tool_name, f'Failed to write via RemoteFS: {message}')


def memory_applied(tool_name: str, **result: Any) -> Dict[str, Any]:
    return tool_success(tool_name, {'status': 'applied', **result})


def map_memory_exception(tool_name: str, exc: Exception) -> Dict[str, Any]:
    if isinstance(exc, ValueError):
        return tool_error(tool_name, str(exc))
    if isinstance(exc, MemoryStoreError):
        return tool_error(tool_name, str(exc))
    return tool_error(tool_name, f'{tool_name} failed: {exc}')
