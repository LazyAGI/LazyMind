from __future__ import annotations

import copy
import inspect

import lazyllm
import pytest

from lazyllm.tools.rag.store import BUILDIN_GLOBAL_META_DESC, create_segment_store
from lazyllm.tools.agent.toolsManager import ToolManager

import lazymind.common.memory.episode_store as episode_store_module
from lazymind.chat.engine.tools.memory import MemoryTools
from lazymind.common.memory import (
    EpisodeConflictError,
    EpisodeCreateInput,
    EpisodeSource,
    EpisodeStore,
    EpisodeType,
    normalize_episode_summary,
    tokenize_episode_text,
)


class FakeSegmentStore:
    def __init__(self):
        self.rows = {}
        self.strict_gets = []
        self.get_calls = []
        self.counter_calls = []
        self.search_calls = []
        self.scores = {}

    def create(self, _collection, data):
        for item in data:
            if item['uid'] in self.rows:
                raise FileExistsError(item['uid'])
        for item in data:
            self.rows[item['uid']] = copy.deepcopy(item)
        return True

    def get(self, _collection, criteria=None, **kwargs):
        self.strict_gets.append(kwargs.get('raise_on_error', False))
        self.get_calls.append(copy.deepcopy(criteria or {}))
        result = []
        for item in self.rows.values():
            for field, expected in (criteria or {}).items():
                if item.get(field) != expected:
                    break
            else:
                result.append(copy.deepcopy(item))
        return result

    def search(
        self,
        collection,
        query,
        topk,
        filters,
        *,
        query_fields=None,
        match_mode=None,
        raise_on_error=False,
    ):
        self.search_calls.append({
            'collection': collection,
            'query': query,
            'topk': topk,
            'filters': copy.deepcopy(filters),
            'query_fields': copy.deepcopy(query_fields),
            'match_mode': match_mode,
            'raise_on_error': raise_on_error,
        })
        result = []
        for item in self.get(collection, filters):
            item['score'] = self.scores.get(item['uid'], 1.0)
            result.append(item)
        return result[:topk]

    def increment_counters(self, collection, criteria, increments):
        self.counter_calls.append({
            'collection': collection,
            'criteria': copy.deepcopy(criteria),
            'increments': copy.deepcopy(increments),
        })
        matches = self.get(collection, criteria)
        for match in matches:
            row = self.rows[match['uid']]
            counters = row.setdefault('counters', {})
            for name, value in increments.items():
                counters[name] = counters.get(name, 0) + value
        return len(matches)

    def delete(self, collection, criteria):
        for item in self.get(collection, criteria):
            del self.rows[item['uid']]
        return True


def _sqlite_store(db_path):
    store = create_segment_store({
        'type': 'SQLiteStore',
        'kwargs': {'db_path': str(db_path)},
    })
    store.connect(global_metadata_desc=BUILDIN_GLOBAL_META_DESC)
    return store


def _episode(
    summary='采用 SegmentStore',
    *,
    occurred_at_ms=1_700_000_000_000,
    conversation_id='conv-1',
    episode_type=EpisodeType.DECISION,
):
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


def test_default_episode_store_requires_sqlite_path(monkeypatch):
    monkeypatch.setitem(episode_store_module._cfg._impl, 'segment_store_type', 'SQLiteStore')
    monkeypatch.setitem(episode_store_module._cfg._impl, 'segment_store_uri_or_path', '')

    with pytest.raises(
        ValueError,
        match='LAZYMIND_SEGMENT_STORE_URI_OR_PATH is required for SQLite segment store',
    ):
        EpisodeStore()


def test_default_episode_store_requires_opensearch_uri(monkeypatch):
    monkeypatch.setitem(episode_store_module._cfg._impl, 'segment_store_type', 'opensearch')
    monkeypatch.setitem(episode_store_module._cfg._impl, 'segment_store_uri_or_path', '')

    with pytest.raises(
        ValueError,
        match='LAZYMIND_SEGMENT_STORE_URI_OR_PATH is required for OpenSearch segment store',
    ):
        EpisodeStore()


def test_episode_store_config_supports_sqlite(monkeypatch, tmp_path):
    db_path = tmp_path / 'episodes.db'
    monkeypatch.setitem(episode_store_module._cfg._impl, 'segment_store_type', 'SQLiteStore')
    monkeypatch.setitem(
        episode_store_module._cfg._impl,
        'segment_store_uri_or_path',
        str(db_path),
    )

    assert episode_store_module._store_config() == {
        'type': 'SQLiteStore',
        'kwargs': {'db_path': str(db_path)},
    }


