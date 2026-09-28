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


def test_batch_loading_is_bounded_concurrent_and_preserves_source_order(monkeypatch):
    import threading
    entered, release = threading.Event(), threading.Event()
    lock = threading.Lock()
    active = peak = 0

    def load(server, namespace='user'):
        nonlocal active, peak
        with lock:
            active += 1
            peak = max(peak, active)
            if active >= 4:
                entered.set()
        try:
            assert release.wait(3)
            return [server['id']]
        finally:
            with lock:
                active -= 1

    monkeypatch.setattr(chat_service, '_load_mcp_server_tools', load)

    async def run():
        task = asyncio.create_task(chat_service._build_mcp_tools([{'id': str(i)} for i in range(8)]))
        try:
            assert await asyncio.to_thread(entered.wait, 2)
            await asyncio.sleep(0.05)
            assert peak == 4
        finally:
            release.set()
        assert await task == [str(i) for i in range(8)]
    asyncio.run(run())


def test_oauth_preview_uses_authorized_snapshot_with_execution_schema_identity(monkeypatch):
    from types import SimpleNamespace
    from lazyllm.tools.mcp.tool_adaptor import generate_lazyllm_tool
    from lazyllm.tools.agent.toolsManager import ToolManager
    server = {'id': 'personal', 'url': 'https://mcp.example/mcp', 'name': 'Personal',
              'transport': 'http', 'auth_type': 'oauth', 'allowed_tools': ['docs.search'],
              'oauth': {'user_id': 'alice', 'server_id': 'personal', 'server_url': 'https://mcp.example/mcp',
                        'grant_id': 'g', 'grant_version': 1}}
    schema = {'type': 'object', 'properties': {'query': {'type': 'string', 'description': 'Keywords'}},
              'required': ['query']}
    wire = SimpleNamespace(name='docs.search', description='Search documents.', inputSchema=schema)
    monkeypatch.setattr(chat_service.MCPClient, 'get_tools', lambda self, **_: [generate_lazyllm_tool(self, wire)])
    actual = ToolManager(chat_service._load_mcp_server_tools(server))
    monkeypatch.setattr(chat_service, 'MCPOAuthAdapter', lambda *_: (_ for _ in ()).throw(AssertionError('OAuth')))
    snapshot = {'status': 'ready', 'tools_complete': True, 'tools': [
        {'tool_name': 'docs.search', 'description': 'Search documents.', 'input_schema': schema},
        {'tool_name': 'unapproved', 'description': 'Hidden', 'input_schema': schema}]}
    preview = ToolManager(chat_service._mcp_tools_for_preview(server, snapshot))
    assert preview.tools_description == actual.tools_description
    assert list(preview.atomic_tool_catalog()) == ['docs_search_mcp_c8f4ce5b8214']
    assert next(iter(preview.atomic_tool_catalog().values()))['identity'] == next(
        iter(actual.atomic_tool_catalog().values()))['identity']
    assert chat_service._mcp_tools_for_preview(server, {**snapshot, 'status': 'needs_authorization'}) == []
    assert chat_service._mcp_tools_for_preview(server, None) == []


def test_preview_reports_missing_schema_without_reclassifying_auth():
    report = chat_service._mcp_preview_catalog_status([
        {'service': 'mcp:a', 'label': 'A', 'status': 'ready', 'tools_complete': True},
        {'service': 'mcp:b', 'label': 'B', 'status': 'ready', 'tools_complete': False},
        {'service': 'mcp:c', 'label': 'C', 'status': 'needs_authorization'},
    ])
    assert report == {'source': 'discovered_snapshot', 'complete': False, 'missing_services': ['B']}


def test_loader_cancellation_drains_pending_service_tasks():
    import threading
    import pytest
    from lazymind.chat.service.mcp_loading import load_mcp_catalog

    release = threading.Event()

    def load(config):
        if config['id'] == 0:
            raise asyncio.CancelledError()
        release.wait(timeout=2)
        return []

    async def run():
        try:
            with pytest.raises(asyncio.CancelledError):
                await load_mcp_catalog([
                    {'service': f'mcp:{i}', 'status': 'ready', 'runtime': {'id': i}} for i in range(9)], load)
            assert not [task for task in asyncio.all_tasks() if task is not asyncio.current_task()]
        finally:
            release.set()

    asyncio.run(run())
