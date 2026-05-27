from typing import Any, Dict, List

import pytest
import requests

import lazymind.online_models.local_models as reranker_module
from lazymind.online_models.local_models import BgeM3Embed, Qwen3Rerank
from lazyllm.tools.rag.doc_node import DocNode


class _FakeResponse:
    def __init__(self, payload: Dict[str, Any]):
        self._payload = payload

    def raise_for_status(self) -> None:
        return None

    def json(self) -> Dict[str, Any]:
        return self._payload


def _build_reranker() -> Qwen3Rerank:
    return Qwen3Rerank(embed_url='http://example.com/rerank', api_key='test-key')


class TestLocalModelsEmbed:
    def test_bgem3_encapsulated_data_handles_single_string(self):
        embed = BgeM3Embed(embed_url='http://example.com/embed', batch_size=2)

        payload = embed._encapsulated_data('hello', model='m1', normalize=True)

        assert payload == {'inputs': 'hello', 'model': 'm1', 'normalize': True}

    def test_bgem3_encapsulated_data_batches_list_input(self):
        embed = BgeM3Embed(embed_url='http://example.com/embed', batch_size=2)

        payload = embed._encapsulated_data(
            ['a', 'b', 'c'], model='m2', normalize=False
        )

        assert payload == [
            {'inputs': ['a', 'b'], 'model': 'm2', 'normalize': False},
            {'inputs': ['c'], 'model': 'm2', 'normalize': False},
        ]

    def test_bgem3_parse_response_supports_local_service_shapes(self):
        embed = BgeM3Embed(embed_url='http://example.com/embed')

        assert embed._parse_response({'custom': 1}, 'hello') == {'custom': 1}
        assert embed._parse_response([0.1, 0.2], 'hello') == [0.1, 0.2]
        assert embed._parse_response([[0.1, 0.2], [0.3, 0.4]], ['a', 'b']) == [
            [0.1, 0.2],
            [0.3, 0.4],
        ]

    def test_bgem3_parse_response_rejects_invalid_payload(self):
        embed = BgeM3Embed(embed_url='http://example.com/embed')

        with pytest.raises(RuntimeError, match='empty embedding response'):
            embed._parse_response([], 'hello')
        with pytest.raises(RuntimeError, match='unexpected embedding response type'):
            embed._parse_response('bad', 'hello')