def test_episode_store_config_supports_opensearch(monkeypatch):
    monkeypatch.setitem(episode_store_module._cfg._impl, 'segment_store_type', 'opensearch')
    monkeypatch.setitem(
        episode_store_module._cfg._impl,
        'segment_store_uri_or_path',
        'https://opensearch.test:9200',
    )
    monkeypatch.setitem(episode_store_module._cfg._impl, 'segment_store_user', 'user')
    monkeypatch.setitem(episode_store_module._cfg._impl, 'segment_store_password', 'password')

    config = episode_store_module._store_config()

    assert config['type'] == 'opensearch'
    assert config['kwargs']['uris'] == 'https://opensearch.test:9200'
    assert config['kwargs']['client_kwargs']['user'] == 'user'
    assert config['kwargs']['client_kwargs']['password'] == 'password'


def test_default_episode_store_rejects_elasticsearch(monkeypatch):
    monkeypatch.setitem(episode_store_module._cfg._impl, 'segment_store_type', 'elasticsearch')
    monkeypatch.setitem(
        episode_store_module._cfg._impl,
        'segment_store_uri_or_path',
        'https://elasticsearch.test:9200',
    )

    with pytest.raises(ValueError, match="Unsupported segment store type: 'elasticsearch'"):
        EpisodeStore()


def test_injected_episode_store_does_not_read_default_configuration(monkeypatch):
    backend = FakeSegmentStore()
    monkeypatch.setattr(
        episode_store_module,
        '_create_default_store',
        lambda: (_ for _ in ()).throw(AssertionError('default config should not be read')),
    )

    store = EpisodeStore(backend)

    assert store._store is backend


def test_default_episode_store_uses_factory_and_connects_adapter(monkeypatch):
    class DefaultStore(FakeSegmentStore):
        supports_counters = True

        def connect(self, **kwargs):
            self.connect_kwargs = kwargs

    backend = DefaultStore()
    config = {'type': 'SQLiteStore', 'kwargs': {'db_path': '/tmp/episodes.db'}}
    monkeypatch.setattr(episode_store_module, '_store_config', lambda: config)
    monkeypatch.setattr(
        episode_store_module,
        'create_segment_store',
        lambda received: backend if received is config else None,
    )

    store = EpisodeStore()

    assert store._store is backend
    assert backend.connect_kwargs == {'global_metadata_desc': BUILDIN_GLOBAL_META_DESC}


def test_single_create_is_idempotent_when_only_time_and_summary_format_change():
    backend = FakeSegmentStore()
    store = EpisodeStore(backend, clock_ms=lambda: 1_800_000_000_000)

    first = store.create('user-1', _episode('  Use  SegmentStore V1  ', occurred_at_ms=1000))
    retry = store.create('user-1', _episode('use segmentstore v１', occurred_at_ms=2000))

    assert first.status == 'created'
    assert retry.status == 'idempotent'
    assert retry.id == first.id
    assert retry.idempotency_key == first.idempotency_key == first.id
    assert first.id.startswith('ep_')
    assert len(backend.rows) == 1
    stored = backend.rows[first.id]['meta']
    assert stored['occurred_at_ms'] == 1000
    assert stored['recorded_at_ms'] == 1_800_000_000_000
    assert stored['summary'] == 'Use  SegmentStore V1'
    assert stored['type'] == 'decision'
    assert 'episode_type' not in stored
    assert {'id', 'user_id', 'hit_count'}.isdisjoint(stored)


def test_episode_store_writes_and_reads_only_the_v1_minimal_contract():
    backend = FakeSegmentStore()
    store = EpisodeStore(backend)

    created = store.create('user-1', _episode('V1 Episode 元数据'))
    row = backend.rows[created.id]
    stored_source = row['meta']['source']

    assert store.collection == 'lazymind_memory_episode_v1'
    assert row['doc_id'] == 'conv-1'
    assert stored_source == {
        'kind': 'chat_explicit',
        'conversation_id': 'conv-1',
    }
    record = store.list_by_conversation('user-1', 'conv-1')[0]

    assert record.source.model_dump() == {
        'kind': 'chat_explicit',
        'conversation_id': 'conv-1',
    }
    assert set(record.model_dump()) == {
        'occurred_at_ms',
        'episode_type',
        'summary',
        'source',
        'id',
        'recorded_at_ms',
        'user_id',
        'hit_count',
    }


def test_store_false_create_result_is_not_reported_as_created():
    class RejectingSegmentStore(FakeSegmentStore):
        def create(self, _collection, _data):
            return False

    store = EpisodeStore(RejectingSegmentStore())

    with pytest.raises(RuntimeError, match='did not confirm Episode creation'):
        store.create('user-1', _episode())


