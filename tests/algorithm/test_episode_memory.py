from __future__ import annotations

import copy
import json

from dataclasses import dataclass
from types import SimpleNamespace
from typing import Any, Iterable
from urllib.parse import urlsplit

import lazyllm
import pytest

from lazyllm.tools.agent.toolsManager import ToolManager

import lazymind.chat.engine.tools.memory as memory_tool_module
import lazymind.common.memory.episode_store as episode_store_module

from lazymind.chat.engine.tools.memory import MemoryReviewEpisodeTools, MemoryTools
from lazymind.common.memory import (
    EpisodeCreateInput,
    EpisodeDeleteResult,
    EpisodeSource,
    EpisodeStore,
    EpisodeType,
    normalize_episode_summary,
)


@dataclass
class FakeResponse:
    payload: dict[str, Any]
    status_code: int = 200
    text: str = ''

    def json(self) -> dict[str, Any]:
        return self.payload


class FakeCoreTransport:
    def __init__(
        self,
        *,
        recorded_times: Iterable[int] | None = None,
        search_all: bool = True,
    ):
        self.rows: dict[str, dict[str, Any]] = {}
        self.calls: list[dict[str, Any]] = []
        self.scores: dict[str, float] = {}
        self._recorded_times = iter(recorded_times or ())
        self._next_id = 1
        self._search_all = search_all

    @staticmethod
    def _ok(data: dict[str, Any]) -> FakeResponse:
        return FakeResponse({'code': 0, 'message': 'ok', 'data': data})

    def __call__(self, method: str, url: str, **kwargs: Any) -> FakeResponse:
        path = urlsplit(url).path
        call = {
            'method': method,
            'path': path,
            'headers': copy.deepcopy(kwargs.get('headers') or {}),
            'params': copy.deepcopy(kwargs.get('params') or {}),
            'json': copy.deepcopy(kwargs.get('json') or {}),
        }
        self.calls.append(call)
        if method == 'POST' and path == '/internal/memory/episodes':
            return self._create(call['json'])
        if method == 'GET' and path == '/internal/memory/episodes':
            return self._list(call['params'])
        if method == 'POST' and path == '/internal/memory/episodes:searchCandidates':
            return self._search(call['json'])
        if method == 'POST' and path == '/internal/memory/episodes:recordHits':
            return self._record_hits(call['json'])
        raise AssertionError(f'unexpected Episode Core request: {method} {path}')

    def _create(self, payload: dict[str, Any]) -> FakeResponse:
        identity = (
            payload['user_id'],
            payload['conversation_id'],
            normalize_episode_summary(payload['summary']),
        )
        existing = sorted(
            (
                row
                for row in self.rows.values()
                if (
                    row['user_id'],
                    row['conversation_id'],
                    normalize_episode_summary(row['summary']),
                ) == identity
            ),
            key=lambda row: (row['recorded_at_ms'], row['id']),
        )
        if existing:
            return self._ok({'status': 'idempotent', 'id': existing[0]['id']})
        episode_id = f'ep_core_{self._next_id:04d}'
        self._next_id += 1
        try:
            recorded_at_ms = next(self._recorded_times)
        except StopIteration:
            recorded_at_ms = 1_800_000_000_000 + self._next_id
        self.rows[episode_id] = {
            'id': episode_id,
            'user_id': payload['user_id'],
            'conversation_id': payload['conversation_id'],
            'source_kind': payload['source_kind'],
            'episode_type': payload['episode_type'],
            'summary': payload['summary'],
            'occurred_at_ms': payload['occurred_at_ms'],
            'recorded_at_ms': recorded_at_ms,
            'hit_count': 0,
            'search_text': payload['search_text'],
            'tokenizer_version': payload['tokenizer_version'],
        }
        return self._ok({'status': 'created', 'id': episode_id})

    def _list(self, params: dict[str, Any]) -> FakeResponse:
        items = [
            copy.deepcopy(row)
            for row in self.rows.values()
            if (
                row['user_id'] == params.get('user_id')
                and row['conversation_id'] == params.get('conversation_id')
            )
        ]
        return self._ok({'items': items})

    def _search(self, payload: dict[str, Any]) -> FakeResponse:
        terms = set(payload['terms'])
        rows = [
            row
            for row in self.rows.values()
            if row['user_id'] == payload['user_id']
            and (
                self._search_all
                or terms.intersection(str(row['search_text']).split())
            )
        ]
        items = [
            {
                'episode': copy.deepcopy(row),
                'lexical_score': self.scores.get(row['id'], 1.0),
            }
            for row in rows[:payload['limit']]
        ]
        return self._ok({'items': items})

    def _record_hits(self, payload: dict[str, Any]) -> FakeResponse:
        results: dict[str, bool] = {}
        for episode_id in payload['episode_ids']:
            row = self.rows.get(episode_id)
            matched = row is not None and row['user_id'] == payload['user_id']
            results[episode_id] = matched
            if matched:
                row['hit_count'] += 1
        return self._ok({'results': results})


