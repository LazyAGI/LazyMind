from __future__ import annotations

import copy
import time

import lazyllm

from lazyllm.tools.rag.store import SegmentStoreConflictError

from lazymind.memory.episode import (
    EpisodeCreateInput, EpisodeSource, EpisodeStore, EpisodeType, tokenize_episode_text,
)
from lazymind.chat.engine.tools import episode_create as episode_create_tool


class FakeSegmentStore:
    def __init__(self):
        self.rows = {}

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

    def search(self, _collection, _query, topk, filters):
        result = []
        for index, item in enumerate(self.get(_collection, filters)):
            item['score'] = float(10 - index)
            result.append(item)
        return result[:topk]

    def patch(self, _collection, filters, set_fields=None, inc_fields=None):
        matches = self.get(_collection, filters)
        for match in matches:
            row = self.rows[match['id']]
            row['metadata']['hit_count'] += (inc_fields or {}).get('hit_count', 0)
        return len(matches)

    def delete(self, _collection, filters):
        for item in self.get(_collection, filters):
            del self.rows[item['id']]
        return True


def _episode(summary='采用 SegmentStore', episode_id=None):
    return EpisodeCreateInput(
        id=episode_id,
        occurred_at_ms=int(time.time() * 1000),
        thread_key='project:lazymind',
        episode_type=EpisodeType.DECISION,
        summary=summary,
        source=EpisodeSource(kind='chat_explicit', task_id='task-1'),
    )


def test_tokenizer_normalizes_mixed_chinese_and_ascii():
    result = tokenize_episode_text('用户决定使用 SegmentStore，版本Ｖ１')
    assert '用户' in result
    assert 'segmentstore' in result
    assert 'v1' in result


def test_create_many_is_idempotent_and_tenant_scoped():
    backend = FakeSegmentStore()
    store = EpisodeStore(backend)
    item = _episode()

    first = store.create_many('user-1', [item])
    retry = store.create_many('user-1', [item])

    assert first[0].status == 'created'
    assert retry[0].status == 'idempotent'
    assert len(backend.rows) == 1
    assert store.reset_episode('user-2') is True
    assert len(backend.rows) == 1


def test_same_explicit_id_with_different_content_is_rejected():
    store = EpisodeStore(FakeSegmentStore())
    assert store.create_many('user-1', [_episode('first', 'ep_fixed')])[0].status == 'created'
    result = store.create_many('user-1', [_episode('changed', 'ep_fixed')])[0]
    assert result.status == 'rejected'
    assert 'different immutable content' in result.reason


def test_search_renders_and_only_selected_hits_increment(monkeypatch):
    backend = FakeSegmentStore()
    store = EpisodeStore(backend)
    monkeypatch.setitem(__import__('lazymind.memory.episode', fromlist=['_cfg'])._cfg._impl,
                        'episode_score_threshold', 0.0)
    created = store.create_many('user-1', [_episode('first'), _episode('second')])

    results = store.search('user-1', 'SegmentStore')
    assert results
    assert 'Historical' not in results[0].rendered
    assert 'occurred_at:' in results[0].rendered
    ids = [result.episode.id for result in results[:1]]
    assert store.increment_hits('user-1', ids) == {ids[0]: True}
    assert backend.rows[ids[0]]['metadata']['hit_count'] == 1
    untouched = next(result.id for result in created if result.id not in ids)
    assert backend.rows[untouched]['metadata']['hit_count'] == 0


def test_episode_create_tool_uses_runtime_user_even_when_memory_retrieval_is_disabled(monkeypatch):
    backend = FakeSegmentStore()
    store = EpisodeStore(backend)
    lazyllm.globals['agentic_config'] = {
        'user_id': 'user-1', 'session_id': 'task-1', 'conversation_id': 'conv-1',
        'use_memory': False,
    }
    module = __import__('lazymind.chat.engine.tools.episode_create', fromlist=['get_episode_store'])
    monkeypatch.setattr(module, 'get_episode_store', lambda: store)

    result = episode_create_tool([{
        'occurred_at_ms': int(time.time() * 1000),
        'episode_type': 'event',
        'summary': '用户明确要求保存此事件',
        'source': {},
    }])

    assert result['success'] is True
    assert result['result']['items'][0]['status'] == 'created'
    stored = next(iter(backend.rows.values()))
    assert stored['metadata']['user_id'] == 'user-1'
    assert stored['metadata']['thread_key'] == 'conv-1'
