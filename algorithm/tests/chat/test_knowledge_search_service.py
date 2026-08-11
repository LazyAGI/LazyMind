import pytest
import httpx
from fastapi import FastAPI
from fastapi.testclient import TestClient

from lazymind.chat.api import knowledge_search_routes as routes
from lazymind.chat.api.knowledge_search_routes import router
from lazymind.chat.app import create_app
from lazymind.chat.service import knowledge_search_service as svc
from lazymind.router.api import proxy_routes


class Node:
    def __init__(self, **kwargs):
        self.__dict__.update(kwargs)


def test_search_calls_search_kb_without_chat_agent(monkeypatch):
    calls = {}

    monkeypatch.setattr(svc, '_ensure_kb_search_runtime', lambda: (['retriever'], None, 'image'))

    def fake_search_kb(payload, **kwargs):
        calls['payload'] = payload
        calls['kwargs'] = kwargs
        return [
            Node(
                uid='chunk-1',
                text='hello',
                relevance_score=0.42,
                global_metadata={'kb_id': 'kb_backend_901', 'docid': 'lazy_doc_1', 'file_name': 'doc.txt'},
            )
        ]

    monkeypatch.setattr(svc, 'search_kb', fake_search_kb)

    hits = svc.search('user-1', ' query ', ['kb_backend_901'], 3)

    assert calls['payload']['user_id'] == 'user-1'
    assert calls['payload']['filters']['kb_id'] == ['kb_backend_901']
    assert calls['payload']['query'] == 'query'
    assert calls['kwargs']['k_max'] == 3
    assert calls['kwargs']['image_topk'] == 0
    assert hits == [
        svc.KnowledgeSearchHit(
            kb_id='kb_backend_901',
            doc_id='lazy_doc_1',
            chunk_id='chunk-1',
            text='hello',
            score=0.42,
            title='doc.txt',
            source_url='',
        )
    ]


def test_search_normalizes_topk_and_empty_hits(monkeypatch):
    captured = {}
    monkeypatch.setattr(svc, '_ensure_kb_search_runtime', lambda: ([], None, None))

    def fake_search_kb(payload, **kwargs):
        captured['k_max'] = kwargs['k_max']
        return []

    monkeypatch.setattr(svc, 'search_kb', fake_search_kb)

    assert svc.search('user-1', 'q', ['kb'], 0) == []
    assert captured['k_max'] == 10
    svc.search('user-1', 'q', ['kb'], 500)
    assert captured['k_max'] == 50


def test_search_filters_image_only_and_unsafe_fields(monkeypatch):
    monkeypatch.setattr(svc, '_ensure_kb_search_runtime', lambda: ([], None, None))
    monkeypatch.setattr(
        svc,
        'search_kb',
        lambda payload, **kwargs: {
            'items': [
                {
                    'kb_id': 'kb',
                    'docid': 'lazy-image',
                    'uid': 'img',
                    'group': 'image',
                    'text': '/var/lib/lazymind/uploads/a.png',
                    'local_path': '/var/lib/lazymind/uploads/a.png',
                },
                {
                    'kb_id': 'kb',
                    'docid': 'lazy-doc',
                    'uid': 'chunk',
                    'text': 'text',
                    'score': 7,
                    'file_name': 'doc.txt',
                    'source_url': 'file:///tmp/doc.txt',
                    'metadata': {'local_path': '/tmp/doc.txt'},
                    'global_metadata': {'secret': 'x'},
                },
            ]
        },
    )

    hits = svc.search('user-1', 'q', ['kb'], 10)

    assert len(hits) == 1
    assert hits[0].doc_id == 'lazy-doc'
    assert hits[0].source_url == ''


