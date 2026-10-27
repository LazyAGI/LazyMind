import pytest

from evo.repair_model import EvoModelConfigError, MODEL_NOT_ALLOWED, opencode_settings, resolve_evo_model


def role(**extra):
    return {
        'source': 'openai', 'model': 'example', 'base_url': 'https://example.test/v1', 'api_key': 'selected-key',
        'opencode': {'provider': 'openai', 'provider_model': 'example', 'model': 'openai/example',
                     'npm': '@ai-sdk/openai', 'base_url': 'https://example.test/v1'},
        **extra,
    }


@pytest.mark.parametrize('advanced', [
    {'headers': {'X-Gateway-Token': 'private-header-value'}},
    {'query': {'api_key': 'private-query-value'}},
    {'extra_body': {'metadata': {'project': 'private-body-value'}}},
    {'headers': 'private-invalid-value'},
])
def test_evolution_rejects_request_options_before_building_an_incomplete_opencode_config(advanced):
    with pytest.raises(EvoModelConfigError) as caught:
        opencode_settings(role(request_advanced=advanced))
    assert caught.value.code == MODEL_NOT_ALLOWED
    assert caught.value.reason == 'advanced_request_options_unsupported'
    assert 'private-' not in str(caught.value.detail())
    assert 'private-' not in str(caught.value)


def test_evolution_requires_tool_calling_even_for_external_runtime_configuration():
    with pytest.raises(EvoModelConfigError) as caught:
        resolve_evo_model(role(features={'tool_calling': False}))
    assert caught.value.code == MODEL_NOT_ALLOWED
    assert caught.value.reason == 'tool_calling_required'


@pytest.mark.parametrize('advanced', [{}, {'headers': {}, 'query': {}, 'extra_body': {}}])
def test_empty_request_options_keep_existing_opencode_configuration_working(advanced):
    assert opencode_settings(role(request_advanced=advanced))['api_key'] == 'selected-key'
