from typing import Any

import pytest


def _globals_config_patch(key: str, value: Any):
    '''Context manager: temporarily set lazyllm.globals["config"][key] = value.'''
    import lazyllm
    from contextlib import contextmanager

    @contextmanager
    def _ctx():
        cfg = lazyllm.globals['config']
        old = cfg.get(key)
        cfg[key] = value
        try:
            yield cfg
        finally:
            cfg[key] = old

    return _ctx()


@pytest.mark.parametrize('source,model,base_url,kind,expected', [
    ('openai', 'gpt-6.1-sol', 'https://api.openai.com/v1', 'llm', 'openai_responses'),
    ('openai', 'gpt-6-sol', '', 'llm', 'openai_responses'),
    ('openai', 'gpt-6.1-sol', 'https://proxy.example/v1', 'llm', 'openai'),
    ('openai', 'gpt-4o', 'https://api.openai.com/v1', 'llm', 'openai'),
    ('openai', 'gpt-6.1-sol', 'https://api.openai.com/v1', 'embed', 'openai'),
    ('openai_responses', 'gpt-6.1-sol', 'https://proxy.example/v1', 'llm', 'openai_responses'),
])
def test_injected_transport_preserves_provider_boundaries(monkeypatch, source, model, base_url, kind, expected):
    from lazymind.model_config import inject_model_config
    monkeypatch.setattr('lazymind.model_config.get_dynamic_role_slot_map', lambda: {'llm': 'chat'})
    original = {'llm': {'source': source, 'model': model, 'base_url': base_url,
                        'type': kind, 'api_key': 'test-key'}}
    with _globals_config_patch('dynamic_model_configs', None) as config:
        inject_model_config(original)
        slot = 'embed' if kind == 'embed' else 'chat'
        assert config['dynamic_model_configs']['llm'][slot]['source'] == expected
    assert original['llm']['source'] == source


def test_dynamic_chat_uses_responses_and_recovers_completed_text(monkeypatch, tmp_path):
    import json
    import uuid
    import lazyllm
    import requests
    from lazyllm import AutoModel
    from lazymind.model_config import inject_model_config

    class Response:
        status_code = 200
        headers = {}
        raw = type('Raw', (), {})()

        def __enter__(self):
            return self

        def __exit__(self, *args):
            return None

        def iter_lines(self):
            event = {'type': 'response.completed', 'response': {'output': [
                {'type': 'message', 'role': 'assistant', 'content': [{'type': 'output_text', 'text': 'Ready'}]},
            ]}}
            yield ('data: ' + json.dumps(event)).encode()

    calls = []

    def post(url, **kwargs):
        calls.append((url, kwargs['json']))
        return Response()

    monkeypatch.setattr(requests, 'post', post)
    monkeypatch.setattr('lazymind.model_config.get_dynamic_role_slot_map', lambda: {'llm': 'chat'})
    sid = 'responses-routing-' + uuid.uuid4().hex
    try:
        with lazyllm.globals._bind_sid(sid), lazyllm.locals._scope():
            inject_model_config({'llm': {
                'source': 'openai', 'model': 'gpt-6.1-sol',
                'base_url': 'https://api.openai.com/v1', 'api_key': 'test-key',
            }})
            config = tmp_path / 'models.yaml'
            config.write_text('llm:\n  source: dynamic\n  dynamic_auth: true\n  type: llm\n')
            model = AutoModel(model='llm', config=str(config), stream=True)
            # The shared IR supplies the assistant role for both transport modes,
            # so the default text formatter has one stable return type.
            assert model('Hello', stream_output=True) == 'Ready'
            assert calls[0][0] == 'https://api.openai.com/v1/responses'
            assert calls[0][1]['model'] == 'gpt-6.1-sol'
            assert 'input' in calls[0][1] and 'messages' not in calls[0][1]
    finally:
        lazyllm.globals._clear_sid(sid)