def test_search_validation_and_backend_error(monkeypatch):
    with pytest.raises(svc.KnowledgeSearchError) as invalid:
        svc.search('user-1', '', ['kb'], 10)
    assert invalid.value.code == 'INVALID_ARGUMENT'

    with pytest.raises(svc.KnowledgeSearchError) as missing_user:
        svc.search('', 'q', ['kb'], 10)
    assert missing_user.value.code == 'INVALID_ARGUMENT'

    monkeypatch.setattr(svc, '_ensure_kb_search_runtime', lambda: (_ for _ in ()).throw(RuntimeError('down')))
    with pytest.raises(svc.KnowledgeSearchError) as unavailable:
        svc.search('user-1', 'q', ['kb'], 10)
    assert unavailable.value.code == 'BACKEND_UNAVAILABLE'
    assert isinstance(unavailable.value.cause, RuntimeError)


def test_search_does_not_share_default_user_context(monkeypatch):
    seen_users = []
    monkeypatch.setattr(svc, '_ensure_kb_search_runtime', lambda: ([], None, None))

    def fake_search_kb(payload, **kwargs):
        seen_users.append(payload['user_id'])
        return []

    monkeypatch.setattr(svc, 'search_kb', fake_search_kb)

    svc.search('user-a', 'q', ['kb'], 10)
    svc.search('user-b', 'q', ['kb'], 10)

    assert seen_users == ['user-a', 'user-b']


def test_internal_route_returns_structured_hits(monkeypatch):
    monkeypatch.setattr(routes, 'expected_internal_token', lambda: 'secret-token')
    app = create_app()

    def fake_search(user_id, query, kb_ids, top_k):
        assert user_id == 'user-1'
        assert query == 'q'
        assert kb_ids == ['kb']
        assert top_k == 2
        return [svc.KnowledgeSearchHit(kb_id='kb', doc_id='lazy', chunk_id='chunk', text='text', score=1.5)]

    monkeypatch.setattr(svc, 'search', fake_search)
    client = TestClient(app)

    resp = client.post(
        '/internal/knowledge:search',
        headers={routes.INTERNAL_TOKEN_HEADER: 'secret-token'},
        json={'user_id': 'user-1', 'query': 'q', 'kb_ids': ['kb'], 'top_k': 2},
    )

    assert resp.status_code == 200
    assert resp.json()['hits'][0]['doc_id'] == 'lazy'


def test_internal_route_maps_service_errors(monkeypatch):
    monkeypatch.setattr(routes, 'expected_internal_token', lambda: 'secret-token')
    app = create_app()
    monkeypatch.setattr(
        svc,
        'search',
        lambda **kwargs: (_ for _ in ()).throw(svc.KnowledgeSearchError('BACKEND_UNAVAILABLE', 'down')),
    )
    client = TestClient(app)

    resp = client.post(
        '/internal/knowledge:search',
        headers={routes.INTERNAL_TOKEN_HEADER: 'secret-token'},
        json={'user_id': 'user-1', 'query': 'q', 'kb_ids': ['kb'], 'top_k': 2},
    )

    assert resp.status_code == 503
    assert resp.json()['detail']['code'] == 'BACKEND_UNAVAILABLE'


def test_internal_route_requires_service_token(monkeypatch):
    app = create_app()
    client = TestClient(app)

    monkeypatch.setattr(routes, 'expected_internal_token', lambda: '')
    resp = client.post('/internal/knowledge:search', json={'user_id': 'user-1', 'query': 'q', 'kb_ids': ['kb'], 'top_k': 2})
    assert resp.status_code == 503

    monkeypatch.setattr(routes, 'expected_internal_token', lambda: 'secret-token')
    resp = client.post('/internal/knowledge:search', json={'user_id': 'user-1', 'query': 'q', 'kb_ids': ['kb'], 'top_k': 2})
    assert resp.status_code == 401

    resp = client.post(
        '/internal/knowledge:search',
        headers={routes.INTERNAL_TOKEN_HEADER: 'wrong-token'},
        json={'user_id': 'user-1', 'query': 'q', 'kb_ids': ['kb'], 'top_k': 2},
    )
    assert resp.status_code == 401
    assert 'wrong-token' not in resp.text
    assert 'secret-token' not in resp.text

    resp = client.post(
        '/internal/knowledge:search',
        headers={routes.INTERNAL_TOKEN_HEADER: 'secret-token'},
        json={'query': 'q', 'kb_ids': ['kb'], 'top_k': 2},
    )
    assert resp.status_code == 422