class TestQwen3RerankCompat:
    def test_supports_query_documents_signature(self, monkeypatch):
        reranker = _build_reranker()
        seen_payloads: List[Dict[str, Any]] = []

        def _fake_post(
            url: str, json: Dict[str, Any], headers: Dict[str, str], timeout: Any
        ):
            seen_payloads.append(
                {'url': url, 'json': json, 'headers': headers, 'timeout': timeout}
            )
            return _FakeResponse(
                {
                    'results': [
                        {'index': 0, 'relevance_score': 0.2},
                        {'index': 1, 'relevance_score': 0.9},
                        {'index': 2, 'relevance_score': 0.4},
                    ]
                }
            )

        monkeypatch.setattr(reranker._session, 'post', _fake_post)

        results = reranker.forward(
            'who wrote this?', documents=['doc-a', 'doc-b', 'doc-c'], top_n=2
        )

        assert results == [(1, 0.9), (2, 0.4)]
        assert seen_payloads[0]['url'] == 'http://example.com/rerank'
        assert 'who wrote this?' in seen_payloads[0]['json']['query']
        assert 'top_n' not in seen_payloads[0]['json']

    def test_supports_keyword_only_documents_signature(self, monkeypatch):
        reranker = _build_reranker()

        def _fake_post(
            url: str, json: Dict[str, Any], headers: Dict[str, str], timeout: Any
        ):
            return _FakeResponse(
                {
                    'results': [
                        {'index': 0, 'relevance_score': 0.3},
                        {'index': 1, 'relevance_score': 0.7},
                    ]
                }
            )

        monkeypatch.setattr(reranker._session, 'post', _fake_post)

        results = reranker.forward(
            query='who wrote this?', documents=['doc-a', 'doc-b'], top_n=1
        )

        assert results == [(1, 0.7)]

    def test_supports_documents_query_signature(self, monkeypatch):
        reranker = _build_reranker()

        def _fake_post(
            url: str, json: Dict[str, Any], headers: Dict[str, str], timeout: Any
        ):
            return _FakeResponse(
                {
                    'results': [
                        {'index': 0, 'relevance_score': 0.6},
                        {'index': 1, 'relevance_score': 0.1},
                    ]
                }
            )

        monkeypatch.setattr(reranker._session, 'post', _fake_post)

        results = reranker.forward(['doc-a', 'doc-b'], query='find a', top_n=1)

        assert results == [(0, 0.6)]

    def test_warns_on_empty_query(self, monkeypatch):
        reranker = _build_reranker()
        warnings = []

        def _fake_post(
            url: str, json: Dict[str, Any], headers: Dict[str, str], timeout: Any
        ):
            return _FakeResponse({'results': [{'index': 0, 'relevance_score': 0.5}]})

        def _fake_warning(message: str, *args: Any):
            warnings.append(message % args if args else message)

        monkeypatch.setattr(reranker._session, 'post', _fake_post)
        monkeypatch.setattr(reranker_module.LOG, 'warning', _fake_warning)

        results = reranker.forward(['doc-a'], top_n=1)

        assert results == [(0, 0.5)]
        assert any('empty query' in item for item in warnings)

    def test_returns_empty_for_empty_documents(self):
        reranker = _build_reranker()

        assert reranker.forward('query', documents=[]) == []
        assert reranker.forward([], query='query') == []
        assert reranker.forward(query='query', documents=[]) == []

    def test_preserves_legacy_nodes_signature(self, monkeypatch):
        reranker = _build_reranker()

        def _fake_post(
            url: str, json: Dict[str, Any], headers: Dict[str, str], timeout: Any
        ):
            return _FakeResponse(
                {
                    'results': [
                        {'index': 0, 'relevance_score': 0.1},
                        {'index': 1, 'relevance_score': 0.8},
                        {'index': 2, 'relevance_score': 0.3},
                    ]
                }
            )

        monkeypatch.setattr(reranker._session, 'post', _fake_post)
        nodes = [DocNode(text='first'), DocNode(text='second'), DocNode(text='third')]

        results = reranker.forward(nodes, query='pick the best one', topk=2)

        assert [node.text for node in results] == ['second', 'third']
        assert results[0].relevance_score == 0.8

    def test_respects_zero_topn(self, monkeypatch):
        reranker = _build_reranker()

        def _fake_post(
            url: str, json: Dict[str, Any], headers: Dict[str, str], timeout: Any
        ):
            return _FakeResponse(
                {
                    'results': [
                        {'index': 0, 'relevance_score': 0.1},
                        {'index': 1, 'relevance_score': 0.9},
                    ]
                }
            )

        monkeypatch.setattr(reranker._session, 'post', _fake_post)
        nodes = [DocNode(text='first'), DocNode(text='second')]

        assert reranker.forward('query', documents=['doc-a', 'doc-b'], top_n=0) == []
        assert reranker.forward(nodes, query='query', topk=0) == []

    def test_encapsulated_data_filters_local_only_keys(self):
        reranker = _build_reranker()

        payload = reranker._encapsulated_data(
            'find this',
            documents=['doc-a'],
            top_n=3,
            timeout=5,
            extra_flag=True,
        )

        assert 'top_n' not in payload
        assert 'timeout' not in payload
        assert payload['extra_flag'] is True
        assert payload['documents'][0].startswith('<Document>: doc-a')

    def test_scores_failed_http_batches_as_zero(self, monkeypatch):
        reranker = _build_reranker()
        errors = []

        def _fake_post(
            url: str, json: Dict[str, Any], headers: Dict[str, str], timeout: Any
        ):
            raise requests.RequestException('network down')

        def _fake_error(message: str, *args: Any):
            errors.append(message % args if args else message)

        monkeypatch.setattr(reranker._session, 'post', _fake_post)
        monkeypatch.setattr(reranker_module.LOG, 'error', _fake_error)

        results = reranker.forward('query', documents=['doc-a', 'doc-b'], top_n=2)

        assert results == [(0, 0.0), (1, 0.0)]
        assert any('HTTP request for reranking failed' in item for item in errors)
