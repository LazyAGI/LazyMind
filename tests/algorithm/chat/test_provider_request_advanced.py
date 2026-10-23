import json

import pytest
import requests

from lazymind.provider_request import apply_request_advanced, configure_request_advanced


def test_request_options_preserve_authentication_and_mandatory_body():
    result = apply_request_advanced(
        {'headers': {'X-Gateway-Token': 'private'}, 'query': {'tenant': 'a & b'},
         'extra_body': {'metadata': {'project': 'test'}}},
        method='POST', url='https://example.test/proxy/v1/chat/completions',
        headers={'Authorization': 'Bearer selected-key'}, json_body={'model': 'saved-model', 'messages': []},
    )
    assert result['headers'] == {'Authorization': 'Bearer selected-key', 'X-Gateway-Token': 'private'}
    assert result['url'] == 'https://example.test/proxy/v1/chat/completions?tenant=a+%26+b'
    assert result['json'] == {'model': 'saved-model', 'messages': [], 'metadata': {'project': 'test'}}
    assert 'json' not in apply_request_advanced(
        {'extra_body': {'metadata': 1}}, method='GET', url='https://example.test/models',
    )


@pytest.mark.parametrize('config', [
    {'headers': {'Authorization': 'replacement'}}, {'headers': {'X-Test': 'bad\r\nheader'}},
    {'extra_body': {'model': 'replacement'}}, {'extra_body': {'dimensions': 1}},
    {'query': {'a': 123}}, {'unknown': {}},
])
def test_request_options_reject_invalid_values(config):
    with pytest.raises(ValueError):
        apply_request_advanced(config, method='POST', url='https://example.test/v1', json_body={})


def test_extra_json_rejects_multipart_instead_of_ignoring_configuration():
    with pytest.raises(ValueError, match='JSON'):
        apply_request_advanced({'extra_body': {'metadata': {}}}, method='POST',
                               url='https://example.test/audio/transcriptions', files={'file': b'data'})


def test_serialized_sdk_json_keeps_input_and_merges_extension_fields():
    result = apply_request_advanced(
        {'extra_body': {'metadata': {'project': 'test'}}}, method='POST', url='https://example.test/api/v1',
        headers={'Content-Type': 'application/json'}, data=b'{"model":"saved","input":{"texts":["hi"]}}',
    )
    assert result['data'] is None
    assert result['json'] == {'model': 'saved', 'input': {'texts': ['hi']}, 'metadata': {'project': 'test'}}


def test_dynamic_runtime_applies_options_per_role_without_changing_supplier(monkeypatch):
    import lazyllm
    from lazyllm import AutoModel
    from lazyllm.common import new_session
    from lazyllm.module.llms.model_config_inject import inject_model_config

    calls = []

    def send(session, request, **kwargs):
        calls.append(request)
        response = requests.Response()
        response.status_code = 200
        response._content = json.dumps({'choices': [{'message': {'content': 'ok'}, 'finish_reason': 'stop'}]}).encode()
        response.request = request
        return response

    monkeypatch.setattr(requests.Session, 'send', send)
    with new_session('test-advanced-runtime'):
        config = {'llm': {'source': 'openai', 'model': 'private-model', 'api_key': 'selected-key',
                          'base_url': 'https://example.test/proxy/v1/',
                          'request_advanced': {'headers': {'X-Gateway-Token': 'first'},
                                               'query': {'tenant': 'one'}, 'extra_body': {'metadata': {'p': 1}}}}}
        inject_model_config(config)
        configure_request_advanced(config)
        model = AutoModel(source='dynamic', name='llm', dynamic_auth=True)
        assert model('hi', stream_output=False)['content'] == 'ok'
        config['llm']['request_advanced'] = {'headers': {'X-Gateway-Token': 'second'}}
        configure_request_advanced(config)
        assert model('hi', stream_output=False)['content'] == 'ok'
        configure_request_advanced({})
        assert model('hi', stream_output=False)['content'] == 'ok'
        assert 'lazymind_request_advanced' in lazyllm.globals
    assert calls[0].headers['X-Gateway-Token'] == 'first'
    assert calls[0].url.endswith('?tenant=one')
    assert json.loads(calls[0].body)['metadata'] == {'p': 1}
    assert calls[1].headers['X-Gateway-Token'] == 'second'
    assert 'tenant=' not in calls[1].url
    assert 'X-Gateway-Token' not in calls[2].headers


