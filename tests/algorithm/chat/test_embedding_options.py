"""Embedding options reach inference without changing source text or shared suppliers."""
import json
from unittest.mock import patch

import lazyllm
import pytest
import requests


def _capture_inference(config, inputs):
    from lazymind.embedding_options import EmbeddingModel
    from lazymind.model_config import inject_model_config

    bodies = []

    def post(_url, *, json, **_kwargs):
        bodies.append(json)
        response = requests.Response()
        response.status_code = 200
        values = json['input']['texts'] if isinstance(json['input'], dict) else json['input']
        rows = [{'embedding': [0.1, 0.2]} for _ in values]
        source = config['embed_main']['source'].lower()
        payload = {'output': {'embeddings': rows}} if source == 'qwen' else (
            {'embeddings': rows} if source == 'sensenova' else {'data': rows})
        response._content = __import__('json').dumps(payload).encode()
        return response

    with lazyllm.new_session():
        inject_model_config(config)
        inner = lazyllm.OnlineEmbeddingModule(source='dynamic', type='embed', name='embed_main', dynamic_auth=True)
        model = EmbeddingModel(inner, role='embed_main')
        with patch('requests.post', side_effect=post), patch('requests.Session.post', side_effect=post):
            results = [model(value) for value in inputs]
        return bodies, results, inner


def test_query_and_passage_prefixes_apply_once_to_each_input_and_batch_reaches_http():
    from lazymind.embedding_options import embedding_query

    passages = ['first', 'second', 'third']
    config = {'embed_main': {'type': 'embed', 'source': 'openai', 'model': 'fixture-vector',
                             'base_url': 'https://provider.example/v1/', 'api_key': 'test-key',
                             'embedding': {'query_prefix': 'query: ', 'passage_prefix': 'passage: ', 'batch_size': 2}}}
    bodies, results, inner = _capture_inference(config, [embedding_query('question'), passages, 'single passage'])

    assert [body['input'] for body in bodies] == [
        ['query: question'], ['passage: first', 'passage: second'], ['passage: third'], ['passage: single passage'],
    ]
    assert all(body['model'] == 'fixture-vector' for body in bodies)
    assert results == [[0.1, 0.2], [[0.1, 0.2]] * 3, [0.1, 0.2]]
    assert passages == ['first', 'second', 'third']
    assert inner.batch_size == 32
    assert inner._suppliers == {}


def test_prefix_whitespace_and_newlines_are_exact_and_unconfigured_role_has_no_stale_options():
    from lazymind.embedding_options import embedding_query

    base = {'type': 'embed', 'source': 'openai', 'model': 'fixture-vector',
            'base_url': 'https://provider.example/v1/', 'api_key': 'test-key'}
    bodies, _, _ = _capture_inference({'embed_main': base | {'embedding': {
        'query_prefix': '  query\n', 'passage_prefix': '\n passage  ', 'batch_size': 1,
    }}}, [embedding_query('text'), 'text'])
    assert [body['input'] for body in bodies] == [['  query\ntext'], ['\n passage  text']]
    bodies, _, _ = _capture_inference({'embed_main': base}, [embedding_query('text'), 'text'])
    assert [body['input'] for body in bodies] == [['text'], ['text']]


@pytest.mark.parametrize(('source', 'url'), [
    ('qwen', 'https://provider.example/api/v1/'),
    ('sensenova', 'https://provider.example/v1/llm/'),
])
def test_native_embedding_adapters_receive_prefix_and_per_call_batch_size(source, url):
    bodies, results, _ = _capture_inference({'embed_main': {
        'type': 'embed', 'source': source, 'model': 'fixture-vector', 'base_url': url, 'api_key': 'sk-test-fixture',
        'embedding': {'passage_prefix': 'p: ', 'batch_size': 2},
    }}, [['first', 'second', 'third']])
    inputs = [body['input']['texts'] if source == 'qwen' else body['input'] for body in bodies]
    assert inputs == [['p: first', 'p: second'], ['p: third']]
    assert results == [[[0.1, 0.2]] * 3]


@pytest.mark.parametrize('options', [
    {'batch_size': 0}, {'batch_size': 2049}, {'batch_size': True}, {'batch_size': 1.5},
    {'query_prefix': 'x' * 2049}, {'passage_prefix': 12}, {'unknown': 'x'},
])
def test_invalid_options_fail_before_inference(options):
    from lazymind.embedding_options import configure_embedding_options

    with lazyllm.new_session(), pytest.raises(ValueError):
        configure_embedding_options({'embed_main': {'type': 'embed', 'embedding': options}})