def test_list_by_conversation_is_tenant_scoped_and_oldest_first():
    backend = FakeSegmentStore()
    recorded_times = iter([2000, 1000, 3000, 4000])
    store = EpisodeStore(backend, clock_ms=lambda: next(recorded_times))

    later = store.create('user-1', _episode('稍后记录', occurred_at_ms=100)).id
    earlier = store.create('user-1', _episode('更早记录', occurred_at_ms=200)).id
    store.create('user-1', _episode('其他会话', conversation_id='conv-2'))
    store.create('user-2', _episode('其他用户'))

    records = store.list_by_conversation('user-1', 'conv-1')

    assert [record.id for record in records] == [earlier, later]
    assert all(record.user_id == 'user-1' for record in records)
    assert all(record.source.conversation_id == 'conv-1' for record in records)
    assert backend.strict_gets and all(backend.strict_gets)
    assert backend.get_calls[-1] == {'kb_id': 'user-1', 'doc_id': 'conv-1'}


def test_list_by_conversation_ignores_malformed_rows_from_other_conversations():
    backend = FakeSegmentStore()
    store = EpisodeStore(backend)
    expected = store.create('user-1', _episode('目标会话记录'))
    unrelated = store.create(
        'user-1',
        _episode('其他会话记录', conversation_id='conv-2'),
    )
    backend.rows[unrelated.id]['meta'] = {'malformed': True}

    records = store.list_by_conversation('user-1', 'conv-1')

    assert [record.id for record in records] == [expected.id]


def test_episode_hit_count_reads_only_the_named_counter():
    backend = FakeSegmentStore()
    store = EpisodeStore(backend)
    created = store.create('user-1', _episode('V1 命中计数'))
    row = backend.rows[created.id]
    row['meta']['hit_count'] = 3
    row['number'] = 5
    row['counters']['hit_count'] = 7

    assert store.list_by_conversation('user-1', 'conv-1')[0].hit_count == 7

    row['counters'] = {}
    assert store.list_by_conversation('user-1', 'conv-1')[0].hit_count == 0


def test_idempotency_key_is_tenant_conversation_and_summary_scoped():
    backend = FakeSegmentStore()
    store = EpisodeStore(backend)

    baseline = store.create('user-1', _episode())
    assert store.create('user-2', _episode()).id != baseline.id
    assert store.create('user-1', _episode(conversation_id='conv-2')).id != baseline.id
    assert store.create('user-1', _episode(episode_type=EpisodeType.RESULT)).id == baseline.id
    assert len(backend.rows) == 3


def test_create_reuses_oldest_existing_id_for_same_conversation_summary():
    backend = FakeSegmentStore()
    store = EpisodeStore(backend, clock_ms=lambda: 2000)
    created = store.create('user-1', _episode('采用 SegmentStore'))
    template = backend.rows.pop(created.id)

    later = copy.deepcopy(template)
    later['uid'] = 'ep_duplicate_later'
    later['meta']['recorded_at_ms'] = 2000
    earlier = copy.deepcopy(template)
    earlier['uid'] = 'ep_duplicate_earlier'
    earlier['meta']['recorded_at_ms'] = 1000
    backend.rows[later['uid']] = later
    backend.rows[earlier['uid']] = earlier

    result = store.create(
        'user-1',
        _episode('  采用  SegmentStore  ', episode_type=EpisodeType.RESULT),
    )

    assert result.status == 'idempotent'
    assert result.id == 'ep_duplicate_earlier'
    assert result.idempotency_key == created.id
    assert set(backend.rows) == {'ep_duplicate_later', 'ep_duplicate_earlier'}


def test_create_conflict_strictly_verifies_same_tenant_and_identity():
    seed_backend = FakeSegmentStore()
    seed_store = EpisodeStore(seed_backend)
    created = seed_store.create('user-1', _episode())

    class RacingSegmentStore(FakeSegmentStore):
        def __init__(self, row):
            super().__init__()
            self.rows[row['uid']] = copy.deepcopy(row)
            self._first_get = True

        def get(self, collection, criteria=None, **kwargs):
            if self._first_get:
                self._first_get = False
                self.strict_gets.append(kwargs.get('raise_on_error', False))
                return []
            return super().get(collection, criteria, **kwargs)

        def create(self, _collection, data):
            raise FileExistsError(data[0]['uid'])

    backend = RacingSegmentStore(seed_backend.rows[created.id])
    result = EpisodeStore(backend).create('user-1', _episode())

    assert result.status == 'idempotent'
    assert result.id == created.id
    assert backend.strict_gets == [True, True]


