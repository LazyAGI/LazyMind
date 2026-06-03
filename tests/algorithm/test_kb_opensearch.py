from importlib import reload

from lazymind.chat.engine.tools.infra import kb_opensearch_client


class DummyResponse:
    def raise_for_status(self):
        pass

    def json(self):
        return {'hits': {'hits': []}}


class DummySession:
    last = None

    def __init__(self):
        self.trust_env = True
        DummySession.last = self

    def __enter__(self):
        return self

    def __exit__(self, exc_type, exc, tb):
        return False

    def post(self, url, **kwargs):
        self.url = url
        self.kwargs = kwargs
        return DummyResponse()


def test_opensearch_search_ignores_environment_proxies(monkeypatch):
    monkeypatch.setattr(kb_opensearch_client.requests.sessions, 'Session', DummySession)
    monkeypatch.setenv('LAZYLLM_HTTPS_PROXY', 'http://proxy.example:3128')

    result = kb_opensearch_client.opensearch_search(
        'idx',
        {'query': {'match_all': {}}},
        {'es_url': 'http://10.0.0.1:9200', 'es_user': 'u', 'es_password': 'p'},
    )

    assert result == {'hits': {'hits': []}}
    assert DummySession.last.trust_env is False
    assert DummySession.last.url == 'http://10.0.0.1:9200/idx/_search'
    assert 'proxies' not in DummySession.last.kwargs
    assert DummySession.last.kwargs['auth'] == ('u', 'p')


def test_opensearch_search_uses_default_password_when_env_is_blank(monkeypatch):
    monkeypatch.setenv('LAZYMIND_OPENSEARCH_PASSWORD', '')
    module = reload(kb_opensearch_client)
    monkeypatch.setattr(module.requests.sessions, 'Session', DummySession)

    module.opensearch_search(
        'idx',
        {'query': {'match_all': {}}},
        {'es_url': 'https://opensearch:9200', 'es_user': 'admin'},
    )

    assert DummySession.last.kwargs['auth'] == ('admin', 'LazyRAG_OpenSearch123!')
