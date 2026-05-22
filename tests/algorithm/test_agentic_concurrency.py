"""Concurrency tests for ``chat.pipelines.agentic``.

Verify that when multiple requests run in parallel — either via OS threads
(sync path) or asyncio tasks driving the streaming generator — each request's
``agentic_config`` is isolated, and the tools invoked inside each request
observe their own per-request configuration without cross-contamination.

The design relies on ``lazyllm.globals`` being keyed by a per-session id
(SID). Production code in ``chat_service.handle_chat`` calls
``lazyllm.globals._init_sid(sid=session_id)`` before running the pipeline so
every incoming request lands in its own SID bucket. These tests exercise
exactly that contract for both the sync and streaming entry points.
"""
from __future__ import annotations

import asyncio
import threading
import time
from typing import Any, Dict, List

import pytest
import lazyllm

from config import config as algo_config
from chat.pipelines import agentic
from chat.components.agentic import tool_stream
from chat.components.agentic import review as agentic_review
from chat.components.agentic.config import (
    DEFAULT_TOOLS,
    _filter_tools_for_request,
    _merge_builtin_file_tools,
)


def _expected_tools_for_request(config: Dict[str, Any]) -> tuple[str, ...]:
    request_config = dict(config)
    return tuple(_filter_tools_for_request(list(DEFAULT_TOOLS), request_config))


class _FakeAgent:
    """Fake ReactAgent that records the ``agentic_config`` visible at call time.

    Instances capture whatever kwargs the pipeline uses to build a real
    ``ReactAgent`` (``prompt``, ``tools``, ``skills``, ...), and when invoked
    they simulate a tool-call round that reads ``lazyllm.globals`` to retrieve
    the per-request config — mirroring what real tools like ``kb_search`` do.
    """

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
                'content': f'observed:{snapshot.get("kb_name") if snapshot else None}',
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
            'query': query,
            'observed_kb_name': snapshot.get('kb_name') if snapshot else None,
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
    monkeypatch.setattr(agentic, 'create_sandbox', lambda **_kw: object())
    monkeypatch.setattr(agentic, '_ensure_tools_registered', lambda: None)
    monkeypatch.setattr(agentic, '_spawn_background_review', lambda **_kw: None)
    monkeypatch.setattr(agentic, '_get_runtime_agent_defaults', lambda: {})
    monkeypatch.setattr(agentic, '_StreamingReactAgent', _FakeAgent)
    monkeypatch.setattr(lazyllm.tools.agent, 'ReactAgent', _FakeAgent)
    monkeypatch.setattr(lazyllm, 'FileSystemQueue', _FakeFileSystemQueue)

    yield _FakeAgent


def _build_configs(prefix: str, n: int) -> List[Dict[str, Any]]:
    return [
        {
            'query': f'{prefix}{i}',
            'kb_name': f'{prefix}kb_{i}',
            'kb_id': f'{prefix}id_{i}',
            'kb_url': f'http://{prefix}host/{i}',
            'available_tools': [f'tool_{prefix}{i}'],
            'available_skills': [f'skill_{prefix}{i}'],
            'skill_fs_url': f'file:///tmp/{prefix}skills/{i}',
        }
        for i in range(n)
    ]


def test_thread_parallel_requests_see_isolated_config(fake_pipeline):
    """Each OS-thread request gets its own ``agentic_config`` snapshot."""
    n = 8
    configs = _build_configs('t_', n)
    results: List[Any] = [None] * n
    barrier = threading.Barrier(n)

    def _run(i: int) -> None:
        lazyllm.globals._init_sid(sid=f'sync-session-{i}')
        lazyllm.locals._init_sid(sid=f'sync-session-{i}')
        barrier.wait()
        results[i] = agentic.agentic_rag(configs[i], stream=False)

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
        assert obs['config']['kb_name'] == f't_kb_{i}'
        assert obs['config']['kb_id'] == f't_id_{i}'
        assert obs['config']['kb_url'] == f'http://t_host/{i}'
        assert obs['agent_kwargs_tools'] == (f'tool_t_{i}',)
        assert obs['config']['available_skills'] == [f'skill_t_{i}']
        assert results[i]['observed_kb_name'] == f't_kb_{i}'

    assert len(sids) == n, f'threads should get distinct SIDs, got {sids!r}'


def test_stream_parallel_requests_see_isolated_config(fake_pipeline):
    """Each asyncio-task streaming request observes only its own config.

    The streaming path spawns a dedicated worker thread per request and
    re-initialises the SID inside it so the worker shares the caller's
    ``agentic_config``. This guards that wiring against regressions.
    """
    n = 6

    async def _drive():
        async def _one(i: int):
            # Mirror chat_service.handle_chat: every incoming request first
            # pins its own SID so globals writes land in an isolated bucket.
            session_id = f'stream-session-{i}'
            lazyllm.globals._init_sid(sid=session_id)
            lazyllm.locals._init_sid(sid=session_id)
            params = {
                'query': f's_{i}',
                'kb_name': f's_kb_{i}',
                'kb_id': f's_id_{i}',
                'kb_url': f'http://s_host/{i}',
                'available_tools': [f's_tool_{i}'],
                'available_skills': [f's_skill_{i}'],
                'skill_fs_url': f'file:///tmp/stream-skills/{i}',
            }
            stream = agentic.agentic_rag(params, stream=True)
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
        assert obs['config']['kb_name'] == f's_kb_{i}'
        assert obs['config']['kb_id'] == f's_id_{i}'
        assert obs['config']['kb_url'] == f'http://s_host/{i}'
        assert obs['agent_kwargs_tools'] == (f's_tool_{i}',)
        assert obs['config']['available_skills'] == [f's_skill_{i}']

    for i, (events, outer, session_id) in enumerate(results):
        assert session_id == f'stream-session-{i}'
        assert isinstance(outer, dict)
        assert outer.get('kb_name') == f's_kb_{i}', (
            'the asyncio task should still see its own agentic_config after '
            'the streaming worker finishes'
        )