def _store(
    transport: Any,
    *,
    clock_ms=None,
) -> EpisodeStore:
    return EpisodeStore(
        transport=transport,
        base_url='http://core.test:8000',
        internal_token='internal-secret',
        clock_ms=clock_ms,
    )


def _episode(
    summary: str = '采用 Core Episode',
    *,
    occurred_at_ms: int = 1_700_000_000_000,
    conversation_id: str = 'conv-1',
    episode_type: EpisodeType = EpisodeType.DECISION,
) -> EpisodeCreateInput:
    return EpisodeCreateInput(
        occurred_at_ms=occurred_at_ms,
        episode_type=episode_type,
        summary=summary,
        source=EpisodeSource(
            kind='chat_explicit',
            conversation_id=conversation_id,
        ),
    )


@pytest.fixture(autouse=True)
def _restore_agentic_config():
    sentinel = object()
    previous = lazyllm.globals.get('agentic_config', sentinel)
    yield
    if previous is sentinel:
        lazyllm.globals.pop('agentic_config', None)
    else:
        lazyllm.globals['agentic_config'] = previous


def test_core_idempotency_is_scoped_by_user_conversation_and_summary():
    transport = FakeCoreTransport()
    store = _store(transport)

    baseline = store.create('user-1', _episode())

    assert store.create('user-2', _episode()).id != baseline.id
    assert store.create('user-1', _episode(conversation_id='conv-2')).id != baseline.id
    assert store.create(
        'user-1',
        _episode(episode_type=EpisodeType.RESULT),
    ).id == baseline.id
    assert len(transport.rows) == 3


def test_list_by_conversation_is_tenant_scoped_and_sorted_by_recorded_time():
    transport = FakeCoreTransport(recorded_times=[2_000, 1_000, 3_000, 4_000])
    store = _store(transport)

    later = store.create('user-1', _episode('稍后记录', occurred_at_ms=100)).id
    earlier = store.create('user-1', _episode('更早记录', occurred_at_ms=200)).id
    store.create('user-1', _episode('其他会话', conversation_id='conv-2'))
    store.create('user-2', _episode('其他用户'))

    records = store.list_by_conversation('user-1', 'conv-1')

    assert [record.id for record in records] == [earlier, later]
    assert all(record.user_id == 'user-1' for record in records)
    assert all(record.source.conversation_id == 'conv-1' for record in records)


def test_search_hard_filters_a_high_scoring_unrelated_candidate():
    transport = FakeCoreTransport()
    store = _store(transport)
    relevant = store.create('user-1', _episode('项目验证码是火星苹果42'))
    unrelated = store.create('user-1', _episode('今天讨论了部署窗口'))
    transport.scores[relevant.id] = 0.000001
    transport.scores[unrelated.id] = 100.0

    results = store.search(
        'user-1',
        '还记得火星苹果42吗',
        now_ms=1_700_000_000_000,
    )

    assert [item.episode.id for item in results] == [relevant.id]
    assert results[0].lexical_score == 0.000001
    assert results[0].score <= 1.0


