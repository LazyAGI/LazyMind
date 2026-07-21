from __future__ import annotations

import asyncio

from lazymind.chat.service import chat_service


def test_mcp_tool_schemas_are_cached_by_server_config(monkeypatch) -> None:
    calls: list[tuple[str, tuple[str, ...] | None]] = []

    class FakeMCPClient:
        def __init__(self, command_or_url, **kwargs):
            self.url = command_or_url

        def get_tools(self, allowed_tools=None):
            allowed = tuple(allowed_tools) if allowed_tools else None
            calls.append((self.url, allowed))
            return [f'{self.url}:{allowed}']

    monkeypatch.setattr(chat_service, 'MCPClient', FakeMCPClient)
    chat_service._mcp_tool_cache.clear()
    config = [{
        'name': 'docs',
        'url': 'https://mcp.example.com',
        'allowed_tools': ['search'],
    }]

    first = asyncio.run(chat_service._build_mcp_tools(config))
    second = asyncio.run(chat_service._build_mcp_tools(config))

    assert first == second
    assert calls == [('https://mcp.example.com', ('search',))]


def test_mcp_tool_cache_changes_with_server_config(monkeypatch) -> None:
    calls: list[tuple[str, ...] | None] = []

    class FakeMCPClient:
        def __init__(self, command_or_url, **kwargs):
            pass

        def get_tools(self, allowed_tools=None):
            allowed = tuple(allowed_tools) if allowed_tools else None
            calls.append(allowed)
            return list(allowed or ())

    monkeypatch.setattr(chat_service, 'MCPClient', FakeMCPClient)
    chat_service._mcp_tool_cache.clear()

    asyncio.run(chat_service._build_mcp_tools([{
        'url': 'https://mcp.example.com', 'allowed_tools': ['search'],
    }]))
    asyncio.run(chat_service._build_mcp_tools([{
        'url': 'https://mcp.example.com', 'allowed_tools': ['fetch'],
    }]))

    assert calls == [('search',), ('fetch',)]
