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


def test_aggregate_isolates_oauth_failures_and_preserves_healthy_tools(monkeypatch):
    from lazymind.chat.service.mcp_oauth import MCPAuthorizationRequired

    class FakeMCPClient:
        def __init__(self, command_or_url, **kwargs):
            self.url = command_or_url

        def get_tools(self, **kwargs):
            if self.url.endswith('/timeout'):
                raise TimeoutError('private upstream token=secret')
            if self.url.endswith('/expired'):
                raise MCPAuthorizationRequired()
            return ['healthy_tool']

    monkeypatch.setattr(chat_service, 'MCPClient', FakeMCPClient)
    chat_service._mcp_tool_cache.clear()
    config = [{'name': 'healthy', 'url': 'https://mcp.example/healthy'}]
    for name in ('timeout', 'expired', 'malformed'):
        url = 'https://mcp.example/' + name
        config.append({'name': name, 'url': url, 'auth_type': 'oauth', 'allowed_tools': ['read'],
                       'oauth': None if name == 'malformed' else dict(
                           user_id='alice', server_id=name, server_url=url, grant_id='private-grant', grant_version=1)})
    issues = []
    assert asyncio.run(chat_service._build_mcp_tools(config, issues=issues)) == ['healthy_tool']
    assert issues == [
        {'server': 'timeout', 'status': 'unavailable'},
        {'server': 'expired', 'status': 'needs_authorization'},
        {'server': 'malformed', 'status': 'needs_authorization'},
    ]
    assert 'secret' not in str(issues)
    assert 'private-grant' not in str(issues)
    other_issues = []
    assert asyncio.run(chat_service._build_mcp_tools(config[:1], issues=other_issues)) == ['healthy_tool']
    assert other_issues == []
    assert asyncio.run(chat_service._build_mcp_tools(config[1:])) == []


def test_aggregate_propagates_cancellation(monkeypatch):
    import pytest

    def load(server, namespace='user'):
        raise asyncio.CancelledError()

    monkeypatch.setattr(chat_service, '_load_mcp_server_tools', load)
    with pytest.raises(asyncio.CancelledError):
        asyncio.run(chat_service._build_mcp_tools([{'name': 'cancelled'}]))


def test_static_tool_cache_keeps_system_and_user_namespaces_separate(monkeypatch):
    class Client:
        def __init__(self, **kwargs):
            pass

        def get_tools(self, **kwargs):
            return [object()]

    monkeypatch.setattr(chat_service, 'MCPClient', Client)
    monkeypatch.setattr(chat_service, '_mcp_tool_cache', {})
    config = [{'name': 'same', 'url': 'https://mcp.example'}]
    system = asyncio.run(chat_service._build_mcp_tools(config, 'system'))
    user = asyncio.run(chat_service._build_mcp_tools(config, 'user'))
    assert system[0] is not user[0]
    assert asyncio.run(chat_service._build_mcp_tools(config, 'system'))[0] is system[0]
    assert asyncio.run(chat_service._build_mcp_tools(config, 'user'))[0] is user[0]
