import asyncio
from types import SimpleNamespace

import pytest

from lazymind.chat.service import chat_service, vocabulary_mcp
from lazymind.chat.service.knowledge_management import KnowledgeManagementRuntime, bind_knowledge_server


def test_builtin_vocabulary_uses_internal_endpoint_and_isolates_users(monkeypatch):
    monkeypatch.setattr(vocabulary_mcp, 'config', {
        'core_api_url': 'http://127.0.0.1:18001', 'core_internal_token': 'service-token',
    })
    clients = []

    class Client:
        def __init__(self, **kwargs):
            self.kwargs = kwargs
            clients.append(kwargs)

        def get_tools(self, allowed_tools):
            from lazyllm.tools.mcp.tool_adaptor import generate_lazyllm_tool
            self.server_name = self.kwargs['server_name']
            self.tool_name_prefix = self.kwargs['tool_name_prefix']
            return [generate_lazyllm_tool(self, SimpleNamespace(
                name=name, description='Query vocabulary.', inputSchema={'type': 'object', 'properties': {}},
            )) for name in allowed_tools]

    monkeypatch.setattr(chat_service, 'MCPClient', Client)
    monkeypatch.setattr(chat_service, '_mcp_tool_cache', {})
    knowledge = KnowledgeManagementRuntime({
        'user_id': 'alice', 'conversation_id': 'conv', 'history_id': 'history', 'run_id': 'run',
    }, 'http://127.0.0.1:18001')
    for user in ('alice', 'bob'):
        server = vocabulary_mcp.bind_vocabulary_server({
            'id': vocabulary_mcp.BUILTIN_ID, 'name': 'LazyMind Vocabulary',
            'allowed_tools': [*vocabulary_mcp.TOOLS, 'knowledge.create'],
        }, {'user_id': user})
        assert bind_knowledge_server(server, knowledge) is server
        issues = []
        tools = asyncio.run(chat_service._build_mcp_tools([server], 'system', issues=issues))
        assert issues == []
        assert [tool.__name__ for tool in tools] == ['mcp_vocabulary_wordbook_list', 'mcp_vocabulary_word_list']
        assert [tool.__mcp_tool_name__ for tool in tools] == list(vocabulary_mcp.TOOLS)
        client = clients[-1]
        assert client['command_or_url'] == 'http://127.0.0.1:18001/internal/mcp/vocabulary'
        assert client['result_mode'] == 'structured'
        assert asyncio.run(client['auth_provider']()) == {
            'Authorization': 'Bearer service-token', 'X-User-Id': user,
        }
    assert len(clients) == 2
    assert chat_service._mcp_tool_cache == {}


def test_missing_vocabulary_credentials_are_reported_as_unavailable(monkeypatch):
    monkeypatch.setattr(vocabulary_mcp, 'config', {
        'core_api_url': 'http://127.0.0.1:18001', 'core_internal_token': '',
    })
    server = vocabulary_mcp.bind_vocabulary_server({
        'id': vocabulary_mcp.BUILTIN_ID, 'name': 'LazyMind Vocabulary',
    }, {'user_id': 'alice'})

    class Client:
        def __init__(self, **kwargs):
            self.headers = kwargs['auth_provider']

        def get_tools(self, **kwargs):
            asyncio.run(self.headers())

    monkeypatch.setattr(chat_service, 'MCPClient', Client)
    issues = []
    assert asyncio.run(chat_service._build_mcp_tools([server], 'system', issues=issues)) == []
    assert issues == [{'server': 'LazyMind Vocabulary', 'status': 'unavailable'}]


@pytest.mark.parametrize('allowed', [[], ['knowledge.create']])
def test_vocabulary_scope_never_exposes_other_core_tools(monkeypatch, allowed):
    monkeypatch.setattr(vocabulary_mcp, 'config', {
        'core_api_url': 'http://127.0.0.1:18001', 'core_internal_token': 'service-token',
    })
    server = vocabulary_mcp.bind_vocabulary_server({
        'id': vocabulary_mcp.BUILTIN_ID, 'allowed_tools': allowed,
    }, {'user_id': 'alice'})
    assert asyncio.run(chat_service._build_mcp_tools([server], 'system')) == []