@pytest.mark.parametrize(
    ('mutate_row', 'message'),
    [
        (
            lambda row: row.__setitem__('kb_id', 'user-2'),
            'outside the current tenant identity',
        ),
        (
            lambda row: row['meta'].__setitem__('summary', '不同的 Episode 内容'),
            'different idempotent content',
        ),
    ],
)
def test_create_conflict_rejects_wrong_tenant_or_identity(mutate_row, message):
    seed_backend = FakeSegmentStore()
    created = EpisodeStore(seed_backend).create('user-1', _episode())
    conflicting_row = copy.deepcopy(seed_backend.rows[created.id])
    mutate_row(conflicting_row)

    class RacingSegmentStore(FakeSegmentStore):
        def __init__(self):
            super().__init__()
            self.rows[conflicting_row['uid']] = conflicting_row
            self._first_get = True

        def get(self, collection, criteria=None, **kwargs):
            if self._first_get:
                self._first_get = False
                self.strict_gets.append(kwargs.get('raise_on_error', False))
                return []
            return super().get(collection, criteria, **kwargs)

        def create(self, _collection, data):
            raise FileExistsError(data[0]['uid'])

    backend = RacingSegmentStore()

    with pytest.raises(EpisodeConflictError, match=message):
        EpisodeStore(backend).create('user-1', _episode())

    assert backend.strict_gets == [True, True]


@pytest.mark.parametrize('summary', ['', 'x' * 201])
def test_episode_input_requires_summary_between_one_and_two_hundred_characters(summary):
    with pytest.raises(ValueError):
        EpisodeCreateInput(
            occurred_at_ms=1_700_000_000_000,
            episode_type='decision',
            summary=summary,
            source=EpisodeSource(
                kind='chat_explicit',
                conversation_id='conv-1',
            ),
        )


def test_search_uses_content_any_and_hard_filters_weak_unrelated_candidate():
    backend = FakeSegmentStore()
    store = EpisodeStore(backend)
    relevant = store.create('user-1', _episode('项目验证码是火星苹果42'))
    unrelated = store.create('user-1', _episode('今天讨论了部署窗口'))
    backend.scores[relevant.id] = 0.000001
    backend.scores[unrelated.id] = 100.0

    results = store.search('user-1', '还记得火星苹果42吗', now_ms=1_700_000_000_000)

    assert [item.episode.id for item in results] == [relevant.id]
    assert results[0].bm25_score == 0.000001
    assert results[0].score <= 1.0
    assert backend.search_calls[-1]['query_fields'] == ['content']
    assert backend.search_calls[-1]['match_mode'] == 'any'
    assert backend.search_calls[-1]['filters'] == {'kb_id': 'user-1'}
    assert backend.search_calls[-1]['raise_on_error'] is True


def test_search_converts_strict_backend_failure_to_safe_episode_error():
    class FailingSearchStore(FakeSegmentStore):
        def search(self, *args, raise_on_error=False, **kwargs):
            assert raise_on_error is True
            raise ConnectionError(
                'OpenSearch unavailable at '
                'https://admin:secret@opensearch.internal:9200/private-index'
            )

    with pytest.raises(episode_store_module.EpisodeReadError) as captured:
        EpisodeStore(FailingSearchStore()).search('user-1', 'mars apple')

    assert captured.value.code == 'storage_unavailable'
    assert captured.value.retryable is True
    assert str(captured.value) == 'Failed to load existing Episodes.'
    assert 'opensearch.internal' not in str(captured.value)


def test_search_ranks_by_coverage_recency_and_hit_count(monkeypatch):
    day_ms = 86_400_000
    now_ms = 2_000_000_000_000
    backend = FakeSegmentStore()
    store = EpisodeStore(backend)
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
    backend.rows[popular.id]['counters']['hit_count'] = 10
    monkeypatch.setitem(episode_store_module._cfg._impl, 'episode_relevance_weight', 0.8)
    monkeypatch.setitem(episode_store_module._cfg._impl, 'episode_recency_weight', 0.1)
    monkeypatch.setitem(episode_store_module._cfg._impl, 'episode_hit_weight', 0.1)
    monkeypatch.setitem(episode_store_module._cfg._impl, 'episode_half_life_days', 10.0)
    monkeypatch.setitem(episode_store_module._cfg._impl, 'episode_hit_saturation', 10)

    results = store.search('user-1', 'mars apple banana', now_ms=now_ms)

    assert [item.episode.id for item in results] == [high_coverage.id, popular.id, recent.id]