def test_search_ranks_by_coverage_recency_and_hit_count(monkeypatch):
    day_ms = 86_400_000
    now_ms = 2_000_000_000_000
    transport = FakeCoreTransport()
    store = _store(transport)
    high_coverage = store.create(
        'user-1',
        _episode('mars apple banana', occurred_at_ms=now_ms - 100 * day_ms),
    )
    recent = store.create(
        'user-1',
        _episode('mars apple recent', conversation_id='conv-2', occurred_at_ms=now_ms),
    )
    popular = store.create(
        'user-1',
        _episode(
            'mars apple popular',
            conversation_id='conv-3',
            occurred_at_ms=now_ms - 10 * day_ms,
        ),
    )
    transport.rows[popular.id]['hit_count'] = 10
    monkeypatch.setitem(episode_store_module._cfg._impl, 'episode_relevance_weight', 0.8)
    monkeypatch.setitem(episode_store_module._cfg._impl, 'episode_recency_weight', 0.1)
    monkeypatch.setitem(episode_store_module._cfg._impl, 'episode_hit_weight', 0.1)
    monkeypatch.setitem(episode_store_module._cfg._impl, 'episode_half_life_days', 10.0)
    monkeypatch.setitem(episode_store_module._cfg._impl, 'episode_hit_saturation', 10)

    results = store.search('user-1', 'mars apple banana', now_ms=now_ms)

    assert [item.episode.id for item in results] == [
        high_coverage.id,
        popular.id,
        recent.id,
    ]


def test_increment_hits_is_batched_deduplicated_and_tenant_scoped():
    transport = FakeCoreTransport()
    store = _store(transport)
    own = store.create('user-1', _episode())
    other = store.create('user-2', _episode())

    result = store.increment_hits('user-1', [own.id, other.id, own.id])

    assert result == {own.id: True, other.id: False}
    assert transport.rows[own.id]['hit_count'] == 1
    assert transport.rows[other.id]['hit_count'] == 0
    call = transport.calls[-1]
    assert call['path'] == '/internal/memory/episodes:recordHits'
    assert call['json'] == {
        'user_id': 'user-1',
        'episode_ids': [own.id, other.id],
    }


def test_memory_tools_registers_as_eager_container_with_episode_schema():
    manager = ToolManager([MemoryTools()])
    descriptions = {
        item['function']['name']: item['function']
        for item in manager.tools_description
    }

    assert set(descriptions) == {
        'MemoryTools_read_memory',
        'MemoryTools_read_memory_reference',
        'MemoryTools_soul_editor',
        'MemoryTools_profile_editor',
        'MemoryTools_preference_editor',
        'MemoryTools_episode_create',
    }
    episode_schema = descriptions['MemoryTools_episode_create']['parameters']
    assert set(episode_schema['properties']) == {'summary', 'episode_type'}
    assert set(episode_schema['required']) == {'summary', 'episode_type'}

    for editor_name in ('MemoryTools_soul_editor', 'MemoryTools_profile_editor'):
        editor_schema = descriptions[editor_name]['parameters']
        assert set(editor_schema['properties']) == {'operations'}
        assert set(editor_schema['required']) == {'operations'}

    resource_tools = {
        'MemoryTools_read_memory',
        'MemoryTools_read_memory_reference',
        'MemoryTools_soul_editor',
        'MemoryTools_profile_editor',
        'MemoryTools_preference_editor',
    }
    assert all(
        manager.tools_info[name].concurrency_spec is not None
        for name in resource_tools
    )
    assert manager.tools_info['MemoryTools_episode_create'].concurrency_spec is None
    schema = json.dumps(manager.tools_description)
    assert '__lazyllm_tool_concurrency__' not in schema
    assert 'concurrency_spec' not in schema