def test_query_marker_survives_serialization_without_putting_prefix_in_search_text():
    import cloudpickle
    from lazymind.embedding_options import embedding_query

    query = cloudpickle.loads(cloudpickle.dumps(embedding_query('find this')))
    assert query == 'find this'
    bodies, _, _ = _capture_inference({'embed_main': {
        'type': 'embed', 'source': 'openai', 'model': 'fixture-vector',
        'base_url': 'https://provider.example/v1/', 'api_key': 'test-key',
        'embedding': {'query_prefix': 'q: ', 'passage_prefix': 'p: '},
    }}, [query])
    assert bodies[0]['input'] == ['q: find this']


def test_model_wrapper_is_cloudpickle_safe():
    import cloudpickle
    from lazymind.embedding_options import EmbeddingModel

    inner = lazyllm.OnlineEmbeddingModule(source='dynamic', type='embed', name='embed_main', dynamic_auth=True)
    restored = cloudpickle.loads(cloudpickle.dumps(EmbeddingModel(inner, role='embed_main')))
    assert isinstance(restored, lazyllm.ModuleBase)
    assert restored.model.name == 'embed_main'
    assert json.loads(json.dumps({'batch_size': restored.batch_size})) == {'batch_size': 32}


def test_persistent_query_dispatch_keeps_lazy_document_embeddings_on_passage_prefix():
    from lazyllm.module import ModuleBase
    from lazyllm.tools.rag.default_index import DefaultIndex
    from lazyllm.tools.rag.doc_node import DocNode
    from lazymind.embedding_options import EmbeddingModel, configure_embedding_options
    from lazymind.parsing.service.build_document import _quiet_trace

    inputs = []

    class Recorder(ModuleBase):
        batch_size = 32

        def forward(self, value):
            inputs.append(value)
            return [1.0, 0.0] if isinstance(value, str) else [[1.0, 0.0] for _ in value]

    node = DocNode(text='source text')

    class Store:
        def get_nodes(self, group):
            return [node]

        def update_nodes(self, nodes):
            pass

    with lazyllm.new_session():
        configure_embedding_options({'embed_main': {'type': 'embed', 'embedding': {
            'query_prefix': 'q: ', 'passage_prefix': 'p: ', 'batch_size': 2,
        }}})
        index = DefaultIndex({'dense': EmbeddingModel(Recorder(), role='embed_main')}, Store())

        def retrieve(operation, **kwargs):
            assert operation == 'retrieve'
            return index.query(**kwargs, group_name='block', similarity_name='cosine',
                               similarity_cut_off=-1.0, topk=3)

        nodes = _quiet_trace({'kb': retrieve})('kb', 'retrieve', query='question')
    assert inputs == ['q: question', ['p: source text']]
    assert nodes[0].text == 'source text'


def test_embedding_options_and_advanced_transport_both_reach_batched_http():
    from urllib.parse import parse_qs, urlsplit
    from lazymind.embedding_options import EmbeddingModel
    from lazymind.model_config import inject_model_config

    captured = []

    def send(_session, prepared, **_kwargs):
        captured.append(prepared)
        body = json.loads(prepared.body)
        response = requests.Response()
        response.status_code = 200
        response._content = json.dumps({'data': [{'embedding': [1.0, 0.0]} for _ in body['input']]}).encode()
        return response

    config = {'embed_main': {'type': 'embed', 'source': 'openai', 'model': 'fixture-vector',
                             'base_url': 'https://provider.example/v1/', 'api_key': 'test-key',
                             'embedding': {'passage_prefix': 'p: ', 'batch_size': 2},
                             'request_advanced': {'headers': {'X-Tenant': 'sample'}, 'query': {'version': '42'},
                                                  'extra_body': {'user': 'sample'}}}}
    with lazyllm.new_session(), patch('requests.Session.send', send):
        inject_model_config(config)
        inner = lazyllm.OnlineEmbeddingModule(source='dynamic', type='embed', name='embed_main', dynamic_auth=True)
        output = EmbeddingModel(inner, role='embed_main')(['first', 'second', 'third'])
    assert output == [[1.0, 0.0]] * 3
    assert [json.loads(item.body)['input'] for item in captured] == [['p: first', 'p: second'], ['p: third']]
    assert all(item.headers['X-Tenant'] == 'sample' for item in captured)
    assert all(parse_qs(urlsplit(item.url).query) == {'version': ['42']} for item in captured)
    assert all(json.loads(item.body)['user'] == 'sample' for item in captured)
    assert inner._suppliers == {}


