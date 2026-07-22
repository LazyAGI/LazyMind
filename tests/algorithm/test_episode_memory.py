from __future__ import annotations

import copy
import inspect

import lazyllm
import pytest

from lazyllm.tools.rag.store import SegmentStore, SegmentStoreConflictError
from lazyllm.tools.agent.toolsManager import ToolManager

from lazymind.chat.engine.tools.memory import MemoryTools
from lazymind.common.memory import (
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
        self.search_calls = []
        self.scores = {}

    def create(self, _collection, data):
        for item in data:
            if item['id'] in self.rows:
                raise SegmentStoreConflictError(item['id'])
        for item in data:
            self.rows[item['id']] = copy.deepcopy(item)
        return True

    def get(self, _collection, filters):
        result = []
        for item in self.rows.values():
            if filters.get('id') and item['id'] != filters['id']:
                continue
            if filters.get('user_id') and item['metadata']['user_id'] != filters['user_id']:
                continue
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
    ):
        self.search_calls.append({
            'collection': collection,
            'query': query,
            'topk': topk,
            'filters': copy.deepcopy(filters),
            'query_fields': copy.deepcopy(query_fields),
            'match_mode': match_mode,
        })
        result = []
        for item in self.get(collection, filters):
            item['score'] = self.scores.get(item['id'], 1.0)
            result.append(item)
        return result[:topk]

    def patch(self, collection, filters, set_fields=None, inc_fields=None):
        matches = self.get(collection, filters)
        for match in matches:
            row = self.rows[match['id']]
            row['metadata']['hit_count'] += (inc_fields or {}).get('hit_count', 0)
        return len(matches)

    def delete(self, collection, filters):
        for item in self.get(collection, filters):
            del self.rows[item['id']]
        return True