def test_runtime_gateway_credentials_stay_inside_supplier_origin_and_path(monkeypatch):
    from lazyllm import AutoModel
    from lazyllm.common import new_session
    from lazyllm.module.llms.model_config_inject import inject_model_config

    calls = []

    def send(session, request, **kwargs):
        calls.append((request, kwargs))
        response = requests.Response()
        response.status_code = 200
        response._content = b'{}'
        response.request = request
        return response

    class Supplier:
        def forward(self, *args, **kwargs):
            requests.get('https://media.example.test/file')
            requests.get('https://gateway.example.test/other-tenant/file')
            requests.get('https://gateway.example.test/tenant/v1/%2e%2e/%2e%2e/other-tenant/file')
            requests.post('https://gateway.example.test/tenant/v1/embeddings', json={'model': 'saved', 'input': []})
            return 'ok'

    monkeypatch.setattr(requests.Session, 'send', send)
    with new_session('advanced-scope'):
        config = {'llm': {'source': 'openai', 'model': 'saved', 'api_key': 'saved-key',
                          'base_url': 'https://gateway.example.test/tenant/v1/',
                          'request_advanced': {'headers': {'X-Private': 'secret'}, 'query': {'token': 'secret'}}}}
        inject_model_config(config)
        configure_request_advanced(config)
        module = AutoModel(source='dynamic', name='llm', dynamic_auth=True)
        monkeypatch.setattr(module, '_get_supplier', lambda: Supplier())
        assert module('hi') == 'ok'
    assert len(calls) == 4
    for request, _ in calls[:3]:
        assert 'X-Private' not in request.headers
        assert 'token=' not in request.url
    assert calls[3][0].headers['X-Private'] == 'secret'
    assert calls[3][0].url.endswith('?token=secret')
    assert calls[3][1]['allow_redirects'] is False


def test_runtime_feature_disables_apply_after_custom_body_even_without_gateway_options(monkeypatch):
    from lazyllm import AutoModel
    from lazyllm.common import new_session
    from lazyllm.module.llms.model_config_inject import inject_model_config

    bodies = []

    def send(session, request, **kwargs):
        bodies.append(json.loads(request.body))
        response = requests.Response()
        response.status_code = 200
        response._content = b'{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}'
        response.request = request
        return response

    monkeypatch.setattr(requests.Session, 'send', send)
    with new_session('advanced-features'):
        config = {'llm': {'source': 'openai', 'model': 'saved', 'api_key': 'saved-key',
                          'features': {'tool_calling': False}}}
        inject_model_config(config)
        configure_request_advanced(config)
        model = AutoModel(source='dynamic', name='llm', dynamic_auth=True)
        model('hi', tools=[{'type': 'function', 'function': {'name': 'test', 'parameters': {}}}],
              tool_choice='required', stream_output=False)
        config['llm']['request_advanced'] = {'extra_body': {
            'tools': [{'name': 'injected'}], 'tool_choice': 'required',
        }}
        config['llm']['base_url'] = 'https://api.openai.com/v1/'
        configure_request_advanced(config)
        model('hi', stream_output=False)
    for body in bodies:
        assert 'tools' not in body
        assert 'tool_choice' not in body