def test_stream_monitor_uses_request_sid_for_realtime_flush(fake_pipeline, monkeypatch):
    class _RecordingQueue:
        _items: dict[str, list[str]] = {}
        _log: list[tuple[str, str, str, list[str] | str | None, float]] = []
        _lock = threading.Lock()

        def __init__(self, klass='__default__'):
            self._class = klass

        @classmethod
        def get_instance(cls, klass):
            return cls(klass=klass)

        @property
        def sid(self) -> str:
            return f'{lazyllm.globals._sid}-{self._class}'

        def clear(self):
            with type(self)._lock:
                type(self)._items[self.sid] = []
                type(self)._log.append(('clear', threading.current_thread().name, self.sid, None, time.perf_counter()))

        def enqueue(self, message):
            with type(self)._lock:
                type(self)._items.setdefault(self.sid, []).append(message)
                type(self)._log.append((
                    'enqueue',
                    threading.current_thread().name,
                    self.sid,
                    str(message),
                    time.perf_counter(),
                ))

        def dequeue(self, limit=None):
            del limit
            with type(self)._lock:
                values = list(type(self)._items.get(self.sid, []))
                type(self)._items[self.sid] = []
                type(self)._log.append((
                    'dequeue',
                    threading.current_thread().name,
                    self.sid,
                    list(values),
                    time.perf_counter(),
                ))
                return values

    def _fake_agentic_forward(*, query, history, stream_event_callback=None):
        del query, history, stream_event_callback
        lazyllm.FileSystemQueue().enqueue('hello')
        time.sleep(0.12)
        lazyllm.FileSystemQueue().enqueue(' world')
        time.sleep(0.12)
        return {'text': 'hello world', 'sources': []}

    monkeypatch.setattr(agentic.lazyllm, 'FileSystemQueue', _RecordingQueue)
    monkeypatch.setattr(agentic, 'agentic_forward', _fake_agentic_forward)

    lazyllm.globals._init_sid(sid='stream-monitor-session')
    lazyllm.locals._init_sid(sid='stream-monitor-session')

    async def _collect():
        started = time.perf_counter()
        frames = []
        async for item in agentic.agentic_rag({'query': 'hello'}, stream=True):
            frames.append((time.perf_counter() - started, item))
        return frames

    frames = asyncio.run(_collect())

    assert frames
    assert frames[0][0] < 0.2, f'expected realtime flush before worker exit, got {frames!r}'
    assert frames[0][1]['text'] == 'hello'
    assert frames[-1][1]['text'] == ' world'

    monitor_dequeues = [
        log for log in _RecordingQueue._log
        if log[0] == 'dequeue' and '_stream_monitor' in log[1]
    ]
    assert monitor_dequeues, 'expected monitor thread to poll the stream queue'
    assert all(log[2].startswith('stream-monitor-session-') for log in monitor_dequeues), monitor_dequeues
    assert not any(log[2].startswith('tid-') for log in monitor_dequeues), monitor_dequeues


def test_stream_monitor_does_not_cross_read_other_sessions(fake_pipeline, monkeypatch):
    class _RecordingQueue:
        _items: dict[str, list[str]] = {}
        _log: list[tuple[str, str, str, list[str] | str | None, float]] = []
        _lock = threading.Lock()

        def __init__(self, klass='__default__'):
            self._class = klass

        @classmethod
        def get_instance(cls, klass):
            return cls(klass=klass)

        @property
        def sid(self) -> str:
            return f'{lazyllm.globals._sid}-{self._class}'

        def clear(self):
            with type(self)._lock:
                type(self)._items[self.sid] = []
                type(self)._log.append(('clear', threading.current_thread().name, self.sid, None, time.perf_counter()))

        def enqueue(self, message):
            with type(self)._lock:
                type(self)._items.setdefault(self.sid, []).append(message)
                type(self)._log.append((
                    'enqueue',
                    threading.current_thread().name,
                    self.sid,
                    str(message),
                    time.perf_counter(),
                ))

        def dequeue(self, limit=None):
            del limit
            with type(self)._lock:
                values = list(type(self)._items.get(self.sid, []))
                type(self)._items[self.sid] = []
                type(self)._log.append((
                    'dequeue',
                    threading.current_thread().name,
                    self.sid,
                    list(values),
                    time.perf_counter(),
                ))
                return values

    def _fake_agentic_forward(*, query, history, stream_event_callback=None):
        del history, stream_event_callback
        lazyllm.FileSystemQueue().enqueue(f'{query}:1')
        time.sleep(0.06)
        lazyllm.FileSystemQueue().enqueue(f'{query}:2')
        time.sleep(0.06)
        return {'text': f'{query}:1{query}:2', 'sources': []}

    monkeypatch.setattr(agentic.lazyllm, 'FileSystemQueue', _RecordingQueue)
    monkeypatch.setattr(agentic, 'agentic_forward', _fake_agentic_forward)

    async def _consume(i: int):
        session_id = f'monitor-cross-session-{i}'
        lazyllm.globals._init_sid(sid=session_id)
        lazyllm.locals._init_sid(sid=session_id)
        frames = []
        async for item in agentic.agentic_rag({'query': f'q{i}'}, stream=True):
            frames.append(item['text'])
        return session_id, frames

    async def _run_all():
        return await asyncio.gather(*(_consume(i) for i in range(6)))

    results = asyncio.run(_run_all())

    for i, (session_id, frames) in enumerate(results):
        assert session_id == f'monitor-cross-session-{i}'
        assert frames == [f'q{i}:1', f'q{i}:2']

    monitor_dequeues = [
        log for log in _RecordingQueue._log
        if log[0] == 'dequeue' and '_stream_monitor' in log[1]
    ]
    assert monitor_dequeues, 'expected monitor threads to drain queues'
    for thread_name, touched_sids in {
        name: {log[2] for log in monitor_dequeues if log[1] == name}
        for name in {log[1] for log in monitor_dequeues}
    }.items():
        session_sids = {sid for sid in touched_sids if sid.endswith('-__default__') or sid.endswith('-think')}
        prefixes = {
            sid[:-len('-__default__')] if sid.endswith('-__default__') else sid[:-len('-think')]
            for sid in session_sids
        }
        assert len(prefixes) == 1, f'{thread_name} touched multiple session prefixes: {sorted(session_sids)!r}'


