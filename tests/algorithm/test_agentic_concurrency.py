"""Concurrency tests for the current chat streaming entry point."""
from __future__ import annotations

import asyncio
import json
import threading
from typing import Any, Dict, List

import lazyllm
from lazymind.chat.service import chat_service


class _FakeAgent:
    """Fake agent that snapshots the per-request config visible at call time."""

    _lock = threading.Lock()
    observations: List[Dict[str, Any]] = []

    def __init__(self, **kwargs: Any) -> None:
        self._kwargs = kwargs

    def _observe(self, query: str) -> Dict[str, Any]:
        config = chat_service.lazyllm.globals.get('agentic_config')
        snapshot = dict(config) if isinstance(config, dict) else None
        with type(self)._lock:
            type(self).observations.append({
                'query': query,
                'sid': chat_service.lazyllm.globals._sid,
                'config': snapshot,
                'agent_kwargs_prompt': self._kwargs.get('prompt'),
                'agent_kwargs_tools': tuple(self._kwargs.get('tools') or ()),
                'agent_kwargs_skills': tuple(self._kwargs.get('skills') or ()),
                'agent_kwargs_max_retries': self._kwargs.get('max_retries'),
                'agent_kwargs_force_summarize': self._kwargs.get('force_summarize'),
                'agent_kwargs_force_summarize_context': self._kwargs.get('force_summarize_context'),
            })
        return {'text': f'final:{query}'}

    def forward(self, query: str, llm_chat_history: Any = None):
        self._observe(query)
        chat_service.lazyllm.FileSystemQueue().enqueue(json.dumps({'tag': 'text', 'delta': f'stream:{query}'}))
        return {'text': f'final:{query}'}


async def _drain_response(response):
    chunks = []
    async for chunk in response.body_iterator:
        if isinstance(chunk, bytes):
            chunk = chunk.decode('utf-8')
        chunks.append(chunk)
    return ''.join(chunks)


def test_stream_parallel_requests_see_isolated_config(monkeypatch):
    _FakeAgent.observations = []
    monkeypatch.setattr(chat_service, 'AutoModel', lambda *_a, **_kw: object())
    monkeypatch.setattr(chat_service.lazyllm.tools.agent, 'ReactAgent', _FakeAgent)

    async def drive_one(i: int):
        session_id = f'stream-session-{i}'
        params = {
            'query': f's_{i}',
            'kb_id': f's_id_{i}',
            'available_tools': ['calculator'],
            'available_skills': [f's_skill_{i}'],
        }
        response = await chat_service.handle_chat(
            query=params['query'],
            history=[],
            session_id=session_id,
            filters={'kb_id': params['kb_id']},
            files=None,
            debug=False,
            reasoning=False,
            databases=None,
            dataset='default',
            priority=None,
            available_tools=params['available_tools'],
            available_skills=params['available_skills'],
            memory=None,
            user_preference=None,
            use_memory=True,
            model_config={},
        )
        body = await _drain_response(response)
        outer = chat_service.lazyllm.globals.get('agentic_config')
        return body, outer, session_id

    async def drive_all():
        return await asyncio.gather(*(drive_one(i) for i in range(6)))

    results = asyncio.run(drive_all())

    assert len(_FakeAgent.observations) == 6
    obs_by_query = {obs['query']: obs for obs in _FakeAgent.observations}
    assert set(obs_by_query.keys()) == {f's_{i}' for i in range(6)}

    for i in range(6):
        obs = obs_by_query[f's_{i}']
        assert obs['sid'] == f'stream-session-{i}'
        assert obs['config']['filters']['kb_id'] == f's_id_{i}'
        assert obs['agent_kwargs_skills'] == (f's_skill_{i}',)

    for i, (body, outer, session_id) in enumerate(results):
        assert session_id == f'stream-session-{i}'
        assert f'stream:s_{i}' in body
        assert outer is None


def test_stream_response_keeps_session_after_route_context_exits(monkeypatch):
    _FakeAgent.observations = []
    monkeypatch.setattr(chat_service, 'AutoModel', lambda *_a, **_kw: object())
    monkeypatch.setattr(chat_service.lazyllm.tools.agent, 'ReactAgent', _FakeAgent)

    async def drive():
        session_id = 'route-stream-session'
        with lazyllm.new_session(session_id):
            response = await chat_service.handle_chat(
                query='route_query',
                history=[],
                session_id=session_id,
                filters={'kb_id': 'route_kb'},
                files=None,
                debug=False,
                reasoning=False,
                databases=None,
                dataset='default',
                priority=None,
                available_tools=['calculator'],
                available_skills=['route_skill'],
                memory=None,
                user_preference=None,
                use_memory=True,
                model_config={},
            )
        return await _drain_response(response)

    body = asyncio.run(drive())

    assert 'stream:route_query' in body
    assert len(_FakeAgent.observations) == 1
    obs = _FakeAgent.observations[0]
    assert obs['sid'] == 'route-stream-session'
    assert obs['config']['session_id'] == 'route-stream-session'
    assert obs['config']['filters']['kb_id'] == 'route_kb'