def _episode(
    summary='采用 SegmentStore',
    *,
    occurred_at_ms=1_700_000_000_000,
    task_id='task-1',
    conversation_id='conv-1',
    episode_type=EpisodeType.DECISION,
):
    return EpisodeCreateInput(
        occurred_at_ms=occurred_at_ms,
        thread_key=conversation_id,
        episode_type=episode_type,
        summary=summary,
        source=EpisodeSource(
            kind='chat_explicit',
            task_id=task_id,
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
    stored = backend.rows[first.id]['metadata']
    assert stored['occurred_at_ms'] == 1000
    assert stored['recorded_at_ms'] == 1_800_000_000_000
    assert stored['summary'] == 'Use  SegmentStore V1'


def test_store_false_create_result_is_not_reported_as_created():
    class RejectingSegmentStore(FakeSegmentStore):
        def create(self, _collection, _data):
            return False

    store = EpisodeStore(RejectingSegmentStore())

    with pytest.raises(RuntimeError, match='did not confirm Episode creation'):
        store.create('user-1', _episode())


def test_idempotency_key_is_tenant_conversation_task_and_type_scoped():
    backend = FakeSegmentStore()
    store = EpisodeStore(backend)

    baseline = store.create('user-1', _episode())
    assert store.create('user-2', _episode()).id != baseline.id
    assert store.create('user-1', _episode(conversation_id='conv-2')).id != baseline.id
    assert store.create('user-1', _episode(task_id='task-2')).id != baseline.id
    assert store.create('user-1', _episode(episode_type=EpisodeType.RESULT)).id != baseline.id


def test_episode_input_requires_thread_key_to_match_source_conversation():
    with pytest.raises(ValueError, match='thread_key must equal source.conversation_id'):
        EpisodeCreateInput(
            occurred_at_ms=1_700_000_000_000,
            thread_key='different-conversation',
            episode_type='decision',
            summary='采用 SegmentStore',
            source=EpisodeSource(
                kind='chat_explicit',
                task_id='task-1',
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
    assert backend.search_calls[-1]['filters'] == {'user_id': 'user-1'}


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
    backend = SegmentStore({
        'type': 'SQLiteStore',
        'kwargs': {'db_path': str(tmp_path / 'episodes.db')},
    })
    store = EpisodeStore(backend)
    relevant = store.create('user-1', _episode('项目验证码是火星苹果42'))
    store.create('user-1', _episode('今天讨论了部署窗口'))
    store.create('user-2', _episode('另一个用户的验证码也是火星苹果42'))

    results = store.search('user-1', '还记得火星苹果42吗')

    assert [result.episode.id for result in results] == [relevant.id]
    assert all(result.episode.user_id == 'user-1' for result in results)


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
    backend = SegmentStore({
        'type': 'SQLiteStore',
        'kwargs': {'db_path': str(tmp_path / f'{query}.db')},
    })
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
        'task_id': 'task-1',
        'conversation_id': 'conv-1',
        'episode_occurred_at_ms': 1_700_000_000_000,
        'use_memory': False,
        'memory_tool_results': [],
    }
    memory_module = __import__('lazymind.chat.engine.tools.memory', fromlist=['get_episode_store'])
    monkeypatch.setattr(memory_module, 'get_episode_store', lambda: store)

    result = MemoryTools().episode_create('用户明确要求保存此事件', 'event')

    assert result['success'] is True
    assert result['result']['status'] == 'created'
    assert result['result']['id'].startswith('ep_')
    stored = next(iter(backend.rows.values()))['metadata']
    assert stored['user_id'] == 'user-1'
    assert stored['thread_key'] == 'conv-1'
    assert stored['occurred_at_ms'] == 1_700_000_000_000
    assert stored['source'] == {
        'kind': 'chat_explicit',
        'task_id': 'task-1',
        'conversation_id': 'conv-1',
        'message_ids': [],
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


def test_episode_create_rejects_invalid_type_as_tool_failure():
    lazyllm.globals['agentic_config'] = {
        'user_id': 'user-1',
        'task_id': 'task-1',
        'conversation_id': 'conv-1',
        'episode_occurred_at_ms': 1_700_000_000_000,
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
        'review_started_at_ms': 1_700_000_000_000,
        'memory_tool_results': [],
    }
    memory_module = __import__('lazymind.chat.engine.tools.memory', fromlist=['get_episode_store'])

    def unavailable_store():
        raise ConnectionError(
            'backend temporarily unavailable '
            'Authorization: Bearer secret-value '
            'http_auth=(admin, admin-secret)'
        )

    monkeypatch.setattr(memory_module, 'get_episode_store', unavailable_store)

    result = MemoryTools().episode_create('需要保存', 'decision')

    assert result['success'] is False
    assert result['error'] == {
        'code': 'storage_unavailable',
        'message': (
            'Episode storage is temporarily unavailable: backend temporarily unavailable '
            'Authorization: <redacted> http_auth=<redacted>'
        ),
        'detail': {'exception_type': 'ConnectionError'},
    }
    assert result['retryable'] is True
    ledger_error = lazyllm.globals['agentic_config']['memory_tool_results'][-1]['error']
    assert ledger_error == result['error']
    assert 'secret-value' not in str(result)
    assert 'admin-secret' not in str(ledger_error)


def test_episode_create_does_not_retry_ambiguous_write_timeout(monkeypatch):
    class TimedOutStore:
        def create(self, _user_id, _item):
            raise TimeoutError('timed out waiting for create response')

    lazyllm.globals['agentic_config'] = {
        'user_id': 'user-1',
        'task_id': 'memory_review_conv-1',
        'conversation_id': 'conv-1',
        'review_started_at_ms': 1_700_000_000_000,
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
        'review_started_at_ms': 1_700_000_000_000,
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
    stored = backend.rows[saved['result']['id']]['metadata']
    assert stored['thread_key'] == 'conv-1'
    assert stored['occurred_at_ms'] == 1_700_000_000_000
    assert stored['source']['kind'] == 'memory_review'
    assert stored['source']['task_id'] == 'memory_review_conv-1'
    assert stored['source']['conversation_id'] == 'conv-1'