def test_stream_clears_orphaned_lazyllm_queue_lock(fake_pipeline, monkeypatch, tmp_path):
    fake_home = tmp_path / 'lazy-home'
    fake_home.mkdir()
    lock_path = fake_home / '.lazyllm_filesystem_queue.db.lock'
    lock_path.write_text('')

    monkeypatch.setattr(agentic, '_stream_frame', lambda **kwargs: kwargs)
    monkeypatch.setattr(agentic, '_lazyllm_queue_db_path', lambda: fake_home / '.lazyllm_filesystem_queue.db')

    lazyllm.globals._init_sid(sid='stream-stale-lock-session')
    lazyllm.locals._init_sid(sid='stream-stale-lock-session')

    async def _consume():
        stream = agentic.agentic_rag({'query': 'hello'}, stream=True)
        return [event async for event in stream]

    events = asyncio.run(_consume())

    assert isinstance(events, list)
    assert fake_pipeline.observations
    assert not lock_path.exists()


def test_kb_tools_disabled_without_kb_id_or_files(fake_pipeline):
    lazyllm.globals._init_sid(sid='no-kb-session')
    lazyllm.locals._init_sid(sid='no-kb-session')

    agentic.agentic_rag({
        'query': 'hello',
        'available_tools': ['all'],
        'filters': {},
        'files': [],
    })

    assert fake_pipeline.observations[-1]['agent_kwargs_tools'] == _expected_tools_for_request({
        'files': [],
        'temp_files': [],
    })


def test_single_file_request_keeps_temp_file_search_only(fake_pipeline):
    lazyllm.globals._init_sid(sid='file-session')
    lazyllm.locals._init_sid(sid='file-session')

    agentic.agentic_rag({
        'query': 'summarize this file',
        'available_tools': ['all'],
        'filters': {},
        'files': ['/var/lib/lazymind/uploads/a.pdf'],
    })

    obs = fake_pipeline.observations[-1]
    assert obs['config']['temp_files'] == ['/var/lib/lazymind/uploads/a.pdf']
    assert obs['agent_kwargs_tools'] == _expected_tools_for_request({
        'files': ['/var/lib/lazymind/uploads/a.pdf'],
        'temp_files': ['/var/lib/lazymind/uploads/a.pdf'],
    })


def test_request_does_not_override_runtime_agent_defaults(fake_pipeline, monkeypatch):
    monkeypatch.setattr(agentic, '_get_runtime_agent_defaults', lambda: {
        'available_tools': ['memory'],
        'skill_fs_url': 'remote://skills',
    })

    lazyllm.globals._init_sid(sid='runtime-default-session')
    lazyllm.locals._init_sid(sid='runtime-default-session')

    agentic.agentic_rag({
        'query': 'hello',
    })

    obs = fake_pipeline.observations[-1]
    assert obs['config']['skill_fs_url'] == 'remote://skills,.agentic/skills'
    assert obs['agent_kwargs_tools'] == ('memory', 'vocab_manage')


def test_stream_rewrites_citations_like_naive(fake_pipeline, monkeypatch):
    class _FakeQueue:
        _default_values = [['事实 [['], ['1.1]]']]
        _named_values = {'think': []}

        def __init__(self, name=None):
            self.name = name

        def clear(self):
            return None

        def dequeue(self):
            if self.name:
                values = type(self)._named_values.get(self.name, [])
                return values.pop(0) if values else []
            values = type(self)._default_values
            return values.pop(0) if values else []

        @classmethod
        def get_instance(cls, name):
            return cls(name)

    source = {
        'index': '1.1',
        'display_index': 1,
        'document_index': 1,
        'chunk_index': 1,
        'segment_number': 21,
        'document_id': 'doc-1',
        'page': -1,
        'bbox': [],
        'dataset_id': 'kb-1',
        'file_name': 'Doc.md',
        'segement_id': 'seg-1',
        'content': 'Source body',
        'group_name': 'block',
    }

    def _fake_agentic_forward(*, query, history, stream_event_callback=None):
        config = lazyllm.globals.get('agentic_config')
        config['_citation_sources'] = {'1.1': source}
        return {'text': '事实 [[1.1]]', 'sources': []}

    monkeypatch.setattr(agentic.lazyllm, 'FileSystemQueue', _FakeQueue)
    monkeypatch.setattr(agentic, 'agentic_forward', _fake_agentic_forward)

    lazyllm.globals._init_sid(sid='stream-citation-session')
    lazyllm.locals._init_sid(sid='stream-citation-session')

    async def _collect():
        stream = agentic.agentic_rag({'query': 'hello'}, stream=True)
        return [item async for item in stream]

    frames = asyncio.run(_collect())

    assert frames == [
        {'think': None, 'text': '事实 ', 'sources': []},
        {'think': None, 'text': '', 'sources': [source]},
    ]


def test_stream_keeps_sources_when_final_result_already_contains_links(fake_pipeline, monkeypatch):
    class _FakeQueue:
        _default_values = [['事实 ['], ['1](#source-1.1 "Doc.md")']]
        _named_values = {'think': []}

        def __init__(self, name=None):
            self.name = name

        def clear(self):
            return None

        def dequeue(self):
            if self.name:
                values = type(self)._named_values.get(self.name, [])
                return values.pop(0) if values else []
            values = type(self)._default_values
            return values.pop(0) if values else []

        @classmethod
        def get_instance(cls, name):
            return cls(name)

    source = {
        'index': '1.1',
        'display_index': 1,
        'document_index': 1,
        'chunk_index': 1,
        'segment_number': 21,
        'document_id': 'doc-1',
        'page': -1,
        'bbox': [],
        'dataset_id': 'kb-1',
        'file_name': 'Doc.md',
        'segement_id': 'seg-1',
        'content': 'Source body',
        'group_name': 'block',
    }

    def _fake_agentic_forward(*, query, history, stream_event_callback=None):
        config = lazyllm.globals.get('agentic_config')
        config['_citation_sources'] = {'1.1': source}
        return {'text': '事实 [1](#source-1.1 "Doc.md")', 'sources': []}

    monkeypatch.setattr(agentic.lazyllm, 'FileSystemQueue', _FakeQueue)
    monkeypatch.setattr(agentic, 'agentic_forward', _fake_agentic_forward)

    lazyllm.globals._init_sid(sid='stream-citation-link-session')
    lazyllm.locals._init_sid(sid='stream-citation-link-session')

    async def _collect():
        stream = agentic.agentic_rag({'query': 'hello'}, stream=True)
        return [item async for item in stream]

    frames = asyncio.run(_collect())

    assert frames == [
        {'think': None, 'text': '事实 ', 'sources': []},
        {'think': None, 'text': '', 'sources': [source]},
    ]