def test_temporary_vectors_refresh_for_prefix_changes_but_not_batch_size(monkeypatch):
    from types import SimpleNamespace
    from lazymind.chat.engine.tools.algo import search_temp
    from lazymind.embedding_options import configure_embedding_options

    monkeypatch.setattr(search_temp, '_session_retrievers', search_temp.OrderedDict())
    monkeypatch.setattr(search_temp, 'temporary_retrieval_scope', lambda: 'test-conversation')
    monkeypatch.setattr(search_temp, 'embed_available', lambda: True)
    monkeypatch.setattr(search_temp, '_make_retriever', lambda **kwargs: SimpleNamespace(**kwargs))
    with lazyllm.new_session():
        configure_embedding_options({'embed_main': {'type': 'embed', 'embedding': {'passage_prefix': 'p: '}}})
        first = search_temp._ensure_retriever()
        configure_embedding_options({'embed_main': {'type': 'embed', 'embedding': {
            'passage_prefix': 'p: ', 'batch_size': 16,
        }}})
        assert search_temp._ensure_retriever() is first
        configure_embedding_options({'embed_main': {'type': 'embed', 'embedding': {'passage_prefix': 'document: '}}})
        changed = search_temp._ensure_retriever()
        assert changed is not first
        assert changed.use_embed is True


def test_temporary_bm25_cache_keeps_conversations_pinned_when_embedding_options_change(monkeypatch):
    from types import SimpleNamespace
    from lazymind.chat.engine.tools.algo import search_temp
    from lazymind.embedding_options import configure_embedding_options

    monkeypatch.setattr(search_temp, '_session_retrievers', search_temp.OrderedDict())
    monkeypatch.setattr(search_temp, 'temporary_retrieval_scope', lambda: 'test-conversation')
    monkeypatch.setattr(search_temp, 'embed_available', lambda: False)
    monkeypatch.setattr(search_temp, '_make_retriever', lambda **kwargs: SimpleNamespace(**kwargs))
    with lazyllm.new_session():
        configure_embedding_options({})
        first = search_temp._ensure_retriever()
        configure_embedding_options({'embed_main': {'type': 'embed', 'embedding': {'passage_prefix': 'p: '}}})
        assert search_temp._ensure_retriever() is first
        assert first.use_embed is False


def test_central_model_config_injection_clears_private_execution_options_on_none():
    from lazymind.model_config import inject_model_config

    with lazyllm.new_session():
        inject_model_config({'embed_main': {
            'type': 'embed', 'source': 'openai', 'model': 'fixture-vector',
            'base_url': 'https://provider.example/v1/', 'embedding': {'passage_prefix': 'p: '},
            'request_advanced': {'headers': {'X-Tenant': 'test'}},
        }})
        assert lazyllm.globals['lazymind_embedding_options']['embed_main'] == {'passage_prefix': 'p: '}
        assert lazyllm.globals['lazymind_request_advanced']['embed_main']['advanced']['headers'] == {'X-Tenant': 'test'}
        inject_model_config(None)
        assert lazyllm.globals['lazymind_embedding_options'] == {}
        assert lazyllm.globals['lazymind_request_advanced'] == {}


@pytest.mark.parametrize(('field', 'changed'), [
    ('source', 'qwen'), ('model', 'new-vector'), ('base_url', 'https://other.example/v1/'),
])
def test_temporary_vectors_refresh_when_embedding_route_changes(monkeypatch, field, changed):
    from types import SimpleNamespace
    from lazymind.chat.engine.tools.algo import search_temp
    from lazymind.model_config import inject_model_config

    monkeypatch.setattr(search_temp, '_session_retrievers', search_temp.OrderedDict())
    monkeypatch.setattr(search_temp, 'temporary_retrieval_scope', lambda: 'test-conversation')
    monkeypatch.setattr(search_temp, 'embed_available', lambda: True)
    monkeypatch.setattr(search_temp, '_make_retriever', lambda **kwargs: SimpleNamespace(**kwargs))
    config = {'type': 'embed', 'source': 'openai', 'model': 'fixture-vector',
              'base_url': 'https://provider.example/v1/'}
    with lazyllm.new_session():
        inject_model_config({'embed_main': config})
        first = search_temp._ensure_retriever()
        inject_model_config({'embed_main': config | {field: changed}})
        assert search_temp._ensure_retriever() is not first
