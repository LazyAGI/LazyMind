"""Feature permissions are enforced after any gateway extras are merged."""
import pytest


def test_disabled_tools_are_removed_from_openai_and_native_qwen_without_mutation():
    from lazymind.model_features import apply_model_features
    body = {'messages': [{'role': 'user', 'content': 'Hi'}], 'tools': [{'type': 'function'}],
            'tool_choice': 'auto', 'functions': [{}], 'function_call': 'auto',
            'parameters': {'tools': [{}], 'tool_choice': 'required', 'temperature': .1}}
    result = apply_model_features(body, {'tool_calling': False}, 'qwen')
    assert not ({'tools', 'tool_choice', 'functions', 'function_call'} & result.keys())
    assert result['parameters'] == {'temperature': .1}
    assert body['tools'] == [{'type': 'function'}]
    assert result['messages'] == body['messages']


def test_reasoning_disabled_removes_explicit_requests_in_all_known_envelopes():
    from lazymind.model_features import apply_model_features
    body = {'model': 'reasoner', 'reasoning': {'effort': 'high'}, 'thinking': {'type': 'enabled'},
            'reasoning_effort': 'high', 'enable_thinking': True, 'include_reasoning': True,
            'parameters': {'enable_thinking': True, 'temperature': .2},
            'chat_template_kwargs': {'enable_thinking': True, 'other': 1},
            'extra_body': {'thinking': {'type': 'enabled'}}}
    result = apply_model_features(body, {'reasoning': False}, 'openai')
    assert result == {'model': 'reasoner', 'parameters': {'temperature': .2},
                      'chat_template_kwargs': {'other': 1}, 'extra_body': {}}


@pytest.mark.parametrize('features', [{}, {'reasoning': True, 'tool_calling': True}])
def test_unknown_and_enabled_permissions_preserve_adapter_requests(features):
    from lazymind.model_features import apply_model_features
    body = {'tools': [{}], 'thinking': {'type': 'enabled'}}
    assert apply_model_features(body, features, 'claude') == body


def test_model_feature_permission_reads_explicit_request_config():
    from lazymind.model_features import feature_is_enabled
    assert feature_is_enabled('tool_calling', {'llm': {'features': {'tool_calling': False}}}) is False
    assert feature_is_enabled('tool_calling', {'llm': {'features': {}}}) is True