def test_format_non_stream_result_collects_existing_source_links(fake_pipeline):
    source = {
        'index': '2.1',
        'display_index': 2,
        'document_index': 2,
        'chunk_index': 1,
        'segment_number': 15,
        'document_id': 'doc-2',
        'page': -1,
        'bbox': [],
        'dataset_id': 'kb-1',
        'file_name': 'Doc.md',
        'segement_id': 'seg-2',
        'content': 'Existing source body',
        'group_name': 'block',
    }

    output = agentic._format_non_stream_result(
        {'text': '答案 [2](#source-2.1 "Doc.md")'},
        {'_citation_sources': {'2.1': source}},
    )

    assert output == {
        'text': '答案 [2](#source-2.1 "Doc.md")',
        'think': '',
        'sources': [source],
    }


def test_tool_stream_frame_serializes_tool_call_into_text_tags():
    frame = agentic._format_tool_stream_frame({
        'round': 3,
        'content': (
            '<think>参考文件不存在。让我检查一下skill目录中实际有哪些文件。\n'
            '</think>\n\n让我查看技能目录中有哪些可用的参考文件：\n'
        ),
        'tool_calls': [{
            'id': 'toolcall-3-1',
            'name': 'run_script',
            'arguments': {
                'name': 'railway-foundation-bearing-capacity-review',
                'rel_path': 'scripts/list_files.sh',
            },
        }],
    })

    assert frame == {
        'think': None,
        'text': (
            '<tp id="toolcall-3-1">Running the selected skill helper script at **scripts/list_files.sh** now.\n</tp>'
            '<tool_call>{"id":"toolcall-3-1","name":"run_script","arguments":{"name":"railway-foundation-bearing-capacity-review","rel_path":"scripts/list_files.sh"}}</tool_call>'
        ),
        'sources': [],
    }


def test_tool_stream_frame_uses_representative_kb_arguments():
    frame = agentic._format_tool_stream_frame({
        'round': 1,
        'content': '',
        'tool_calls': [
            {
                'id': 'toolcall-1-1',
                'name': 'kb_search',
                'arguments': {
                    'query': '全风化 软岩 风化岩分组 地基承载力 σ0 表',
                    'topk': 15,
                },
            },
            {
                'id': 'toolcall-1-2',
                'name': 'kb_get_window_nodes',
                'arguments': {
                    'docid': 'doc_7e052315556b40323f5007c5b9f549ab',
                    'number': '36',
                    'group': 'block',
                },
            },
        ],
    })

    assert frame == {
        'think': None,
        'text': (
            '<tp id="toolcall-1-1">Checking **全风化 软岩 风化岩分组 地基承载力 σ0 表** in the knowledge base for relevant material.\n</tp>'
            '<tool_call>{"id":"toolcall-1-1","name":"kb_search","arguments":{"query":"全风化 软岩 风化岩分组 地基承载力 σ0 表","topk":15}}</tool_call>'
            '<tp id="toolcall-1-2">Expanding nearby related segments around **36** for review.\n</tp>'
            '<tool_call>{"id":"toolcall-1-2","name":"kb_get_window_nodes","arguments":{"docid":"doc_7e052315556b40323f5007c5b9f549ab","number":"36","group":"block"}}</tool_call>'
        ),
        'sources': [],
    }


def test_tool_stream_frame_uses_vocab_mapping_preview_value():
    frame = agentic._format_tool_stream_frame({
        'round': 1,
        'content': '',
        'preview_text': 'remember vocabulary',
        'tool_calls': [{
            'id': 'toolcall-vocab-1',
            'name': 'vocab_manage',
            'arguments': {
                'suggestions': [{
                    'word': 'HCA',
                    'synonym': 'hardened cement aggregate',
                    'reason': 'The user explicitly defined the acronym.',
                }],
            },
        }],
    })

    assert frame['think'] is None
    assert frame['sources'] == []
    preview = frame['text'].split('</tp>', 1)[0]
    assert 'Updating vocabulary entries for **HCA <-> hardened cement aggregate** now.' in preview
    assert 'The user explicitly defined the acronym.' not in preview
    assert '<tool_call>{"id":"toolcall-vocab-1","name":"vocab_manage"' in frame['text']


def test_tool_stream_zh_and_en_preview_template_keys_match():
    assert set(tool_stream._TOOL_CALL_PREVIEW_TEMPLATES) == set(tool_stream._ZH_TOOL_CALL_PREVIEW_TEMPLATES)
    assert set(tool_stream._TOOL_RESULT_PREVIEW_TEMPLATES) == set(tool_stream._ZH_TOOL_RESULT_PREVIEW_TEMPLATES)
    assert set(tool_stream._TOOL_RESULT_FAILURE_TEMPLATES) == set(tool_stream._ZH_TOOL_RESULT_FAILURE_TEMPLATES)
    assert set(tool_stream._TOOL_RESULT_APPROVAL_TEMPLATES) == set(tool_stream._ZH_TOOL_RESULT_APPROVAL_TEMPLATES)