@pytest.mark.parametrize('status,payload,expected', [
    (401, {'error': {'message': 'echo synthetic-private-token', 'code': 'synthetic-private-token'}},
     'authentication_failed'),
    (401, 'echo synthetic-private-token', 'authentication_failed'),
    (429, {'error': {'message': 'echo synthetic-private-token', 'code': 'insufficient_quota'}}, 'quota_exceeded'),
    (429, {'error': {'message': 'echo synthetic-private-token', 'code': 'rate_limit_exceeded'}}, 'rate_limited'),
])
def test_embedding_http_errors_are_sanitized_before_adapter_logging(monkeypatch, capfd, status, payload, expected):
    from lazyllm.module.llms.onlinemodule.embedding import OnlineEmbeddingModule
    from lazyllm.common import new_session
    from lazyllm.module.llms.model_config_inject import inject_model_config

    def send(session, request, **kwargs):
        response = requests.Response()
        response.status_code = status
        response._content = json.dumps(payload).encode()
        response.reason = 'synthetic-private-token'
        response.headers['X-Echo'] = 'synthetic-private-token'
        response.request = request
        response.url = request.url
        return response

    monkeypatch.setattr(requests.Session, 'send', send)
    with new_session('advanced-embedding-safe-error'):
        config = {'embed_main': {'source': 'openai', 'type': 'embed', 'model': 'text-embedding-3-small',
                                 'api_key': 'selected-key', 'base_url': 'https://example.test/v1/',
                                 'request_advanced': {'headers': {'X-Private': 'synthetic-private-token'},
                                                      'query': {'token': 'synthetic-private-token'}}}}
        inject_model_config(config)
        configure_request_advanced(config)
        module = OnlineEmbeddingModule(source='dynamic', type='embed', name='embed_main', dynamic_auth=True)
        with pytest.raises(Exception) as caught:
            module('test input')
    captured = capfd.readouterr()
    assert 'synthetic-private-token' not in str(caught.value)
    assert 'synthetic-private-token' not in repr(caught.value)
    assert 'synthetic-private-token' not in captured.out + captured.err
    assert str(status) in str(caught.value)
    assert expected in str(caught.value)


def test_supplier_failure_before_http_does_not_expose_private_config(monkeypatch, capfd):
    from lazyllm import AutoModel
    from lazyllm.common import new_session
    from lazyllm.module.llms.model_config_inject import inject_model_config

    class Supplier:
        def forward(self, *args, **kwargs):
            raise RuntimeError('echo synthetic-private-token')

    with new_session('advanced-before-http-safe-error'):
        config = {'llm': {'source': 'openai', 'model': 'saved', 'api_key': 'saved-key',
                          'base_url': 'https://example.test/v1/',
                          'request_advanced': {'headers': {'X-Private': 'synthetic-private-token'}}}}
        inject_model_config(config)
        configure_request_advanced(config)
        module = AutoModel(source='dynamic', name='llm', dynamic_auth=True)
        monkeypatch.setattr(module, '_get_supplier', lambda: Supplier())
        with pytest.raises(Exception) as caught:
            module('test')
    captured = capfd.readouterr()
    assert 'synthetic-private-token' not in str(caught.value)
    assert 'synthetic-private-token' not in captured.out + captured.err


@pytest.mark.parametrize('kind,model', [('text2image', 'cogview-4-250304'), ('stt', 'glm-asr')])
def test_unsupported_native_sdk_is_rejected_before_supplier_call(monkeypatch, kind, model):
    from lazyllm.module.llms.onlinemodule.multimodal import OnlineMultiModalModule
    from lazyllm.common import new_session
    from lazyllm.module.llms.model_config_inject import inject_model_config

    constructed = []

    class Supplier:
        def forward(self, *args, **kwargs):
            constructed.append('called')
            return 'charged but unconfigured'

    with new_session('advanced-native-sdk-preflight'):
        config = {'test_role': {'source': 'glm', 'type': kind, 'model': model, 'api_key': 'saved-key',
                                'base_url': 'https://example.test/v4/',
                                'request_advanced': {'headers': {'X-Private': 'synthetic-private-token'}}}}
        inject_model_config(config)
        configure_request_advanced(config)
        module = OnlineMultiModalModule(source='dynamic', type=kind, name='test_role', dynamic_auth=True)

        def build():
            constructed.append('constructed')
            return Supplier()
        monkeypatch.setattr(module, '_get_supplier', build)
        with pytest.raises(Exception, match='unsupported'):
            module('test')
    assert constructed == []


