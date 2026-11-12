from types import SimpleNamespace

import lazyllm
import pytest

from lazymind.chat.request_scope import request_configuration_scope
from lazymind.chat.service import external_tools


def echo_tool(text: str) -> dict:
    """Echo text for a registry dispatch test.

    Args:
        text (str): Text to echo.
    """
    return {'text': text, 'local_path': '/private/test'}


@pytest.fixture
def registry(monkeypatch):
    config = SimpleNamespace(name='new_registered_tool', tool=echo_tool, description='Echo')
    monkeypatch.setattr(external_tools, 'DEFAULT_TOOLS', [config])
    monkeypatch.setattr(external_tools, 'tool_is_active', lambda _: True)
    return config


def test_new_registry_tool_is_discovered_and_executed(registry):
    catalog = external_tools.run_external_tools({})['items']
    assert catalog[0]['name'] == 'new_registered_tool'
    assert catalog[0]['available']
    assert 'text' in catalog[0]['input_schema']['properties']
    result = external_tools.run_external_tools({
        'tool_name': registry.name, 'arguments': {'text': 'hello'},
    }, execute=True)
    assert result['result'] == {'text': 'hello'}


def test_internal_visibility_does_not_revoke_external_tools(registry):
    payload = {'disabled_tools': [registry.name], 'tool_name': registry.name, 'arguments': {'text': 'hello'}}
    assert external_tools.run_external_tools(payload)['items'][0]['available']
    assert external_tools.run_external_tools(payload, execute=True)['result'] == {'text': 'hello'}


def test_context_tool_cannot_be_enabled_by_caller(registry):
    registry.name = 'local_fs'
    assert 'filesystem' in external_tools.run_external_tools({})['items'][0]['reason']
    with pytest.raises(ValueError):
        external_tools.run_external_tools({'tool_name': 'local_fs'}, execute=True)


def test_unavailable_and_unknown_tools_fail_closed(registry, monkeypatch):
    monkeypatch.setattr(external_tools, 'tool_is_active', lambda _: False)
    assert not external_tools.run_external_tools({})['items'][0]['available']
    for name in (registry.name, 'unknown'):
        with pytest.raises(ValueError):
            external_tools.run_external_tools({'tool_name': name}, execute=True)


def test_required_arguments_are_validated(registry):
    with pytest.raises(ValueError):
        external_tools.run_external_tools({'tool_name': registry.name, 'arguments': {}}, execute=True)


def test_credentials_are_request_scoped(registry, monkeypatch):
    observed = []

    def active(_):
        config = lazyllm.globals['config']
        model = config.get('dynamic_model_configs', {}).get('llm', {}).get('chat', {}).get('model')
        tool_key = config.get('dynamic_tool_auth', {}).get('tavily')
        observed.append((model, tool_key, dict(lazyllm.locals.curr_key)))
        if tool_key:
            lazyllm.locals.curr_key['tavily'] = tool_key
        return True

    monkeypatch.setattr(external_tools, 'tool_is_active', active)
    with request_configuration_scope() as caller_sid:
        lazyllm.locals.curr_key['caller'] = 'caller-key'
        with pytest.raises(ValueError, match='Invalid tool arguments'):
            external_tools.run_external_tools({
                'tool_name': registry.name,
                'llm_config': {'llm': {'source': 'openai', 'model': 'external-model'}},
                'tool_config': {'tavily': 'external-tool-key'},
            }, execute=True)
        assert external_tools.run_external_tools({})['items'][0]['available']
        assert lazyllm.globals._sid == lazyllm.locals._sid == caller_sid
        assert lazyllm.locals.curr_key == {'caller': 'caller-key'}
        assert 'dynamic_model_configs' not in lazyllm.globals['config']
        assert 'tavily' not in lazyllm.globals['config'].get('dynamic_tool_auth', {})

    assert observed == [('external-model', 'external-tool-key', {}), (None, None, {})]