def test_increment_hits_uses_atomic_counter_with_tenant_scope():
    backend = FakeSegmentStore()
    store = EpisodeStore(backend)
    own = store.create('user-1', _episode())
    other = store.create('user-2', _episode())

    result = store.increment_hits('user-1', [own.id, other.id, own.id])

    assert result == {own.id: True, other.id: False}
    assert backend.rows[own.id]['counters']['hit_count'] == 1
    assert backend.rows[other.id]['counters']['hit_count'] == 0
    assert backend.counter_calls == [
        {
            'collection': episode_store_module.EPISODE_COLLECTION,
            'criteria': {'uid': own.id, 'kb_id': 'user-1'},
            'increments': {'hit_count': 1},
        },
        {
            'collection': episode_store_module.EPISODE_COLLECTION,
            'criteria': {'uid': other.id, 'kb_id': 'user-1'},
            'increments': {'hit_count': 1},
        },
    ]


def test_search_requires_all_short_query_terms_and_exact_identifiers():
    backend = FakeSegmentStore()
    store = EpisodeStore(backend)
    exact = store.create('user-1', _episode('错误编号 ERR-42 来自 payment_api'))
    store.create('user-1', _episode('错误编号 ERR-43 来自 payment_api'))
    store.create('user-1', _episode('错误编号 ERR-42 来自 profile_api'))

    results = store.search('user-1', 'ERR-42 payment_api')

    assert [item.episode.id for item in results] == [exact.id]
    search_count = len(backend.search_calls)
    assert store.search('user-1', '还记得吗') == []
    assert store.search('user-1', '帮我看看') == []
    assert store.search('user-1', '你知道吗') == []
    assert store.search('user-1', '继续') == []
    assert store.search('user-1', '为什么') == []
    assert store.search('user-1', '再详细说说') == []
    assert store.search('user-1', '请继续介绍') == []
    assert store.search('user-1', '好不好') == []
    assert store.search('user-1', '帮我查一下') == []
    assert store.search('user-1', '给我看看') == []
    assert store.search('user-1', '多说一点') == []
    assert len(backend.search_calls) == search_count


def test_numeric_query_requires_complete_ascii_identifier_boundary():
    backend = FakeSegmentStore()
    store = EpisodeStore(backend)
    exact = store.create('user-1', _episode('错误编号是 42'))
    store.create('user-1', _episode('错误编号是 42A'))
    store.create('user-1', _episode('错误编号是 A42'))

    results = store.search('user-1', '42')

    assert [item.episode.id for item in results] == [exact.id]


def test_structured_numeric_identifier_requires_same_order_and_separators():
    backend = FakeSegmentStore()
    store = EpisodeStore(backend)
    exact = store.create('user-1', _episode('工单编号是 123-456'))
    store.create('user-1', _episode('工单编号是 456-123'))
    store.create('user-1', _episode('工单编号是 123/456'))

    results = store.search('user-1', '123-456')

    assert [item.episode.id for item in results] == [exact.id]


def test_sqlite_episode_contract_recalls_natural_question_and_keeps_tenant_scope(tmp_path):
    backend = _sqlite_store(tmp_path / 'episodes.db')
    store = EpisodeStore(backend)
    relevant = store.create('user-1', _episode('项目验证码是火星苹果42'))
    store.create('user-1', _episode('今天讨论了部署窗口'))
    store.create('user-2', _episode('另一个用户的验证码也是火星苹果42'))

    results = store.search('user-1', '还记得火星苹果42吗')

    assert [result.episode.id for result in results] == [relevant.id]
    assert all(result.episode.user_id == 'user-1' for result in results)


def test_sqlite_conversation_list_and_exact_dedup_keep_tenant_scope(tmp_path):
    backend = _sqlite_store(tmp_path / 'conversation-episodes.db')
    store = EpisodeStore(backend)
    first = store.create('user-1', _episode('采用蓝色发布'))
    duplicate = store.create(
        'user-1',
        _episode('  采用蓝色发布  ', episode_type=EpisodeType.RESULT),
    )
    second = store.create('user-1', _episode('发布验证已经完成'))
    store.create('user-1', _episode('其他会话记录', conversation_id='conv-2'))
    store.create('user-2', _episode('其他用户记录'))

    records = store.list_by_conversation('user-1', 'conv-1')

    assert duplicate.status == 'idempotent'
    assert duplicate.id == first.id
    assert [record.id for record in records] == [first.id, second.id]
    assert all(record.user_id == 'user-1' for record in records)
    assert all(record.source.conversation_id == 'conv-1' for record in records)