def test_tool_stream_frame_handles_flat_vocab_arguments_preview_value():
    frame = agentic._format_tool_stream_frame({
        'round': 1,
        'content': '',
        'preview_text': '更新词汇表',
        'tool_calls': [{
            'id': 'toolcall-vocab-flat-1',
            'name': 'vocab_manage',
            'arguments': {
                'suggestions': 'AI',
                'synonym': 'artificial intelligence',
                'reason': 'Standard synonym mapping for clarity',
            },
        }],
    })

    assert frame['think'] is None
    assert frame['sources'] == []
    preview = frame['text'].split('</tp>', 1)[0]
    assert '正在更新与 **AI <-> artificial intelligence** 相关的词汇表。' in preview
    assert 'AI <-> artificial intelligence' in preview
    assert '{"suggestions"' not in preview
    assert 'Standard synonym mapping for clarity' not in preview
    assert '<tool_call>{"id":"toolcall-vocab-flat-1","name":"vocab_manage"' in frame['text']
    assert '"arguments":{"suggestions":"AI","synonym":"artificial intelligence","reason":"Standard synonym mapping for clarity"}' in frame['text']


def test_tool_call_normalization_coerces_vocab_arguments_only_for_execution():
    tool_call = {
        'id': 'toolcall-vocab-flat-1',
        'name': 'vocab_manage',
        'arguments': {
            'suggestions': 'AI',
            'synonym': 'artificial intelligence',
            'reason': 'Standard synonym mapping for clarity',
        },
    }

    display_call = tool_stream._normalize_tool_call(tool_call, coerce_arguments=False)
    execution_call = tool_stream._normalize_tool_call(tool_call, coerce_arguments=True)

    assert display_call['arguments'] == {
        'suggestions': 'AI',
        'synonym': 'artificial intelligence',
        'reason': 'Standard synonym mapping for clarity',
    }
    assert execution_call['arguments'] == {
        'suggestions': [{
            'word': 'AI',
            'synonym': 'artificial intelligence',
            'reason': 'Standard synonym mapping for clarity',
        }],
    }


def test_tool_stream_frame_repairs_stringified_vocab_arguments_preview_value():
    frame = agentic._format_tool_stream_frame({
        'round': 1,
        'content': '',
        'preview_text': '更新词汇表',
        'tool_calls': [{
            'id': 'toolcall-vocab-string-1',
            'name': 'vocab_manage',
            'arguments': (
                '{"suggestions": "AI", "synonym": "artificial intelligence", '
                '"reason": "Standard synonym mapping"]}'
            ),
        }],
    })

    preview = frame['text'].split('</tp>', 1)[0]
    assert 'AI <-> artificial intelligence' in preview
    assert '{"suggestions"' not in preview
    assert 'Standard synonym mapping' not in preview
    assert '"arguments":{"suggestions":"AI","synonym":"artificial intelligence","reason":"Standard synonym mapping"}' in frame['text']


def test_tool_stream_frame_treats_string_parameter_errors_as_failed():
    frame = agentic._format_tool_stream_frame({
        'round': 1,
        'content': '',
        'preview_text': '',
        'tool_results': [{
            'id': 'toolcall-vocab-error-1',
            'tool_name': 'vocab_manage',
            'result': 'Tool [vocab_manage] parameters error.',
        }],
    })

    preview = frame['text'].split('</trp>', 1)[0]
    assert 'could not be updated' in preview
    assert 'updated successfully' not in preview


def test_tool_stream_frame_uses_memory_suggestion_title_preview_value():
    frame = agentic._format_tool_stream_frame({
        'round': 1,
        'content': '',
        'preview_text': 'remember preference',
        'tool_calls': [{
            'id': 'toolcall-memory-1',
            'name': 'memory',
            'arguments': {
                'target': 'user',
                'suggestions': [{
                    'title': 'Language preference',
                    'content': 'The user prefers Chinese responses.',
                    'reason': 'The user explicitly asked to use Chinese.',
                }],
            },
        }],
    })

    assert frame['think'] is None
    assert frame['sources'] == []
    preview = frame['text'].split('</tp>', 1)[0]
    assert 'Saving **Language preference** as useful long term memory now.' in preview
    assert 'The user prefers Chinese responses.' not in preview
    assert 'The user explicitly asked to use Chinese.' not in preview
    assert '<tool_call>{"id":"toolcall-memory-1","name":"memory"' in frame['text']


def test_tool_stream_frame_repairs_stringified_memory_arguments_preview_value():
    frame = agentic._format_tool_stream_frame({
        'round': 1,
        'content': '',
        'preview_text': '保存记忆',
        'tool_calls': [{
            'id': 'toolcall-memory-string-1',
            'name': 'memory',
            'arguments': (
                '{"target": "memory", "suggestions": [{"title": "System status", '
                '"content": "All tools are functional and ready for use.", '
                '"reason": "Initial system validation"]}'
            ),
        }],
    })

    preview = frame['text'].split('</tp>', 1)[0]
    assert 'System status' in preview
    assert 'All tools are functional and ready for use.' not in preview
    assert 'Initial system validation' not in preview
    assert '"arguments":{"target":"memory","suggestions":[{"title":"System status","content":"All tools are functional and ready for use.","reason":"Initial system validation"}]}' in frame['text']


def test_tool_stream_frame_uses_skill_category_and_name_preview_value():
    frame = agentic._format_tool_stream_frame({
        'round': 1,
        'content': '',
        'preview_text': 'update skill',
        'tool_calls': [{
            'id': 'toolcall-skill-1',
            'name': 'skill_manage',
            'arguments': {
                'action': 'modify',
                'category': 'writing',
                'name': 'report-review',
                'suggestions': [{
                    'title': 'Tighten verification',
                    'content': 'Add a final evidence check before summarizing.',
                }],
            },
        }],
    })

    assert frame['think'] is None
    assert frame['sources'] == []
    preview = frame['text'].split('</tp>', 1)[0]
    assert 'Updating reusable skill notes related to **writing/report-review** now.' in preview
    assert 'Tighten verification' not in preview
    assert '<tool_call>{"id":"toolcall-skill-1","name":"skill_manage"' in frame['text']


