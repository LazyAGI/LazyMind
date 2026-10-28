import json
from types import SimpleNamespace
from unittest.mock import MagicMock

import pytest

from lazymind.chat.engine.agent_runtime.budget import resolve_max_input_tokens


def test_input_limit_caps_the_context_without_changing_existing_context_semantics():
    config = {'llm': {'max_input_tokens': '1M', 'input_token_limit': '128K'}}
    assert resolve_max_input_tokens(llm_config=config) == 131072
    config['llm']['input_token_limit'] = '2M'
    assert resolve_max_input_tokens(llm_config=config) == 1048576
    del config['llm']['input_token_limit']
    assert resolve_max_input_tokens(llm_config=config) == 1048576
    config['llm']['input_token_limit'] = '128K'
    assert resolve_max_input_tokens('2M', llm_config=config) == 131072


@pytest.fixture
def provider_calls(monkeypatch):
    import requests

    calls = []

    def post(_url, **kwargs):
        calls.append(kwargs['json'])
        response = MagicMock(status_code=200)
        response.text = json.dumps({'choices': [{
            'message': {'role': 'assistant', 'content': 'ok', 'reasoning_content': 'separate reasoning'},
            'finish_reason': 'stop',
        }]})
        response.__enter__.return_value = response
        return response
    monkeypatch.setattr(requests, 'post', post)
    return calls


@pytest.mark.parametrize(('requested', 'expected'), [(65536, 32768), (8192, 8192)])
def test_output_limit_uses_a_copy_and_reaches_the_provider_request(provider_calls, requested, expected):
    from lazyllm.common import new_session
    from lazyllm.module.llms.onlinemodule.chat import OnlineChatModule
    from lazymind.model_config import inject_model_config
    from lazymind.model_token_limits import bounded_model_for_role

    calls = provider_calls
    config = {'llm': {'source': 'openai', 'model': 'fixture', 'base_url': 'https://fixture.test/v1/',
                      'api_key': 'fixture-key', 'max_output_tokens': '32K'}}
    with new_session('model-token-limit-test'):
        inject_model_config(config)
        original = OnlineChatModule(source='dynamic', name='llm', dynamic_auth=True,
                                    stream=False, static_params={'max_tokens': requested})
        limited = bounded_model_for_role(original, config)
        assert limited is not original
        assert original._static_params == {'max_tokens': requested}
        limited('hello', stream_output=False)
        assert calls[-1]['max_tokens'] == expected
        assert 'max_output_tokens' not in calls[-1]
        assert 'input_token_limit' not in calls[-1]
        assert bounded_model_for_role(original, {'other': config['llm']}) is original


def test_output_limit_is_used_at_the_conversation_call_boundary(provider_calls):
    from lazyllm.common import new_session
    from lazymind.conversation import model_client
    from lazymind.model_config import inject_model_config

    calls = provider_calls
    config = {'llm': {'source': 'openai', 'model': 'private-model', 'base_url': 'https://fixture.test/v1/',
                      'api_key': 'fixture-key', 'max_output_tokens': '32K'}}
    with new_session('conversation-output-limit-test'):
        inject_model_config(config)
        request = SimpleNamespace(options={}, llm_config=config)
        assert model_client.call_model(request, 'hello', stream_output=False, max_tokens=65536) == 'ok'
        assert calls[-1]['max_tokens'] == 32768
        assert model_client.call_model(request, 'hello', stream_output=False, max_tokens=8192) == 'ok'
        assert calls[-1]['max_tokens'] == 8192
        request.llm_config['llm'].pop('max_output_tokens')
        assert model_client.call_model(request, 'hello', stream_output=False) == 'ok'
        assert 'max_tokens' not in calls[-1]
    assert len(calls) == 3
