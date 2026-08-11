from __future__ import annotations

from dataclasses import dataclass
from typing import Any

import pytest

import lazymind.common.memory.episode_store as episode_store_module

from lazymind.common.memory import (
    EpisodeCreateInput,
    EpisodeSource,
    EpisodeStore,
    EpisodeType,
)


@dataclass
class FakeResponse:
    payload: dict[str, Any]
    status_code: int = 200
    text: str = ''

    @property
    def ok(self) -> bool:
        return 200 <= self.status_code < 300

    def json(self) -> dict[str, Any]:
        return self.payload


class RecordingTransport:
    def __init__(self, *responses: FakeResponse):
        self.responses = list(responses)
        self.calls: list[dict[str, Any]] = []

    def __call__(self, method: str, url: str, **kwargs: Any) -> FakeResponse:
        self.calls.append({'method': method, 'url': url, **kwargs})
        return self.responses.pop(0)


def _episode(summary: str = '采用 SegmentStore') -> EpisodeCreateInput:
    return EpisodeCreateInput(
        occurred_at_ms=1_700_000_000_000,
        episode_type=EpisodeType.DECISION,
        summary=summary,
        source=EpisodeSource(
            kind='chat_explicit',
            conversation_id='conv-1',
        ),
    )


def _wire_episode(
    episode_id: str,
    *,
    recorded_at_ms: int,
    occurred_at_ms: int = 1_700_000_000_000,
    user_id: str = 'user-1',
    episode_type: str = 'decision',
) -> dict[str, Any]:
    return {
        'id': episode_id,
        'user_id': user_id,
        'conversation_id': 'conv-1',
        'source_kind': 'chat_explicit',
        'episode_type': episode_type,
        'summary': f'Episode {episode_id}',
        'occurred_at_ms': occurred_at_ms,
        'recorded_at_ms': recorded_at_ms,
        'hit_count': 3,
    }


def test_create_posts_core_contract_with_internal_token(monkeypatch):
    monkeypatch.setitem(
        episode_store_module._cfg._impl,
        'core_api_url',
        'http://core.test:8000/',
    )
    monkeypatch.setitem(
        episode_store_module._cfg._impl,
        'core_internal_token',
        'internal-secret',
    )
    transport = RecordingTransport(FakeResponse({
        'code': 0,
        'message': 'ok',
        'data': {'status': 'created', 'id': 'ep_core_generated'},
    }))

    result = EpisodeStore(transport=transport).create(
        'user-1',
        _episode('项目验证码是火星苹果42'),
    )

    assert result.status == 'created'
    assert result.id == 'ep_core_generated'
    assert result.model_dump() == {
        'status': 'created',
        'id': 'ep_core_generated',
    }
    call = transport.calls[0]
    assert call['method'] == 'POST'
    assert call['url'] == 'http://core.test:8000/internal/memory/episodes'
    assert call['headers'] == {
        'X-LazyMind-Internal-Token': 'internal-secret',
    }
    assert call['json'] == {
        'user_id': 'user-1',
        'conversation_id': 'conv-1',
        'source_kind': 'chat_explicit',
        'episode_type': 'decision',
        'summary': '项目验证码是火星苹果42',
        'search_text': episode_store_module.tokenize_episode_text('项目验证码是火星苹果42'),
        'tokenizer_version': 'jieba-v1',
        'occurred_at_ms': 1_700_000_000_000,
    }
    assert 'id' not in call['json']
    assert 'idempotency_key' not in call['json']


@pytest.mark.parametrize('status', ['deleted', 'not_found'])
def test_delete_uses_tenant_scoped_internal_endpoint(status):
    transport = RecordingTransport(FakeResponse({
        'code': 0,
        'message': 'ok',
        'data': {'status': status, 'id': 'ep_remove'},
    }))
    store = EpisodeStore(
        transport=transport,
        base_url='http://core.test:8000',
        internal_token='internal-secret',
    )

    result = store.delete('user-1', 'ep_remove')

    assert result.model_dump() == {
        'status': status,
        'id': 'ep_remove',
    }
    call = transport.calls[0]
    assert call['method'] == 'DELETE'
    assert call['url'].endswith('/internal/memory/episodes/ep_remove')
    assert call['params'] == {'user_id': 'user-1'}


