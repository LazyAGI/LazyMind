from __future__ import annotations

import copy
import inspect

from dataclasses import dataclass
from typing import Any, Iterable
from urllib.parse import urlsplit

import lazyllm
import pytest
import requests

from lazyllm.tools.agent.toolsManager import ToolManager

import lazymind.common.memory.episode_store as episode_store_module

from lazymind.chat.engine.tools.memory import MemoryTools
from lazymind.common.memory import (
    EpisodeCreateInput,
    EpisodeReadError,
    EpisodeSource,
    EpisodeStore,
    EpisodeType,
    normalize_episode_summary,
    tokenize_episode_text,
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


def test_episode_summary_and_tokenizer_use_stable_unicode_normalization():
    assert normalize_episode_summary('  Use  ＳｅｇｍｅｎｔStore\nV１  ') == 'use segmentstore v1'
    tokens = tokenize_episode_text('用户决定使用 SegmentStore，版本Ｖ１')
    assert '用户' in tokens
    assert 'segmentstore' in tokens
    assert 'v1' in tokens


@pytest.mark.parametrize('summary', ['', 'x' * 201])
def test_episode_input_requires_summary_between_one_and_two_hundred_characters(summary):
    with pytest.raises(ValueError):
        _episode(summary)


def test_create_uses_core_idempotency_without_exposing_a_hash_id():
    transport = FakeCoreTransport(recorded_times=[2_000])
    store = _store(transport)

    first = store.create(
        'user-1',
        _episode('  Use  Core Episode V1  ', occurred_at_ms=1_000),
    )
    retry = store.create(
        'user-1',
        _episode(
            'use core episode v１',
            occurred_at_ms=2_000,
            episode_type=EpisodeType.RESULT,
        ),
    )

    assert first.model_dump() == {'status': 'created', 'id': first.id}
    assert retry.model_dump() == {'status': 'idempotent', 'id': first.id}
    assert first.id.startswith('ep_core_')
    assert len(transport.rows) == 1
    row = transport.rows[first.id]
    assert row['occurred_at_ms'] == 1_000
    assert row['summary'] == 'Use  Core Episode V1'
    assert row['episode_type'] == 'decision'
    assert row['search_text'] == tokenize_episode_text(row['summary'])
    assert row['tokenizer_version'] == 'jieba-v1'


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


def test_list_by_conversation_converts_transport_failure_to_strict_read_error():
    def unavailable_transport(*_args, **_kwargs):
        raise ConnectionError(
            'Core unavailable at https://admin:secret@core.internal/private'
        )

    with pytest.raises(EpisodeReadError) as captured:
        _store(unavailable_transport).list_by_conversation('user-1', 'conv-1')

    assert captured.value.code == 'storage_unavailable'
    assert captured.value.retryable is True
    assert str(captured.value) == 'Failed to load existing Episodes.'
    assert 'core.internal' not in str(captured.value)


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


def test_search_converts_transport_failure_to_safe_episode_error():
    def unavailable_transport(*_args, **_kwargs):
        raise ConnectionError(
            'Core unavailable at https://admin:secret@core.internal/private'
        )

    with pytest.raises(EpisodeReadError) as captured:
        _store(unavailable_transport).search('user-1', 'mars apple')

    assert captured.value.code == 'storage_unavailable'
    assert captured.value.retryable is True
    assert str(captured.value) == 'Failed to load existing Episodes.'


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


def test_search_requires_short_query_terms_and_exact_identifiers():
    transport = FakeCoreTransport()
    store = _store(transport)
    exact = store.create('user-1', _episode('错误编号 ERR-42 来自 payment_api'))
    store.create('user-1', _episode('错误编号 ERR-43 来自 payment_api'))
    store.create('user-1', _episode('错误编号 ERR-42 来自 profile_api'))

    assert [
        item.episode.id
        for item in store.search('user-1', 'ERR-42 payment_api')
    ] == [exact.id]

    search_count = sum(
        call['path'].endswith(':searchCandidates')
        for call in transport.calls
    )
    for query in (
        '还记得吗',
        '帮我看看',
        '你知道吗',
        '继续',
        '为什么',
        '再详细说说',
        '请继续介绍',
        '好不好',
        '帮我查一下',
        '给我看看',
        '多说一点',
    ):
        assert store.search('user-1', query) == []
    assert sum(
        call['path'].endswith(':searchCandidates')
        for call in transport.calls
    ) == search_count


def test_numeric_query_requires_complete_ascii_identifier_boundary():
    transport = FakeCoreTransport()
    store = _store(transport)
    exact = store.create('user-1', _episode('错误编号是 42'))
    store.create('user-1', _episode('错误编号是 42A'))
    store.create('user-1', _episode('错误编号是 A42'))

    assert [item.episode.id for item in store.search('user-1', '42')] == [exact.id]


def test_structured_numeric_identifier_requires_same_order_and_separators():
    transport = FakeCoreTransport()
    store = _store(transport)
    exact = store.create('user-1', _episode('工单编号是 123-456'))
    store.create('user-1', _episode('工单编号是 456-123'))
    store.create('user-1', _episode('工单编号是 123/456'))

    assert [
        item.episode.id
        for item in store.search('user-1', '123-456')
    ] == [exact.id]


@pytest.mark.parametrize(
    ('query', 'summary'),
    [
        ('v1.2.3', '当前发布版本是 v1.2.3'),
        ('ERR42', '线上错误编号是 ERR42'),
        ('build2026', '当前构建标识是 build2026'),
    ],
)
def test_search_recalls_exact_alphanumeric_identifier(query, summary):
    transport = FakeCoreTransport()
    store = _store(transport)
    relevant = store.create('user-1', _episode(summary))

    results = store.search('user-1', query)

    assert [result.episode.id for result in results] == [relevant.id]


def test_episode_create_schema_only_exposes_two_required_arguments():
    signature = inspect.signature(MemoryTools.episode_create)
    assert list(signature.parameters) == ['self', 'summary', 'episode_type']
    assert signature.parameters['summary'].default is inspect.Parameter.empty
    assert signature.parameters['episode_type'].default is inspect.Parameter.empty


def test_memory_tools_registers_as_eager_container_with_episode_schema():
    manager = ToolManager([MemoryTools()])
    descriptions = {
        item['function']['name']: item['function']
        for item in manager.tools_description
    }

    assert set(descriptions) == {
        'MemoryTools_read_memory_reference',
        'MemoryTools_soul_editor',
        'MemoryTools_profile_editor',
        'MemoryTools_preference_editor',
        'MemoryTools_episode_create',
    }
    episode_schema = descriptions['MemoryTools_episode_create']['parameters']
    assert set(episode_schema['properties']) == {'summary', 'episode_type'}
    assert set(episode_schema['required']) == {'summary', 'episode_type'}


def _episode_runtime_config(*, source_kind: str = 'chat_explicit') -> dict[str, Any]:
    return {
        'user_id': 'user-1',
        'task_id': 'task-1',
        'conversation_id': 'conv-1',
        'episode_occurred_at_ms': 1_700_000_000_000,
        'episode_source_kind': source_kind,
        'memory_tool_results': [],
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
        'success': True,
        'tool': 'episode_create',
        'result': {
            'status': 'created',
            'id': next(iter(transport.rows)),
        },
        'retryable': False,
    }
    row = next(iter(transport.rows.values()))
    assert row['user_id'] == 'user-1'
    assert row['conversation_id'] == 'conv-1'
    assert row['occurred_at_ms'] == 1_700_000_000_000
    assert row['source_kind'] == 'chat_explicit'
    ledger_result = config['memory_tool_results'][0]['result']
    assert ledger_result['status'] == 'created'
    assert ledger_result['retry_fingerprint'].startswith('episode_retry_')
    assert 'retry_fingerprint' not in result['result']


def test_episode_create_reports_missing_context_to_agent_and_ledger():
    lazyllm.globals['agentic_config'] = {
        'user_id': 'user-1',
        'task_id': 'task-1',
        'memory_tool_results': [],
    }

    result = MemoryTools().episode_create('需要保存', 'decision')

    assert result['success'] is False
    assert result['error']['code'] == 'missing_context'
    assert result['error']['detail'] == {'field': 'conversation_id'}
    assert result['retryable'] is False


@pytest.mark.parametrize(
    ('source_kind', 'expected_code'),
    [(None, 'missing_context'), ('unsupported_source', 'invalid_arguments')],
)
def test_episode_create_requires_valid_explicit_source_kind(source_kind, expected_code):
    config = _episode_runtime_config()
    if source_kind is None:
        config.pop('episode_source_kind')
    else:
        config['episode_source_kind'] = source_kind
    lazyllm.globals['agentic_config'] = config

    result = MemoryTools().episode_create('需要保存', 'decision')

    assert result['success'] is False
    assert result['error']['code'] == expected_code
    assert result['error']['detail'] == {'field': 'episode_source_kind'}
    assert result['retryable'] is False


def test_episode_create_rejects_invalid_type_as_tool_failure():
    lazyllm.globals['agentic_config'] = _episode_runtime_config()

    result = MemoryTools().episode_create('需要保存', 'unknown')

    assert result['success'] is False
    assert result['error']['code'] == 'invalid_arguments'
    assert result['retryable'] is False


def test_episode_create_exposes_safe_retryable_store_initialization_failure(monkeypatch):
    config = _episode_runtime_config(source_kind='memory_review')
    lazyllm.globals['agentic_config'] = config
    memory_module = __import__(
        'lazymind.chat.engine.tools.memory',
        fromlist=['get_episode_store'],
    )

    def unavailable_store():
        raise ConnectionError(
            'backend temporarily unavailable https://core.internal/private '
            'Authorization: Bearer secret-value token=another-secret'
        )

    monkeypatch.setattr(memory_module, 'get_episode_store', unavailable_store)

    result = MemoryTools().episode_create('需要保存', 'decision')

    assert result['success'] is False
    assert result['error'] == {
        'code': 'storage_unavailable',
        'message': 'Episode storage is temporarily unavailable.',
        'detail': {'exception_type': 'ConnectionError'},
    }
    assert result['retryable'] is True
    assert 'secret-value' not in str(result)
    assert 'core.internal' not in str(result)
    ledger_result = config['memory_tool_results'][0]['result']
    assert ledger_result['retry_fingerprint'].startswith('episode_retry_')


def test_episode_create_does_not_retry_ambiguous_http_timeout(monkeypatch):
    def timed_out_transport(*_args, **_kwargs):
        raise requests.exceptions.Timeout()

    lazyllm.globals['agentic_config'] = _episode_runtime_config(
        source_kind='memory_review',
    )
    _patch_episode_store(monkeypatch, _store(timed_out_transport))

    result = MemoryTools().episode_create('需要保存', 'decision')

    assert result['success'] is False
    assert result['error']['code'] == 'storage_timeout'
    assert result['retryable'] is False
    ledger = lazyllm.globals['agentic_config']['memory_tool_results'][-1]
    assert ledger['mutation'] is None
    assert ledger['retryable'] is False


def test_episode_retry_ledger_uses_internal_fingerprint(monkeypatch):
    class FlakyCoreTransport(FakeCoreTransport):
        attempts = 0

        def __call__(self, method: str, url: str, **kwargs: Any) -> FakeResponse:
            if method == 'POST' and urlsplit(url).path == '/internal/memory/episodes':
                self.attempts += 1
                if self.attempts == 1:
                    raise ConnectionError('Core temporarily unavailable')
            return super().__call__(method, url, **kwargs)

    transport = FlakyCoreTransport()
    store = _store(transport)
    config = _episode_runtime_config(source_kind='memory_review')
    lazyllm.globals['agentic_config'] = config
    _patch_episode_store(monkeypatch, store)
    tools = MemoryTools()

    failed = tools.episode_create('采用 Core Episode', 'decision')
    saved = tools.episode_create('采用 Core Episode', 'decision')

    assert failed['success'] is False
    assert saved['success'] is True
    assert 'retry_fingerprint' not in saved['result']
    ledger = config['memory_tool_results']
    assert ledger[0]['result']['status'] == 'failed'
    assert (
        ledger[0]['result']['retry_fingerprint']
        == ledger[1]['result']['retry_fingerprint']
    )


def test_review_episode_uses_explicit_source_and_shared_conversation_time(monkeypatch):
    transport = FakeCoreTransport()
    store = _store(transport)
    lazyllm.globals['agentic_config'] = _episode_runtime_config(
        source_kind='memory_review',
    )
    _patch_episode_store(monkeypatch, store)
    tools = MemoryTools()

    first = tools.episode_create('发布方案确定为蓝色发布', 'decision')
    second = tools.episode_create('发布验证已经完成', 'result')

    assert first['success'] is True
    assert second['success'] is True
    assert len(transport.rows) == 2
    for row in transport.rows.values():
        assert row['occurred_at_ms'] == 1_700_000_000_000
        assert row['source_kind'] == 'memory_review'
        assert row['conversation_id'] == 'conv-1'


def test_chat_episode_is_reused_by_review_with_different_task_and_type(monkeypatch):
    transport = FakeCoreTransport()
    store = _store(transport)
    _patch_episode_store(monkeypatch, store)
    tools = MemoryTools()

    lazyllm.globals['agentic_config'] = _episode_runtime_config()
    chat_result = tools.episode_create('采用蓝色发布', 'decision')

    review_config = _episode_runtime_config(source_kind='memory_review')
    review_config['task_id'] = 'ordinary-review-task-1'
    review_config['episode_occurred_at_ms'] = 1_700_000_100_000
    lazyllm.globals['agentic_config'] = review_config
    review_result = tools.episode_create('  采用蓝色发布  ', 'result')

    assert chat_result['result']['status'] == 'created'
    assert review_result['result']['status'] == 'idempotent'
    assert review_result['result']['id'] == chat_result['result']['id']
    assert len(transport.rows) == 1
    stored = transport.rows[chat_result['result']['id']]
    assert stored['episode_type'] == 'decision'
    assert stored['source_kind'] == 'chat_explicit'