def test_internal_route_offloads_sync_search(monkeypatch):
    monkeypatch.setattr(routes, 'expected_internal_token', lambda: 'secret-token')
    calls = {}

    def fake_search(**kwargs):
        raise AssertionError('service.search should be invoked through asyncio.to_thread')

    async def fake_to_thread(func, *args, **kwargs):
        calls['func'] = func
        calls['kwargs'] = kwargs
        return [svc.KnowledgeSearchHit(kb_id='kb', doc_id='lazy', chunk_id='chunk', text='text', score=1.5)]

    monkeypatch.setattr(svc, 'search', fake_search)
    monkeypatch.setattr(routes.asyncio, 'to_thread', fake_to_thread)
    client = TestClient(create_app())

    resp = client.post(
        '/internal/knowledge:search',
        headers={routes.INTERNAL_TOKEN_HEADER: 'secret-token'},
        json={'user_id': 'user-1', 'query': 'q', 'kb_ids': ['kb'], 'top_k': 2},
    )

    assert resp.status_code == 200
    assert calls['func'] is fake_search
    assert calls['kwargs'] == {'user_id': 'user-1', 'query': 'q', 'kb_ids': ['kb'], 'top_k': 2}


def test_router_internal_proxy_requires_and_replaces_service_token(monkeypatch):
    app = FastAPI()
    app.include_router(proxy_routes.router)
    captured = {}

    class Instance:
        url = 'http://child.test'
        host = 'child-host'

    async def fake_select_instance(caller_algo_id):
        return 'algo-1', Instance()

    class FakeAsyncClient:
        def __init__(self, **kwargs):
            captured['client_kwargs'] = kwargs

        async def __aenter__(self):
            return self

        async def __aexit__(self, exc_type, exc, tb):
            return False

        async def request(self, method, url, headers, content):
            captured['method'] = method
            captured['url'] = url
            captured['headers'] = dict(headers)
            captured['content'] = content
            return httpx.Response(200, json={'hits': []})

    monkeypatch.setattr(routes, 'expected_internal_token', lambda: 'server-token')
    monkeypatch.setattr(proxy_routes, 'expected_internal_token', lambda: 'server-token')
    monkeypatch.setattr(proxy_routes, '_select_instance', fake_select_instance)
    monkeypatch.setattr(proxy_routes.httpx, 'AsyncClient', FakeAsyncClient)
    client = TestClient(app)

    missing = client.post('/internal/knowledge:search', json={'user_id': 'user-1', 'query': 'q', 'kb_ids': ['kb']})
    assert missing.status_code == 401

    wrong = client.post(
        '/internal/knowledge:search',
        headers={routes.INTERNAL_TOKEN_HEADER: 'wrong-token'},
        json={'user_id': 'user-1', 'query': 'q', 'kb_ids': ['kb']},
    )
    assert wrong.status_code == 401
    assert 'wrong-token' not in wrong.text
    assert 'server-token' not in wrong.text

    ok = client.post(
        '/internal/knowledge:search',
        headers={routes.INTERNAL_TOKEN_HEADER: 'server-token'},
        json={'user_id': 'user-1', 'query': 'q', 'kb_ids': ['kb']},
    )
    assert ok.status_code == 200
    assert captured['headers'][routes.INTERNAL_TOKEN_HEADER] == 'server-token'
    assert captured['url'] == 'http://child.test/internal/knowledge:search'
    assert captured['client_kwargs']['timeout'] == 30.0