def test_memory_review_episode_search_is_tenant_scoped_and_capped(monkeypatch):
    class FakeEpisodeStore:
        def search(self, user_id, query):
            assert user_id == 'user-1'
            assert query == '健身记录 训练内容 减脂追踪'
            return [
                SimpleNamespace(
                    episode=SimpleNamespace(
                        id=f'ep_{index}',
                        summary=f'episode {index}',
                        episode_type=EpisodeType.DECISION,
                        occurred_at_ms=1_700_000_000_000 + index,
                        source=SimpleNamespace(conversation_id=f'conv-{index}'),
                    ),
                    score=1.0 - index / 100,
                )
                for index in range(25)
            ]

    lazyllm.globals['agentic_config'] = {
        'user_id': 'user-1',
        'memory_source_kind': 'memory_review',
        'memory_operation_ledger': [],
    }
    monkeypatch.setattr(
        memory_tool_module,
        'get_episode_store',
        lambda: FakeEpisodeStore(),
    )

    result = MemoryReviewEpisodeTools().episode_search(
        '健身记录 训练内容 减脂追踪',
    )

    assert result['candidate_count'] == 20
    assert len(result['items']) == 20
    assert result['items'][0] == {
        'id': 'ep_0',
        'summary': 'episode 0',
        'episode_type': 'decision',
        'occurred_at_ms': 1_700_000_000_000,
        'conversation_id': 'conv-0',
        'score': 1.0,
    }
    assert lazyllm.globals['agentic_config']['memory_operation_ledger'] == [{
        'operation': 'episode_search',
        'status': 'succeeded',
        'mutation': 'none',
        'result': {'candidate_count': 20},
        'retryable': False,
    }]


@pytest.mark.parametrize(
    ('status', 'mutation'),
    [
        ('deleted', True),
        ('not_found', False),
    ],
)
def test_memory_review_episode_delete_is_idempotent_and_recorded(
    monkeypatch,
    status,
    mutation,
):
    calls = []

    class FakeEpisodeStore:
        def delete(self, user_id, episode_id):
            calls.append((user_id, episode_id))
            return EpisodeDeleteResult(status=status, id=episode_id)

    lazyllm.globals['agentic_config'] = {
        'user_id': 'user-1',
        'memory_source_kind': 'memory_review',
        'memory_operation_ledger': [],
    }
    monkeypatch.setattr(
        memory_tool_module,
        'get_episode_store',
        lambda: FakeEpisodeStore(),
    )

    result = MemoryReviewEpisodeTools().episode_delete('ep_duplicate')

    assert result == {
        'status': status,
        'id': 'ep_duplicate',
    }
    assert calls == [('user-1', 'ep_duplicate')]
    assert lazyllm.globals['agentic_config']['memory_operation_ledger'] == [{
        'operation': 'episode_delete',
        'status': 'succeeded',
        'mutation': 'applied' if mutation else 'none',
        'retry_fingerprint': 'episode_delete:ep_duplicate',
        'result': {
            'status': status,
            'id': 'ep_duplicate',
        },
        'retryable': False,
    }]


def _episode_runtime_config(*, source_kind: str = 'chat_explicit') -> dict[str, Any]:
    return {
        'user_id': 'user-1',
        'task_id': 'task-1',
        'conversation_id': 'conv-1',
        'episode_occurred_at_ms': 1_700_000_000_000,
        'episode_source_kind': source_kind,
        'memory_operation_ledger': [],
    }


def _patch_episode_store(monkeypatch, store: Any) -> None:
    memory_module = __import__(
        'lazymind.chat.engine.tools.memory',
        fromlist=['get_episode_store'],
    )
    monkeypatch.setattr(memory_module, 'get_episode_store', lambda: store)


def test_episode_create_uses_runtime_context_and_keeps_fingerprint_internal(monkeypatch):
    transport = FakeCoreTransport()
    store = _store(transport)
    config = _episode_runtime_config()
    config['use_memory'] = False
    lazyllm.globals['agentic_config'] = config
    _patch_episode_store(monkeypatch, store)

    result = MemoryTools().episode_create('用户明确要求保存此事件', 'event')

    assert result == {
        'status': 'created',
        'id': next(iter(transport.rows)),
    }
    row = next(iter(transport.rows.values()))
    assert row['user_id'] == 'user-1'
    assert row['conversation_id'] == 'conv-1'
    assert row['occurred_at_ms'] == 1_700_000_000_000
    assert row['source_kind'] == 'chat_explicit'
    ledger = config['memory_operation_ledger'][0]
    ledger_result = ledger['result']
    assert ledger_result['status'] == 'created'
    assert ledger['retry_fingerprint'].startswith('episode_retry_')
    assert 'retry_fingerprint' not in result