def test_sqlite_episode_counter_and_reset_complete_lifecycle(tmp_path):
    backend = _sqlite_store(tmp_path / 'episode-lifecycle.db')
    store = EpisodeStore(backend)
    created = store.create('user-1', _episode('完整生命周期'))

    assert store.increment_hits('user-1', [created.id]) == {created.id: True}
    assert store.list_by_conversation('user-1', 'conv-1')[0].hit_count == 1

    assert store.reset_episode('user-1') is True
    assert store.list_by_conversation('user-1', 'conv-1') == []


@pytest.mark.parametrize(
    ('query', 'summary'),
    [
        ('v1.2.3', '当前发布版本是 v1.2.3'),
        ('ERR42', '线上错误编号是 ERR42'),
        ('build2026', '当前构建标识是 build2026'),
    ],
)
def test_sqlite_episode_contract_recalls_exact_alphanumeric_identifier(
    tmp_path, query, summary,
):
    backend = _sqlite_store(tmp_path / f'{query}.db')
    store = EpisodeStore(backend)
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
    assert not hasattr(MemoryTools, 'memory_editor')
    episode_schema = descriptions['MemoryTools_episode_create']['parameters']
    assert set(episode_schema['properties']) == {'summary', 'episode_type'}
    assert set(episode_schema['required']) == {'summary', 'episode_type'}
    assert episode_schema['properties']['episode_type']['type'] == 'string'
    assert 'decision, progress, result, blocker, or event' in (
        episode_schema['properties']['episode_type']['description']
    )


def test_episode_create_uses_runtime_context_even_when_retrieval_is_disabled(monkeypatch):
    backend = FakeSegmentStore()
    store = EpisodeStore(backend, clock_ms=lambda: 1_800_000_000_000)
    lazyllm.globals['agentic_config'] = {
        'user_id': 'user-1',
        'task_id': 'memory_review_misleading-chat-task',
        'conversation_id': 'conv-1',
        'episode_occurred_at_ms': 1_700_000_000_000,
        'episode_source_kind': 'chat_explicit',
        'use_memory': False,
        'memory_tool_results': [],
    }
    memory_module = __import__('lazymind.chat.engine.tools.memory', fromlist=['get_episode_store'])
    monkeypatch.setattr(memory_module, 'get_episode_store', lambda: store)

    result = MemoryTools().episode_create('用户明确要求保存此事件', 'event')

    assert result['success'] is True
    assert result['result']['status'] == 'created'
    assert result['result']['id'].startswith('ep_')
    row = next(iter(backend.rows.values()))
    stored = row['meta']
    assert row['kb_id'] == 'user-1'
    assert row['doc_id'] == 'conv-1'
    assert stored['occurred_at_ms'] == 1_700_000_000_000
    assert stored['source'] == {
        'kind': 'chat_explicit',
        'conversation_id': 'conv-1',
    }
    ledger = lazyllm.globals['agentic_config']['memory_tool_results']
    assert ledger == [{
        'tool': 'episode_create',
        'success': True,
        'mutation': True,
        'result': {
            'status': 'created',
            'idempotency_key': result['result']['idempotency_key'],
        },
        'retryable': False,
    }]


def test_episode_create_reports_missing_context_to_agent_and_ledger():
    lazyllm.globals['agentic_config'] = {
        'user_id': 'user-1',
        'task_id': 'task-1',
        'memory_tool_results': [],
    }

    result = MemoryTools().episode_create('需要保存', 'decision')

    assert result['success'] is False
    assert result['error']['code'] == 'missing_context'
    assert result['error']['message'] == 'conversation_id is required in agentic_config.'
    assert result['error']['detail'] == {'field': 'conversation_id'}
    assert result['retryable'] is False
    assert lazyllm.globals['agentic_config']['memory_tool_results'][-1] == {
        'tool': 'episode_create',
        'success': False,
        'mutation': False,
        'error': {
            'code': 'missing_context',
            'message': 'conversation_id is required in agentic_config.',
            'detail': {'field': 'conversation_id'},
        },
        'retryable': False,
    }


@pytest.mark.parametrize(
    ('source_kind', 'expected_code'),
    [(None, 'missing_context'), ('unsupported_source', 'invalid_arguments')],
)
def test_episode_create_requires_valid_explicit_source_kind(source_kind, expected_code):
    config = {
        'user_id': 'user-1',
        'conversation_id': 'conv-1',
        'episode_occurred_at_ms': 1_700_000_000_000,
        'memory_tool_results': [],
    }
    if source_kind is not None:
        config['episode_source_kind'] = source_kind
    lazyllm.globals['agentic_config'] = config

    result = MemoryTools().episode_create('需要保存', 'decision')

    assert result['success'] is False
    assert result['error']['code'] == expected_code
    assert result['error']['detail'] == {'field': 'episode_source_kind'}
    assert result['retryable'] is False
    assert config['memory_tool_results'][-1]['mutation'] is False