def test_tool_stream_frame_serializes_full_tool_result_into_text_tags():
    frame = agentic._format_tool_stream_frame({
        'round': 2,
        'content': '',
        'tool_results': [{
            'id': 'toolcall-2-1',
            'tool_name': 'memory',
            'result': {
                'status': 'success',
                'message': 'memory saved',
                'path': '/tmp/memory.json',
            },
        }],
    })

    assert frame == {
        'think': None,
        'text': (
            '<trp id="toolcall-2-1">Long term memory for **memory saved** was saved successfully.\n</trp>'
            '<tool_result>{"id":"toolcall-2-1","name":"memory","result":{"status":"success","message":"memory saved","path":"/tmp/memory.json"}}</tool_result>'
        ),
        'sources': [],
    }


def test_builtin_file_tool_uses_natural_preview_templates():
    frame = agentic._format_tool_stream_frame({
        'round': 4,
        'content': '',
        'tool_calls': [{
            'id': 'toolcall-4-1',
            'name': 'read_file',
            'arguments': {
                'path': '/tmp/demo.txt',
                'start_line': 1,
                'end_line': 20,
            },
        }],
        'tool_results': [{
            'id': 'toolcall-4-1',
            'tool_name': 'read_file',
            'result': {
                'status': 'ok',
                'path': '/tmp/demo.txt',
                'content': 'hello world',
            },
        }],
    })

    assert frame == {
        'think': None,
        'text': (
            '<tp id="toolcall-4-1">Reading file content from **/tmp/demo.txt** for review now.\n</tp>'
            '<tool_call>{"id":"toolcall-4-1","name":"read_file","arguments":{"path":"/tmp/demo.txt","start_line":1,"end_line":20}}</tool_call>'
            '<trp id="toolcall-4-1">File content from **/tmp/demo.txt** was loaded successfully now.\n</trp>'
            '<tool_result>{"id":"toolcall-4-1","name":"read_file","result":{"status":"ok","path":"/tmp/demo.txt","content":"hello world"}}</tool_result>'
        ),
        'sources': [],
    }


def test_merge_builtin_file_tools_skips_duplicates():
    merged = _merge_builtin_file_tools([
        'memory',
        'builtin_tools.read_file',
        'read_file',
        'list_dir',
    ])

    assert merged == [
        'memory',
        'builtin_tools.read_file',
        'list_dir',
        'search_in_files',
        'make_dir',
        'write_file',
        'delete_file',
        'move_file',
    ]


def test_tool_result_preview_is_truncated_to_fifty_chars():
    long_result = 'a' * 60

    frame = agentic._format_tool_stream_frame({
        'round': 5,
        'content': '',
        'tool_results': [{
            'id': 'toolcall-5-1',
            'tool_name': 'read_file',
            'result': {
                'status': 'ok',
                'path': '/tmp/long.txt',
                'content': long_result,
            },
        }],
    })

    assert frame == {
        'think': None,
        'text': (
            '<trp id="toolcall-5-1">File content from **aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa...** was loaded successfully now.\n</trp>'
            '<tool_result>{"id":"toolcall-5-1","name":"read_file","result":{"status":"ok","path":"/tmp/long.txt","content":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}</tool_result>'
        ),
        'sources': [],
    }


def test_tool_result_failure_uses_failure_preview_template():
    frame = agentic._format_tool_stream_frame({
        'round': 6,
        'content': '',
        'tool_results': [{
            'id': 'toolcall-6-1',
            'tool_name': 'read_file',
            'result': {
                'status': 'missing',
                'path': '/tmp/missing.txt',
            },
        }],
    })

    assert frame == {
        'think': None,
        'text': (
            '<trp id="toolcall-6-1">File content from **/tmp/missing.txt** could not be read.\n</trp>'
            '<tool_result>{"id":"toolcall-6-1","name":"read_file","result":{"status":"missing","path":"/tmp/missing.txt"}}</tool_result>'
        ),
        'sources': [],
    }


def test_tool_result_needs_approval_uses_approval_preview_template():
    frame = agentic._format_tool_stream_frame({
        'round': 7,
        'content': '',
        'tool_results': [{
            'id': 'toolcall-7-1',
            'tool_name': 'delete_file',
            'result': {
                'status': 'needs_approval',
                'reason': 'Deleting files requires approval.',
                'path': '/tmp/demo.txt',
            },
        }],
    })

    assert frame == {
        'think': None,
        'text': (
            '<trp id="toolcall-7-1">Please review the confirmation note "**Deleting files requires approval.**" before deleting this file.\n</trp>'
            '<tool_result>{"id":"toolcall-7-1","name":"delete_file","result":{"status":"needs_approval","reason":"Deleting files requires approval.","path":"/tmp/demo.txt"}}</tool_result>'
        ),
        'sources': [],
    }


def test_unknown_tool_fallback_preview_omits_value():
    frame = agentic._format_tool_stream_frame({
        'round': 8,
        'content': '',
        'tool_calls': [{
            'id': 'toolcall-8-1',
            'name': 'unknown_tool',
            'arguments': {'path': '/tmp/demo.txt'},
        }],
        'tool_results': [
            {
                'id': 'toolcall-8-1',
                'tool_name': 'unknown_tool',
                'result': {
                    'status': 'ok',
                    'content': 'done',
                },
            },
            {
                'id': 'toolcall-8-2',
                'tool_name': 'unknown_tool',
                'result': {
                    'status': 'failed',
                    'reason': 'boom',
                },
            },
        ],
    })

    assert frame == {
        'think': None,
        'text': (
            '<tp id="toolcall-8-1">Preparing the requested tool action for **/tmp/demo.txt**.\n</tp>'
            '<tool_call>{"id":"toolcall-8-1","name":"unknown_tool","arguments":{"path":"/tmp/demo.txt"}}</tool_call>'
            '<trp id="toolcall-8-1">Tool results for **/tmp/demo.txt** were received successfully.\n</trp>'
            '<tool_result>{"id":"toolcall-8-1","name":"unknown_tool","result":{"status":"ok","content":"done"}}</tool_result>'
            '<trp id="toolcall-8-2">The step for **boom** could not be completed.\n</trp>'
            '<tool_result>{"id":"toolcall-8-2","name":"unknown_tool","result":{"status":"failed","reason":"boom"}}</tool_result>'
        ),
        'sources': [],
    }


