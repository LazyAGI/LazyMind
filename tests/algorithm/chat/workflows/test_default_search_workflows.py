"""Execute the shared search selected by every package declaring general web retrieval."""
from pathlib import Path
from unittest.mock import Mock

import httpx
import lazyllm
import pytest
import yaml
from lazyllm.tools.agent.toolsManager import ToolManager

from lazymind.chat.engine.agent_runtime.tool_configuration import ToolConfigurationRuntime
from lazymind.chat.engine.tools.infra import CitationResultMiddleware
from lazymind.chat.service.component.tool_registry import DEFAULT_TOOLS, filter_tools


ROOT = Path(__file__).resolve().parents[4]
CASES = []
for state_file in sorted((ROOT / 'workflows').glob('*/scenario/state.yml')):
    state = yaml.safe_load(state_file.read_text())
    for name, step in (state.get('steps') or {}).items():
        if 'web_search' in (step.get('tools') or []):
            CASES.append(pytest.param(step['tools'], id=f'{state_file.parents[1].name}/{name}'))


@pytest.mark.parametrize('declared_tools', CASES)
def test_declared_workflow_search_executes_without_user_key(monkeypatch, declared_tools):
    old_auth = lazyllm.globals.config['dynamic_tool_auth']
    old_workspace = lazyllm.locals['_lazyllm_agent'].get('workspace') or {}
    lazyllm.globals.config['dynamic_tool_auth'] = {}
    lazyllm.locals['_lazyllm_agent']['workspace'] = {}
    calls = []

    def request(method, url, **options):
        calls.append((url, options.get('headers', {})))
        return httpx.Response(200, request=httpx.Request(method, url), json={'results': [
            {'title': 'Evidence', 'url': 'https://example.test/source', 'snippet': 'A public source'},
        ]})

    monkeypatch.setattr(httpx, 'request', request)
    try:
        configs = [cfg for cfg in filter_tools(DEFAULT_TOOLS, available_tools=declared_tools)
                   if cfg.name == 'web_search']
        assert len(configs) == 1
        host = ToolConfigurationRuntime('u', 'c', 'h', 'r', loader=lambda _: [])
        host._post = Mock(return_value={'status': 'ready', 'tool_config': {'keenable': 'public'}})
        manager = ToolManager(host.native_tools(configs))
        host.bind(manager)
        result = CitationResultMiddleware(manager)({'function': {
            'name': 'KeenableSearch_search', 'arguments': {'query': 'evidence'},
        }})[0]
        assert result['ok'] is True
        assert result['value'][0]['url'] == 'https://example.test/source'
        assert calls[0][0] == 'https://api.keenable.ai/v1/search/public'
        assert 'X-API-Key' not in calls[0][1]
    finally:
        lazyllm.globals.config['dynamic_tool_auth'] = old_auth
        lazyllm.locals['_lazyllm_agent']['workspace'] = old_workspace