def test_chat_stream_error_keeps_category_without_reading_or_exposing_raw_body(monkeypatch, capfd):
    from lazyllm import AutoModel
    from lazyllm.common import new_session
    from lazyllm.module.llms.model_config_inject import inject_model_config
    from lazyllm.module.llms.onlinemodule.base.model_outcome import ModelCallError

    closed = []

    class RawError:
        def read(self, *args, **kwargs):
            pytest.fail('streamed provider error body must not be read')

        def close(self):
            closed.append(True)

    def send(session, request, **kwargs):
        response = requests.Response()
        response.status_code = 401
        response.raw = RawError()
        response.request = request
        response.url = request.url
        return response

    monkeypatch.setattr(requests.Session, 'send', send)
    with new_session('advanced-stream-safe-error'):
        config = {'llm': {'source': 'openai', 'model': 'saved', 'api_key': 'saved-key',
                          'base_url': 'https://example.test/v1/',
                          'request_advanced': {'query': {'token': 'synthetic-private-token'}}}}
        inject_model_config(config)
        configure_request_advanced(config)
        module = AutoModel(source='dynamic', name='llm', dynamic_auth=True)
        with pytest.raises(ModelCallError) as caught:
            module('test', stream_output=False)
    assert caught.value.terminal.failure.code.value == 'authentication_failed'
    assert caught.value.terminal.failure.provider_http_status == 401
    assert closed == [True]
    captured = capfd.readouterr()
    assert 'synthetic-private-token' not in captured.out + captured.err + repr(caught.value)


@pytest.mark.parametrize('source,provider_code,expected', [
    ('openai', 'insufficient_quota', 'quota_exhausted'),
    ('openai', 'rate_limit_exceeded', 'rate_limited'),
    ('openai', 'synthetic-private-token', 'rate_limited'),
    ('glm', '1308', 'quota_exhausted'),
])
def test_cached_chat_errors_preserve_safe_provider_category(monkeypatch, capfd, source, provider_code, expected):
    from lazyllm import AutoModel
    from lazyllm.common import new_session
    from lazyllm.module.llms.model_config_inject import inject_model_config
    from lazyllm.module.llms.onlinemodule.base.model_outcome import ModelCallError

    def send(session, request, **kwargs):
        response = requests.Response()
        response.status_code = 429
        response._content = json.dumps({'error': {
            'code': provider_code, 'message': 'echo synthetic-private-token',
        }}).encode()
        response.request = request
        response.url = request.url
        return response

    monkeypatch.setattr(requests.Session, 'send', send)
    with new_session('advanced-cached-safe-category'):
        config = {'llm': {'source': source, 'model': 'saved', 'api_key': 'saved-key',
                          'base_url': 'https://example.test/v1/',
                          'request_advanced': {'query': {'token': 'synthetic-private-token'}}}}
        inject_model_config(config)
        configure_request_advanced(config)
        module = AutoModel(source='dynamic', name='llm', dynamic_auth=True)
        with pytest.raises(ModelCallError) as caught:
            module('test', stream_output=False, max_retries=1)
    assert caught.value.terminal.failure.code.value == expected
    assert caught.value.terminal.failure.provider_http_status == 429
    captured = capfd.readouterr()
    assert 'synthetic-private-token' not in captured.out + captured.err + repr(caught.value)


def test_runtime_preserves_explicit_unsupported_json_transport_reason(monkeypatch):
    from lazyllm.common import new_session
    from lazyllm.module.llms.model_config_inject import inject_model_config
    from lazyllm.module.llms.onlinemodule.multimodal import OnlineMultiModalModule

    class Supplier:
        def forward(self, *args, **kwargs):
            return requests.post('https://example.test/v1/audio/transcriptions', files={'file': b'audio'})

    def send(*args, **kwargs):
        pytest.fail('unsupported JSON body must fail before HTTP')

    monkeypatch.setattr(requests.Session, 'send', send)
    with new_session('advanced-multipart-configuration-error'):
        config = {'test_role': {'source': 'siliconflow', 'type': 'stt', 'model': 'test-stt', 'api_key': 'saved-key',
                                'base_url': 'https://example.test/v1/',
                                'request_advanced': {'extra_body': {'metadata': {'project': 'test'}}}}}
        inject_model_config(config)
        configure_request_advanced(config)
        module = OnlineMultiModalModule(source='dynamic', type='stt', name='test_role', dynamic_auth=True)
        monkeypatch.setattr(module, '_get_supplier', lambda: Supplier())
        with pytest.raises(Exception, match='extra JSON body is unsupported'):
            module('test')