def test_normalize_history_keeps_plain_chat_messages_unchanged():
    history = [
        {'role': 'user', 'content': 'hello'},
        {'role': 'assistant', 'content': 'world'},
    ]

    result = agentic._normalize_history_for_agent(history)
    assert result[0] == {'role': 'user', 'content': 'hello'}
    assert result[1]['role'] == 'assistant'
    assert result[1]['content'] == 'world'
    assert result[1].get('reasoning_content', '') == ''


def test_normalize_history_rebuilds_tool_messages_from_assistant_content():
    history = [{
        'role': 'assistant',
        'content': (
            '先看文件。'
            '<tp id="toolcall-1-1">正在查看文件内容</tp>'
            '<tool_call>{"id":"toolcall-1-1","name":"read_file","arguments":{"path":"/tmp/demo.txt"}}</tool_call>'
            '<trp id="toolcall-1-1">已读取文件内容</trp>'
            '<tool_result>{"id":"toolcall-1-1","name":"read_file","result":{"status":"ok","content":"hello world"}}</tool_result>'
        ),
    }]

    assert agentic._normalize_history_for_agent(history) == [
        {
            'role': 'assistant',
            'content': '先看文件。',
            'reasoning_content': '',
            'tool_calls': [{
                'id': 'toolcall-1-1',
                'type': 'function',
                'function': {
                    'name': 'read_file',
                    'arguments': '{"path": "/tmp/demo.txt"}',
                },
            }],
        },
        {
            'role': 'tool',
            'tool_call_id': 'toolcall-1-1',
            'name': 'read_file',
            'content': '{"status":"ok","content":"hello world"}',
        },
    ]


def test_normalize_history_restores_citations_from_tool_result():
    config = {}
    agentic._reset_citation_state(config)
    history = [{
        'role': 'assistant',
        'content': (
            '这里是总结。'
            '<tool_call>{"id":"toolcall-1-1","name":"kb_search","arguments":{"query":"HCA"}}</tool_call>'
            '<tool_result>{"id":"toolcall-1-1","name":"kb_search","result":{"status":"success","items":['
            '{"citation_index":"1.1","ref":"[[1.1]]","file_name":"DeepSeek_V4.pdf","docid":"doc-1","uid":"seg-1","text":"HCA body","group":"block","number":21,"kb_id":"kb-1"}'
            ']}}</tool_result>'
        ),
    }]

    normalized = agentic._normalize_history_for_agent(history, config)

    assert normalized == [
        {
            'role': 'assistant',
            'content': '这里是总结。',
            'reasoning_content': '',
            'tool_calls': [{
                'id': 'toolcall-1-1',
                'type': 'function',
                'function': {
                    'name': 'kb_search',
                    'arguments': '{"query": "HCA"}',
                },
            }],
        },
        {
            'role': 'tool',
            'tool_call_id': 'toolcall-1-1',
            'name': 'kb_search',
            'content': '{"status":"success","items":[{"citation_index":"1.1","ref":"[[1.1]]","file_name":"DeepSeek_V4.pdf","docid":"doc-1","uid":"seg-1","text":"HCA body","group":"block","number":21,"kb_id":"kb-1"}]}',
        },
    ]
    assert config['_citation_sources'] == {
        '1.1': {
            'file_id': '',
            'file_name': 'DeepSeek_V4.pdf',
            'document_id': 'doc-1',
            'segement_id': 'seg-1',
            'dataset_id': 'kb-1',
            'index': '1.1',
            'display_index': 1,
            'document_index': 1,
            'chunk_index': 1,
            'content': 'HCA body',
            'group_name': 'block',
            'segment_number': 21,
            'page': -1,
            'bbox': [],
        },
    }
    assert config['_citation_key_map'] == {'uid:seg-1': '1.1'}
    assert config['_citation_next_doc_index'] == 2
    assert config['_citation_next_chunk_index_map'] == {'doc:kb-1:doc-1': 2}


def test_normalize_history_skips_citation_restore_for_non_kb_tool_result():
    config = {}
    agentic._reset_citation_state(config)
    history = [{
        'role': 'assistant',
        'content': (
            '我先查公网。'
            '<tool_call>{"id":"toolcall-1-1","name":"web_search","arguments":{"query":"evo"}}</tool_call>'
            '<tool_result>{"id":"toolcall-1-1","name":"web_search","result":{"success":true,"status":"ok","query":"evo",'
            '"requested_source":"auto","resolved_source":"wikipedia","tried_sources":["bocha","google","bing","wikipedia"],'
            '"lang":"zh","total":0,"items":[]}}</tool_result>'
        ),
    }]

    normalized = agentic._normalize_history_for_agent(history, config)

    assert normalized == [
        {
            'role': 'assistant',
            'content': '我先查公网。',
            'reasoning_content': '',
            'tool_calls': [{
                'id': 'toolcall-1-1',
                'type': 'function',
                'function': {
                    'name': 'web_search',
                    'arguments': '{"query": "evo"}',
                },
            }],
        },
        {
            'role': 'tool',
            'tool_call_id': 'toolcall-1-1',
            'name': 'web_search',
            'content': '{"success":true,"status":"ok","query":"evo","requested_source":"auto","resolved_source":"wikipedia","tried_sources":["bocha","google","bing","wikipedia"],"lang":"zh","total":0,"items":[]}',
        },
    ]
    assert config['_citation_sources'] == {}
    assert config['_citation_key_map'] == {}
    assert config['_citation_next_index'] == 1


