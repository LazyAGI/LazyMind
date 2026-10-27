import pytest
import lazyllm

from lazymind.chat.engine.tools import github_prs
from lazymind.chat.engine.tools.file_resources import github_contributions
from lazymind.chat.engine.tools.conversation_workspace import _cfg
from lazyllm.tools.agent import ToolExecutionError, ToolManager


@pytest.fixture
def chat_scope(tmp_path, monkeypatch):
    monkeypatch.setitem(_cfg._impl, 'agentic_workspace', str(tmp_path))
    previous = lazyllm.globals.get('agentic_config')
    lazyllm.globals['agentic_config'] = {'user_id': 'alice', 'conversation_id': 'chat-1'}
    yield
    lazyllm.globals['agentic_config'] = previous


@pytest.mark.parametrize('repository', ['acme/project', 'https://github.com/acme/project'])
def test_chat_collect_read_and_isolation(chat_scope, monkeypatch, repository):
    calls = []
    def api(method, path, **kwargs):
        calls.append((path, kwargs['params']))
        return [{'number': 1, 'user': {'login': 'alice'}, 'body': 'My contribution',
                 'created_at': '2026-05-01T00:00:00Z', 'state': 'closed',
                 'head': {'ref': 'feature'}, 'base': {'ref': 'main'}}]
    monkeypatch.setattr(github_contributions, 'request_api', lambda mode: api)
    tool = github_prs.GitHubPRToolkit()
    result = tool.collect(repository, author='ALICE', state='closed')
    source = result['records'][0]['source_id']
    assert tool.read_description(source)['description'] == 'My contribution'
    assert calls == [('/repos/acme/project/pulls', {
        'state': 'closed', 'sort': 'created', 'direction': 'desc', 'per_page': 100, 'page': 1})]
    lazyllm.globals['agentic_config'] = {'user_id': 'bob', 'conversation_id': 'chat-1'}
    with pytest.raises(ToolExecutionError, match='not found in this conversation'):
        tool.read_description(source)
    lazyllm.globals['agentic_config'] = {'user_id': 'alice', 'conversation_id': 'chat-2'}
    with pytest.raises(ToolExecutionError, match='not found in this conversation'):
        tool.read_description(source)


def test_missing_context_fails_closed(chat_scope):
    lazyllm.globals['agentic_config'] = {}
    with pytest.raises(ToolExecutionError, match='context is unavailable'):
        github_prs.GitHubPRToolkit().collect('acme/project', 'alice')


def test_registered_without_credentials_and_schema():
    from lazymind.chat.service.component.tool_registry import DEFAULT_TOOLS, filter_tools
    from lazymind.chat.service.external_tools import _CONTEXT_REQUIREMENTS
    configs = filter_tools(DEFAULT_TOOLS, available_tools=['github_prs'])
    assert len(configs) == 1
    manager = ToolManager([configs[0].tool])
    assert manager is not None
    assert 'github_prs' in _CONTEXT_REQUIREMENTS
    assert 'no PPT workflow' in configs[0].appendix_system_prompt['tool_policy']
    assert 'Never require the user to construct' in configs[0].appendix_system_prompt['tool_policy']
