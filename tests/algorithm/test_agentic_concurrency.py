"""Concurrency tests for the current agentic pipeline entry points."""
from __future__ import annotations

import asyncio
import threading
from typing import Any, Dict, List

import pytest
import lazyllm

from chat.pipelines import agentic
from chat.components.agentic.config import (
    DEFAULT_TOOLS,
    _filter_tools_for_request,
)


def _expected_tools_for_request(config: Dict[str, Any]) -> tuple[str, ...]:
    request_config = dict(config)
    return tuple(_filter_tools_for_request(list(DEFAULT_TOOLS), request_config))


class _FakeAgent:
    """Fake agent that snapshots the per-request config visible at call time."""

    _lock = threading.Lock()
    observations: List[Dict[str, Any]] = []

    def __init__(self, **kwargs: Any) -> None:
        self._kwargs = kwargs

    def __call__(self, query: str, llm_chat_history: Any = None) -> Dict[str, Any]:
        config = lazyllm.globals.get('agentic_config')
        snapshot = dict(config) if isinstance(config, dict) else None
        callback = self._kwargs.get('stream_event_callback')
        if callable(callback):
            callback({
                'round': 1,
                'content': f'observed:{snapshot.get("algo_id") if snapshot else None}',
                'tool_calls': [],
            })
        with type(self)._lock:
            type(self).observations.append({
                'query': query,
                'sid': lazyllm.globals._sid,
                'config': snapshot,
                'agent_kwargs_prompt': self._kwargs.get('prompt'),
                'agent_kwargs_tools': tuple(self._kwargs.get('tools') or ()),
                'agent_kwargs_skills': tuple(self._kwargs.get('skills') or ()),
                'agent_kwargs_max_retries': self._kwargs.get('max_retries'),
                'agent_kwargs_force_summarize': self._kwargs.get('force_summarize'),
                'agent_kwargs_force_summarize_context': self._kwargs.get('force_summarize_context'),
            })
        return {
            'text': f'final:{query}',
            'observed_algo_id': snapshot.get('algo_id') if snapshot else None,
        }


@pytest.fixture
def fake_pipeline(monkeypatch):
    """Patch agentic's heavy external deps so it can run offline."""
    _FakeAgent.observations = []

    class _FakeFileSystemQueue:
        def __init__(self, *_args, **_kwargs):
            pass

        def clear(self):
            return None

        def dequeue(self):
            return []

        @classmethod
        def get_instance(cls, *_args, **_kwargs):
            return cls()

    monkeypatch.setattr(agentic, 'AutoModel', lambda *_a, **_kw: object())
    monkeypatch.setattr(agentic, '_ensure_tools_registered', lambda: None)
    monkeypatch.setattr(agentic, '_augment_query_with_attached_images', lambda query, config: query)
    monkeypatch.setattr(agentic, '_build_review_decision', lambda **_kw: {'mode': None})
    monkeypatch.setattr(agentic, '_clear_orphaned_lazyllm_queue_lock', lambda: None)
    monkeypatch.setattr(agentic, '_spawn_background_review', lambda **_kw: None)
    monkeypatch.setattr(agentic, '_StreamingReactAgent', _FakeAgent)
    monkeypatch.setattr(lazyllm.tools.agent, 'ReactAgent', _FakeAgent)
    monkeypatch.setattr(lazyllm, 'FileSystemQueue', _FakeFileSystemQueue)

    yield _FakeAgent


def _build_configs(prefix: str, n: int) -> List[Dict[str, Any]]:
    return [
        {
            'query': f'{prefix}{i}',
            'kb_id': f'{prefix}id_{i}',
            'algo_id': f'{prefix}algo_{i}',
            'available_tools': [f'tool_{prefix}{i}'],
            'available_skills': [f'skill_{prefix}{i}'],
        }
        for i in range(n)
    ]


def test_thread_parallel_requests_see_isolated_config(fake_pipeline):
    n = 8
    configs = _build_configs('t_', n)
    results: List[Any] = [None] * n
    barrier = threading.Barrier(n)

    def _run(i: int) -> None:
        lazyllm.globals._init_sid(sid=f'sync-session-{i}')
        lazyllm.locals._init_sid(sid=f'sync-session-{i}')
        lazyllm.globals['agentic_config'] = dict(configs[i])
        barrier.wait()
        results[i] = agentic.agentic_forward(query=configs[i]['query'], history=[])

    threads = [threading.Thread(target=_run, args=(i,)) for i in range(n)]
    for t in threads:
        t.start()
    for t in threads:
        t.join()

    assert len(fake_pipeline.observations) == n
    obs_by_query = {obs['query']: obs for obs in fake_pipeline.observations}
    assert set(obs_by_query.keys()) == {f't_{i}' for i in range(n)}

    sids = set()
    for i in range(n):
        obs = obs_by_query[f't_{i}']
        sids.add(obs['sid'])
        assert obs['sid'] == f'sync-session-{i}'
        assert obs['config']['kb_id'] == f't_id_{i}'
        assert obs['config']['algo_id'] == f't_algo_{i}'
        assert obs['agent_kwargs_tools'][0] == f'tool_t_{i}'
        assert obs['config']['available_skills'] == [f'skill_t_{i}']
        assert results[i]['observed_algo_id'] == f't_algo_{i}'

    assert len(sids) == n, f'threads should get distinct SIDs, got {sids!r}'


def test_stream_parallel_requests_see_isolated_config(fake_pipeline):
    n = 6

    async def _drive():
        async def _one(i: int):
            session_id = f'stream-session-{i}'
            lazyllm.globals._init_sid(sid=session_id)
            lazyllm.locals._init_sid(sid=session_id)
            params = {
                'query': f's_{i}',
                'algo_id': f's_algo_{i}',
                'filters': {'kb_id': f's_id_{i}'},
                'available_tools': [f's_tool_{i}'],
                'available_skills': [f's_skill_{i}'],
            }
            stream = agentic.agentic_rag(params)
            events = []
            async for event in stream:
                events.append(event)
            outer = lazyllm.globals.get('agentic_config')
            return events, outer, session_id

        tasks = [asyncio.create_task(_one(i)) for i in range(n)]
        return await asyncio.gather(*tasks)

    results = asyncio.run(_drive())

    assert len(fake_pipeline.observations) == n
    obs_by_query = {obs['query']: obs for obs in fake_pipeline.observations}
    assert set(obs_by_query.keys()) == {f's_{i}' for i in range(n)}

    for i in range(n):
        obs = obs_by_query[f's_{i}']
        assert obs['sid'] == f'stream-session-{i}'
        assert obs['config']['kb_id'] == f's_id_{i}'
        assert obs['config']['algo_id'] == f's_algo_{i}'
        assert obs['agent_kwargs_tools'][0] == f's_tool_{i}'
        assert obs['config']['available_skills'] == [f's_skill_{i}']

    for i, (events, outer, session_id) in enumerate(results):
        assert session_id == f'stream-session-{i}'
        assert events
        assert isinstance(outer, dict)
        assert outer.get('algo_id') == f's_algo_{i}', (
            'the asyncio task should still see its own agentic_config after the '
            'streaming worker finishes'
        )


def test_expected_tool_filter_matches_runtime_config():
    request_config = {
        'kb_id': 'kb-1',
    }

    filtered = _expected_tools_for_request(request_config)

    assert filtered == tuple(DEFAULT_TOOLS)