def test_normalize_history_keeps_reasoning_aligned_with_assistant_segments():
    history = [{
        'role': 'assistant',
        'content': (
            '<think>先规划检索。</think>'
            '我先查资料。'
            '<tool_call>{"id":"call-1","name":"kb_search","arguments":{"query":"HCA"}}</tool_call>'
            '<tool_result>{"id":"call-1","name":"kb_search","result":{"status":"ok"}}</tool_result>'
            '<think>现在信息够了，开始写结论。</think>'
            '现在基于结果整理报告。'
        ),
    }]

    assert agentic._normalize_history_for_agent(history) == [
        {
            'role': 'assistant',
            'content': '我先查资料。',
            'reasoning_content': '先规划检索。',
            'tool_calls': [{
                'id': 'call-1',
                'type': 'function',
                'function': {
                    'name': 'kb_search',
                    'arguments': '{"query": "HCA"}',
                },
            }],
        },
        {
            'role': 'tool',
            'tool_call_id': 'call-1',
            'name': 'kb_search',
            'content': '{"status":"ok"}',
        },
        {
            'role': 'assistant',
            'content': '现在基于结果整理报告。',
            'reasoning_content': '现在信息够了，开始写结论。',
        },
    ]


def test_stream_uses_citations_restored_from_history(fake_pipeline, monkeypatch):
    class _FakeQueue:
        _default_values = [['延续上一轮知识。[['], ['1.1]]']]
        _named_values = {'think': []}

        def __init__(self, name=None):
            self.name = name

        def clear(self):
            return None

        def dequeue(self):
            if self.name:
                values = type(self)._named_values.get(self.name, [])
                return values.pop(0) if values else []
            values = type(self)._default_values
            return values.pop(0) if values else []

        @classmethod
        def get_instance(cls, name):
            return cls(name)

    history = [{
        'role': 'assistant',
        'content': (
            '上一轮总结。'
            '<tool_call>{"id":"toolcall-1-1","name":"kb_search","arguments":{"query":"HCA"}}</tool_call>'
            '<tool_result>{"id":"toolcall-1-1","name":"kb_search","result":{"status":"success","items":['
            '{"citation_index":"1.1","ref":"[[1.1]]","file_name":"DeepSeek_V4.pdf","docid":"doc-1","uid":"seg-1","text":"HCA body","group":"block","number":21,"kb_id":"kb-1"}'
            ']}}</tool_result>'
        ),
    }]

    def _fake_agentic_forward(*, query, history, stream_event_callback=None):
        return {'text': '延续上一轮知识。[1](#source-1.1 "DeepSeek_V4.pdf")', 'sources': []}

    monkeypatch.setattr(agentic.lazyllm, 'FileSystemQueue', _FakeQueue)
    monkeypatch.setattr(agentic, 'agentic_forward', _fake_agentic_forward)

    lazyllm.globals._init_sid(sid='stream-history-citation-session')
    lazyllm.locals._init_sid(sid='stream-history-citation-session')

    async def _collect():
        stream = agentic.agentic_rag({'query': 'follow-up', 'history': history}, stream=True)
        return [item async for item in stream]

    frames = asyncio.run(_collect())

    assert frames == [
        {'think': None, 'text': '延续上一轮知识。', 'sources': []},
        {
            'think': None,
            'text': '',
            'sources': [{
                'file_id': '',
                'file_name': 'DeepSeek_V4.pdf',
                'document_id': 'doc-1',
                'segement_id': 'seg-1',
                'dataset_id': 'kb-1',
                'index': '1.1',
                'display_index': 1,
                'document_index': 1,
                'chunk_index': 1,
                'content': 'HCA body',
                'group_name': 'block',
                'segment_number': 21,
                'page': -1,
                'bbox': [],
            }],
        },
    ]

def test_count_tool_turns_only_counts_assistant_messages_with_tool_calls():
    history = agentic._normalize_history_for_agent([
        {'role': 'assistant', 'content': 'plain text'},
        {
            'role': 'assistant',
            'content': (
                '<tool_call>{"id":"call-1","name":"kb_search","arguments":{"query":"foo"}}</tool_call>'
                '<tool_result>{"id":"call-1","name":"kb_search","result":{"total":1}}</tool_result>'
            ),
        },
        {
            'role': 'assistant',
            'content': (
                'done'
                '<tool_call>{"id":"call-2","name":"memory","arguments":{"target":"memory"}}</tool_call>'
                '<tool_result>{"id":"call-2","name":"memory","result":{"status":"ok"}}</tool_result>'
            ),
        },
    ])

    assert agentic._count_tool_turns(history) == 2


def test_spawn_background_review_uses_all_skills_under_skill_fs_url(monkeypatch):
    captured = {}

    class _ReviewAgent:
        def __init__(self, **kwargs):
            captured['skills'] = tuple(kwargs.get('skills') or ())
            captured['skills_dir'] = kwargs.get('skills_dir')

        def __call__(self, *_args, **_kwargs):
            return 'ok'

    monkeypatch.setattr(
        agentic_review,
        'list_all_skills_with_category',
        lambda _path: {
            'skill_a': '',
            'skill_b': 'ops',
            'skill_c': 'drafts',
        },
    )
    monkeypatch.setattr(lazyllm.tools.agent, 'ReactAgent', _ReviewAgent)
    monkeypatch.setenv('LAZYMIND_REVIEW_DEBUG', '1')
    algo_config.refresh('review_debug')

    agentic._spawn_background_review(
        config={
            'available_skills': ['skill_a'],
            'skill_fs_url': 'file:///tmp/skills',
        },
        llm=object(),
        keep_full_turns=3,
        history_snapshot=[],
        review_mode='skill',
        request_global_sid='sid-review',
    )

    assert captured == {
        'skills': ('skill_a', 'skill_b', 'skill_c'),
        'skills_dir': 'file:///tmp/skills',
    }
    monkeypatch.delenv('LAZYMIND_REVIEW_DEBUG')
    algo_config.refresh('review_debug')


def test_max_retries_and_force_summary_use_lazymind_env(fake_pipeline, monkeypatch):
    monkeypatch.setenv('LAZYMIND_MAX_RETRIES', '13')
    lazyllm.globals._init_sid(sid='max-retries-session')
    lazyllm.locals._init_sid(sid='max-retries-session')

    agentic.agentic_rag({
        'query': 'hello',
        'available_tools': [],
    })

    obs = fake_pipeline.observations[-1]
    assert obs['agent_kwargs_max_retries'] == 13
    assert obs['agent_kwargs_force_summarize'] is True
    assert obs['agent_kwargs_force_summarize_context'] == 'hello'
