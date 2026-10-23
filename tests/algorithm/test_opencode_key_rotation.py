from copy import deepcopy

from evo.operations.repair import opencode
from evo.repair_model import opencode_settings


def role(key='first-key\nsecond-key'):
    return {
        'source': 'openai', 'model': 'example', 'base_url': 'https://example.test/v1',
        'api_key': key,
        'opencode': {
            'provider': 'openai', 'provider_model': 'example', 'model': 'openai/example',
            'npm': '@ai-sdk/openai', 'base_url': 'https://example.test/v1',
        },
    }


def test_legacy_opencode_configuration_never_cycles_or_mutates_durable_identity():
    config = role()
    original = deepcopy(config)
    settings = opencode_settings(config)
    for _ in range(3):
        output = opencode._opencode_json(settings)
        options = output['provider']['openai']['options']
        assert options['apiKey'] == 'first-key'
        assert '\n' not in options['apiKey']
    assert config == original
    assert settings['api_key'] == 'first-key\nsecond-key'


def test_opencode_keeps_backend_selected_single_key():
    settings = opencode_settings(role('selected-key'))
    for _ in range(3):
        assert opencode._opencode_json(settings)['provider']['openai']['options']['apiKey'] == 'selected-key'


def test_selected_and_legacy_keys_are_redacted_individually_in_opencode_events():
    secrets = opencode._secrets({'api_key': 'first-key\nsecond-key'})
    event = {'error': 'Authorization first-key failed', 'other': 'second-key'}
    clean = opencode._clean(event, secrets)
    assert 'first-key' not in str(clean)
    assert 'second-key' not in str(clean)
