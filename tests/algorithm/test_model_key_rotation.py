from concurrent.futures import ThreadPoolExecutor
from copy import deepcopy
import importlib

import lazyllm
import pytest

from lazymind import model_config


def role(keys='first-key\nsecond-key', *, source='openai', endpoint='https://example.test/v1'):
    return {'source': source, 'base_url': endpoint, 'model': 'example-model', 'api_key': keys}


def resolve(config):
    return model_config.resolve_request_api_keys(config)


def test_legacy_newline_pool_always_uses_first_key_without_mutating_saved_config():
    config = {'llm': role(' first-key\r\nsecond-key\nfirst-key\n ')}
    original = deepcopy(config)
    assert [resolve(config)['llm']['api_key'] for _ in range(4)] == ['first-key'] * 4
    assert config == original


def test_backend_selected_singleton_remains_authoritative_across_requests():
    config = {'llm': role('second-key'), 'evo_llm': {**role('second-key'), 'model': 'other-model'}}
    for _ in range(4):
        resolved = resolve(config)
        assert resolved['llm']['api_key'] == resolved['evo_llm']['api_key'] == 'second-key'


def test_other_suppliers_cannot_change_a_selected_key():
    config = {
        'llm': role('selected-openai'),
        'evo_llm': role('selected-deepseek', source='deepseek'),
        'vlm': role('selected-proxy', endpoint='https://other.test/v1'),
    }
    for _ in range(4):
        resolved = resolve(config)
        assert [item['api_key'] for item in resolved.values()] == [
            'selected-openai', 'selected-deepseek', 'selected-proxy',
        ]


@pytest.mark.parametrize('keys, expected', [
    (' single-key ', 'single-key'), ('', ''), (None, ''),
    ([' first-key ', 'second-key', 'first-key'], 'first-key'),
    (('first-key', 'second-key'), 'first-key'),
    ([' ', '\n', 'second-key'], 'second-key'),
])
def test_single_key_keyless_and_legacy_sequences_are_normalized(keys, expected):
    assert resolve({'llm': role(keys)})['llm']['api_key'] == expected


def test_keyless_configuration_remains_keyless():
    config = {'llm': {**role(''), 'skip_auth': True}}
    assert resolve(config)['llm'] == config['llm']


def test_concurrent_legacy_requests_do_not_cycle_credentials():
    config = {'llm': role()}
    with ThreadPoolExecutor(max_workers=16) as executor:
        selected = list(executor.map(lambda _: resolve(config)['llm']['api_key'], range(300)))
    assert selected == ['first-key'] * 300


def test_real_lazyllm_injection_preserves_selected_key_and_capabilities(monkeypatch):
    monkeypatch.setattr(model_config, 'load_model_config', lambda: {})
    config = {'llm': {**role('selected-key'), 'vision': True}, 'evo_llm': role('selected-key')}
    for _ in range(3):
        with lazyllm.new_session():
            model_config.inject_model_config(config)
            injected = lazyllm.globals['config']['openai_api_key']
            assert injected['llm'] == injected['evo_llm'] == 'selected-key'
            assert lazyllm.globals['lazymind_model_capabilities']['llm']['vision'] is True


def test_forwarded_config_never_contains_a_legacy_newline_pool(monkeypatch):
    target = importlib.import_module('lazyllm.module.llms.model_config_inject')
    captured = []
    monkeypatch.setattr(target, 'inject_model_config', captured.append)
    monkeypatch.setattr(model_config, 'load_model_config', lambda: {})
    for _ in range(3):
        model_config.inject_model_config({'llm': role(), 'evo_llm': role()})
    assert all(item['api_key'] == 'first-key' for config in captured for item in config.values())


def test_online_authorization_keeps_backend_selected_key_without_network():
    from lazyllm.module.llms.onlinemodule.supplier.openai import OpenAIChat

    headers = []
    for _ in range(3):
        config = resolve({'llm': role('selected-key')})['llm']
        model = OpenAIChat(base_url=config['base_url'], model=config['model'], api_key=config['api_key'])
        headers.append(model._header['Authorization'])
    assert headers == ['Bearer selected-key'] * 3
