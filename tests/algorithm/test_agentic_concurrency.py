"""Concurrency tests for the current agentic pipeline entry points."""
from __future__ import annotations

import asyncio
import threading
from typing import Any, Dict, List

import pytest
import lazyllm

from lazymind.chat.service.agentic import runtime as agentic


class _FakeAgent:
    """Fake agent that snapshots the per-request config visible at call time."""

    _lock = threading.Lock()
    observations: List[Dict[str, Any]] = []

    def __init__(self, **kwargs: Any) -> None:
        self._kwargs = kwargs

    def _observe(self, query: str) -> Dict[str, Any]:
        config = lazyllm.globals.get('agentic_config')
        snapshot = dict(config) if isinstance(config, dict) else None
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

    def __call__(self, query: str, llm_chat_history: Any = None) -> Dict[str, Any]:
        return self._observe(query)

    def stream(self, query: str, llm_chat_history: Any = None):
        result = self._observe(query)

        def _iter():
            yield {
                'type': 'agent.text.delta',
                'delta': f'stream:{query}',
            }
            return result

        return _iter()


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

    monkeypatch.setattr(agentic, 'AutoModel', lambda *_a, **_kw: object(), raising=False)
    monkeypatch.setattr(agentic, '_build_review_decision', lambda **_kw: {'mode': None}, raising=False)
    monkeypatch.setattr(agentic, '_spawn_background_review', lambda **_kw: None, raising=False)
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
                'kb_id': f's_id_{i}',
                'available_tools': [f's_tool_{i}'],
                'available_skills': [f's_skill_{i}'],
                'stream': True,
            }
            stream = agentic.stream_agentic_runtime(
                query=params['query'],
                history=[],
                runtime_params=params,
                agent_components={
                    'llm': object(),
                    'runtime_tools': params['available_tools'],
                },
                global_sid=lazyllm.globals._sid,
                local_sid=lazyllm.locals._sid,
                trace_config=lazyllm.globals.get('trace') or {},
            )
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
