from types import SimpleNamespace

import pytest

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


def test_disabled_tool_is_listed_but_cannot_execute(registry):
    payload = {'disabled_tools': [registry.name], 'tool_name': registry.name, 'arguments': {'text': 'hello'}}
    assert not external_tools.run_external_tools(payload)['items'][0]['available']
    with pytest.raises(ValueError):
        external_tools.run_external_tools(payload, execute=True)


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
    import lazyllm
    observed = []

    def active(_):
        observed.append(dict(lazyllm.globals.config['dynamic_tool_auth'] or {}))
        return True

    monkeypatch.setattr(external_tools, 'tool_is_active', active)
    external_tools.run_external_tools({'tool_config': {'tavily': 'test-only-secret'}})
    external_tools.run_external_tools({})
    assert observed[0]
    assert not observed[1]