def test_episode_create_rejects_invalid_type_as_tool_failure():
    lazyllm.globals['agentic_config'] = {
        'user_id': 'user-1',
        'task_id': 'task-1',
        'conversation_id': 'conv-1',
        'episode_occurred_at_ms': 1_700_000_000_000,
        'episode_source_kind': 'chat_explicit',
        'memory_tool_results': [],
    }

    result = MemoryTools().episode_create('需要保存', 'unknown')

    assert result['success'] is False
    assert result['error']['code'] == 'invalid_arguments'
    assert result['retryable'] is False
    assert lazyllm.globals['agentic_config']['memory_tool_results'][-1]['success'] is False


def test_episode_create_exposes_safe_retryable_storage_failure(monkeypatch):
    lazyllm.globals['agentic_config'] = {
        'user_id': 'user-1',
        'task_id': 'memory_review_conv-1',
        'conversation_id': 'conv-1',
        'episode_occurred_at_ms': 1_700_000_000_000,
        'episode_source_kind': 'memory_review',
        'memory_tool_results': [],
    }
    memory_module = __import__('lazymind.chat.engine.tools.memory', fromlist=['get_episode_store'])

    def unavailable_store():
        raise ConnectionError(
            'backend temporarily unavailable https://opensearch.internal:9200/private-index '
            'Authorization: Bearer secret-value '
            'http_auth=(admin, admin-secret)'
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
    ledger_error = lazyllm.globals['agentic_config']['memory_tool_results'][-1]['error']
    assert ledger_error == result['error']
    assert 'secret-value' not in str(result)
    assert 'admin-secret' not in str(ledger_error)
    assert 'opensearch.internal' not in str(result)


def test_episode_create_marks_dedup_lookup_failure_as_retryable_without_mutation(monkeypatch):
    class FailingLookupStore(FakeSegmentStore):
        def get(self, _collection, _criteria=None, *, raise_on_error=False):
            assert raise_on_error is True
            raise ConnectionError(
                'OpenSearch temporarily unavailable token=secret-value '
                'https://opensearch.internal:9200/private-index'
            )

    backend = FailingLookupStore()
    store = EpisodeStore(backend)
    lazyllm.globals['agentic_config'] = {
        'user_id': 'user-1',
        'task_id': 'memory_review_conv-1',
        'conversation_id': 'conv-1',
        'episode_occurred_at_ms': 1_700_000_000_000,
        'episode_source_kind': 'memory_review',
        'memory_tool_results': [],
    }
    memory_module = __import__('lazymind.chat.engine.tools.memory', fromlist=['get_episode_store'])
    monkeypatch.setattr(memory_module, 'get_episode_store', lambda: store)

    result = MemoryTools().episode_create('需要保存', 'decision')

    assert result['success'] is False
    assert result['error']['code'] == 'storage_unavailable'
    assert result['error']['message'] == 'Episode storage is temporarily unavailable.'
    assert result['retryable'] is True
    ledger = lazyllm.globals['agentic_config']['memory_tool_results'][-1]
    assert ledger['mutation'] is False
    assert ledger['retryable'] is True
    assert not backend.rows
    assert 'secret-value' not in str(result)
    assert 'opensearch.internal' not in str(result)


def test_episode_create_does_not_retry_ambiguous_write_timeout(monkeypatch):
    class TimedOutStore:
        def create(self, _user_id, _item):
            raise TimeoutError('timed out waiting for create response')

    lazyllm.globals['agentic_config'] = {
        'user_id': 'user-1',
        'task_id': 'memory_review_conv-1',
        'conversation_id': 'conv-1',
        'episode_occurred_at_ms': 1_700_000_000_000,
        'episode_source_kind': 'memory_review',
        'memory_tool_results': [],
    }
    memory_module = __import__('lazymind.chat.engine.tools.memory', fromlist=['get_episode_store'])
    monkeypatch.setattr(memory_module, 'get_episode_store', lambda: TimedOutStore())

    result = MemoryTools().episode_create('需要保存', 'decision')

    assert result['success'] is False
    assert result['error']['code'] == 'storage_timeout'
    assert result['retryable'] is False
    ledger = lazyllm.globals['agentic_config']['memory_tool_results'][-1]
    assert ledger['mutation'] is None
    assert ledger['retryable'] is False


def test_episode_retry_ledger_uses_same_key_so_later_success_can_resolve_failure(monkeypatch):
    class FlakySegmentStore(FakeSegmentStore):
        attempts = 0

        def create(self, collection, data):
            self.attempts += 1
            if self.attempts == 1:
                raise ConnectionError('backend temporarily unavailable')
            return super().create(collection, data)

    backend = FlakySegmentStore()
    store = EpisodeStore(backend)
    lazyllm.globals['agentic_config'] = {
        'user_id': 'user-1',
        'task_id': 'memory_review_conv-1',
        'conversation_id': 'conv-1',
        'episode_occurred_at_ms': 1_700_000_000_000,
        'episode_source_kind': 'memory_review',
        'memory_tool_results': [],
    }
    memory_module = __import__('lazymind.chat.engine.tools.memory', fromlist=['get_episode_store'])
    monkeypatch.setattr(memory_module, 'get_episode_store', lambda: store)
    tools = MemoryTools()

    failed = tools.episode_create('采用 SegmentStore', 'decision')
    saved = tools.episode_create('采用 SegmentStore', 'decision')

    assert failed['success'] is False
    assert saved['success'] is True
    ledger = lazyllm.globals['agentic_config']['memory_tool_results']
    assert ledger[0]['result']['status'] == 'failed'
    assert ledger[0]['result']['idempotency_key'] == saved['result']['idempotency_key']
    assert ledger[1]['result']['idempotency_key'] == saved['result']['idempotency_key']
    stored = backend.rows[saved['result']['id']]['meta']
    assert stored['occurred_at_ms'] == 1_700_000_000_000
    assert stored['source']['kind'] == 'memory_review'
    assert 'task_id' not in stored['source']
    assert stored['source']['conversation_id'] == 'conv-1'


def test_review_episode_uses_explicit_source_and_shared_conversation_time(monkeypatch):
    backend = FakeSegmentStore()
    store = EpisodeStore(backend, clock_ms=lambda: 1_800_000_000_000)
    lazyllm.globals['agentic_config'] = {
        'user_id': 'user-1',
        'task_id': 'ordinary-review-task-without-prefix',
        'conversation_id': 'conv-1',
        'episode_occurred_at_ms': 1_700_000_000_000,
        'episode_source_kind': 'memory_review',
        'memory_tool_results': [],
    }
    memory_module = __import__('lazymind.chat.engine.tools.memory', fromlist=['get_episode_store'])
    monkeypatch.setattr(memory_module, 'get_episode_store', lambda: store)
    tools = MemoryTools()

    first = tools.episode_create('发布方案确定为蓝色发布', 'decision')
    second = tools.episode_create('发布验证已经完成', 'result')

    assert first['success'] is True
    assert second['success'] is True
    assert len(backend.rows) == 2
    for row in backend.rows.values():
        stored = row['meta']
        assert stored['occurred_at_ms'] == 1_700_000_000_000
        assert stored['source'] == {
            'kind': 'memory_review',
            'conversation_id': 'conv-1',
        }


def test_chat_episode_is_reused_by_review_with_different_task_and_type(monkeypatch):
    backend = FakeSegmentStore()
    store = EpisodeStore(backend, clock_ms=lambda: 1_800_000_000_000)
    memory_module = __import__('lazymind.chat.engine.tools.memory', fromlist=['get_episode_store'])
    monkeypatch.setattr(memory_module, 'get_episode_store', lambda: store)
    tools = MemoryTools()

    lazyllm.globals['agentic_config'] = {
        'user_id': 'user-1',
        'task_id': 'memory_review_misleading-chat-task',
        'conversation_id': 'conv-1',
        'episode_occurred_at_ms': 1_700_000_000_000,
        'episode_source_kind': 'chat_explicit',
        'memory_tool_results': [],
    }
    chat_result = tools.episode_create('采用蓝色发布', 'decision')

    lazyllm.globals['agentic_config'] = {
        'user_id': 'user-1',
        'task_id': 'ordinary-review-task-1',
        'conversation_id': 'conv-1',
        'episode_occurred_at_ms': 1_700_000_100_000,
        'episode_source_kind': 'memory_review',
        'memory_tool_results': [],
    }
    review_result = tools.episode_create('  采用蓝色发布  ', 'result')

    assert chat_result['result']['status'] == 'created'
    assert review_result['result']['status'] == 'idempotent'
    assert review_result['result']['id'] == chat_result['result']['id']
    assert len(backend.rows) == 1
    stored = backend.rows[chat_result['result']['id']]['meta']
    assert stored['type'] == 'decision'
    assert stored['source'] == {
        'kind': 'chat_explicit',
        'conversation_id': 'conv-1',
    }