def test_list_by_conversation_maps_core_records_and_keeps_oldest_first():
    transport = RecordingTransport(FakeResponse({
        'code': 0,
        'message': 'ok',
        'data': {
            'items': [
                _wire_episode('ep_later', recorded_at_ms=2_000),
                _wire_episode('ep_earlier', recorded_at_ms=1_000),
            ],
        },
    }))
    store = EpisodeStore(
        transport=transport,
        base_url='http://core.test:8000',
        internal_token='internal-secret',
    )

    records = store.list_by_conversation('user-1', 'conv-1')

    assert [record.id for record in records] == ['ep_earlier', 'ep_later']
    assert records[0].source.model_dump() == {
        'kind': 'chat_explicit',
        'conversation_id': 'conv-1',
    }
    assert records[0].hit_count == 3
    call = transport.calls[0]
    assert call['method'] == 'GET'
    assert call['url'] == 'http://core.test:8000/internal/memory/episodes'
    assert call['params'] == {
        'user_id': 'user-1',
        'conversation_id': 'conv-1',
    }


def test_search_requests_core_candidates_then_applies_local_hard_filter(monkeypatch):
    relevant = _wire_episode(
        'ep_relevant',
        recorded_at_ms=1_700_000_000_000,
        occurred_at_ms=1_700_000_000_000,
    )
    relevant['summary'] = '项目验证码是火星苹果42'
    unrelated = _wire_episode(
        'ep_unrelated',
        recorded_at_ms=1_700_000_000_000,
        occurred_at_ms=1_700_000_000_000,
    )
    unrelated['summary'] = '今天讨论了部署窗口'
    transport = RecordingTransport(FakeResponse({
        'code': 0,
        'message': 'ok',
        'data': {
            'items': [
                {'episode': unrelated, 'lexical_score': 100.0},
                {'episode': relevant, 'lexical_score': 0.25},
            ],
        },
    }))
    monkeypatch.setitem(
        episode_store_module._cfg._impl,
        'episode_candidate_topk',
        7,
    )
    store = EpisodeStore(
        transport=transport,
        base_url='http://core.test:8000',
        internal_token='internal-secret',
    )

    results = store.search(
        'user-1',
        '还记得火星苹果42吗',
        now_ms=1_700_000_000_000,
    )

    assert [result.episode.id for result in results] == ['ep_relevant']
    assert results[0].lexical_score == 0.25
    call = transport.calls[0]
    assert call['method'] == 'POST'
    assert call['url'].endswith('/internal/memory/episodes:searchCandidates')
    assert call['json'] == {
        'user_id': 'user-1',
        'terms': ['火星', '苹果', '42'],
        'limit': 7,
    }


def test_increment_hits_records_unique_episode_ids_in_core():
    transport = RecordingTransport(FakeResponse({
        'code': 0,
        'message': 'ok',
        'data': {
            'results': {
                'ep_own': True,
                'ep_missing': False,
            },
        },
    }))
    store = EpisodeStore(
        transport=transport,
        base_url='http://core.test:8000',
        internal_token='internal-secret',
    )

    result = store.increment_hits(
        'user-1',
        ['ep_own', 'ep_missing', 'ep_own'],
    )

    assert result == {
        'ep_own': True,
        'ep_missing': False,
    }
    call = transport.calls[0]
    assert call['method'] == 'POST'
    assert call['url'].endswith('/internal/memory/episodes:recordHits')
    assert call['json'] == {
        'user_id': 'user-1',
        'episode_ids': ['ep_own', 'ep_missing'],
    }


def test_episode_store_requires_internal_token_before_sending_requests():
    with pytest.raises(
        ValueError,
        match='LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN is required',
    ):
        EpisodeStore(
            transport=RecordingTransport(),
            base_url='http://core.test:8000',
            internal_token='',
        )
