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


def test_oauth_tools_never_enter_shared_cache(monkeypatch):
    clients = []
    class FakeMCPClient:
        def __init__(self, **kwargs):
            clients.append(kwargs)
        def get_tools(self, allowed_tools=None):
            return [object()]
    monkeypatch.setattr(chat_service, 'MCPClient', FakeMCPClient)
    chat_service._mcp_tool_cache.clear()
    server = {
        'url': 'https://mcp.example', 'allowed_tools': ['search'],
        'oauth': dict(user_id='alice', server_id='s', server_url='https://mcp.example',
                      grant_id='g', grant_version=1),
    }
    first = chat_service._load_mcp_server_tools(server)
    second = chat_service._load_mcp_server_tools(server)
    assert first[0] is not second[0]
    assert clients[0]['auth_provider'] != clients[1]['auth_provider']
    assert chat_service._mcp_tool_cache == {}


def test_oauth_empty_allowed_tools_exposes_nothing(monkeypatch):
    clients = []
    class FakeMCPClient:
        def __init__(self, **kwargs):
            clients.append(kwargs)
        def get_tools(self, **kwargs):
            return ['unapproved']
    monkeypatch.setattr(chat_service, 'MCPClient', FakeMCPClient)
    server = {
        'url': 'https://mcp.example', 'allowed_tools': [],
        'oauth': dict(user_id='alice', server_id='s', server_url='https://mcp.example',
                      grant_id='g', grant_version=1),
    }
    assert chat_service._load_mcp_server_tools(server) == []
    assert clients == []


def test_oauth_auth_needed_is_not_swallowed(monkeypatch):
    chat_service._mcp_tool_cache.clear()
    import pytest
    from lazymind.chat.service.mcp_oauth import MCPAuthorizationRequired
    class FakeMCPClient:
        def __init__(self, **kwargs):
            pass
        def get_tools(self, **kwargs):
            raise MCPAuthorizationRequired()
    monkeypatch.setattr(chat_service, 'MCPClient', FakeMCPClient)
    server = {
        'url': 'https://mcp.example', 'allowed_tools': ['search'],
        'oauth': dict(user_id='alice', server_id='s', server_url='https://mcp.example',
                      grant_id='g', grant_version=1),
    }
    with pytest.raises(MCPAuthorizationRequired):
        chat_service._load_mcp_server_tools(server)
