from __future__ import annotations

import asyncio
import json
from types import SimpleNamespace

import pytest

from lazymind.chat.service import chat_service
from lazymind.chat.service.chat_request import ChatRequest
from lazymind.common.memory import EpisodeReadError


class _ContextAgent:
    def describe_context(self, history, query):
        return {}


def _export_prompt(
    monkeypatch,
    *,
    query: str,
    history: list[dict],
    use_memory: bool = True,
    observed_configs: list[dict] | None = None,
    observed_tool_types: list[list[str]] | None = None,
    usage_preview: bool = False,
    current_turn_seq: int | None = None,
    plugin_context: dict | None = None,
    user_id: str = 'episode-prompt-user',
):
    monkeypatch.setattr(chat_service, 'AutoModel', lambda *_args, **_kwargs: object())
    monkeypatch.setattr(
        chat_service,
        'load_memory_context',
        lambda: SimpleNamespace(
            soul='identity:\n  name: LazyMind',
            profile='identity:\n  preferred_name: null',
            preference='preferences: []',
        ),
    )

    def create_agent(self, llm, plan):
        if observed_configs is not None:
            observed_configs.append(dict(chat_service.lazyllm.globals['agentic_config']))
        if observed_tool_types is not None:
            observed_tool_types.append([type(tool).__name__ for tool in plan.tools])
        return _ContextAgent()

    monkeypatch.setattr(
        chat_service.AgentExecutor,
        'create_agent',
        create_agent,
    )

    return asyncio.run(chat_service.handle_chat(ChatRequest(
        message={
            'query': query,
            'history': history,
            'current_turn_seq': current_turn_seq,
        },
        conversation={
            'session_id': 'episode-prompt-session',
            'conversation_id': 'episode-prompt-conversation',
            'user_id': user_id,
        },
        retrieval={'filters': {}},
        runtime={
            'llm_config': {},
            'context_prompt_export': not usage_preview,
            'context_usage_preview': usage_preview,
        },
        personalization={'use_memory': use_memory},
        agent={'disabled_tools': [], 'available_skills': [], 'enable_subagent': False},
        plugin={
            'enable_plugin': False,
            'plugin_context': plugin_context,
        },
    )))


def test_episode_retrieval_uses_only_the_current_user_query(monkeypatch) -> None:
    class EpisodeStore:
        queries = []

        def search(self, user_id, query):
            self.queries.append((user_id, query))
            return []

    store = EpisodeStore()
    monkeypatch.setattr(chat_service, 'get_episode_store', lambda: store)

    _export_prompt(
        monkeypatch,
        query='还记得火星苹果42吗',
        history=[
            {'role': 'user', 'content': '一段不相关的历史'},
            {'role': 'assistant', 'content': '另一段不相关的回答'},
        ],
    )

    assert store.queries == [('episode-prompt-user', '还记得火星苹果42吗')]


def test_first_turn_semantic_miss_injects_distinct_recent_progress_reference(
    monkeypatch,
) -> None:
    records = [
        SimpleNamespace(
            id=f'ep-progress-{index}',
            rendered=(
                f'- occurred_at: 2026-07-2{index}T12:00:00+00:00\n'
                '  type: progress\n'
                f'  summary: progress {index}'
            ),
        )
        for index in range(3, 0, -1)
    ]

    class EpisodeStore:
        calls = []

        def search(self, user_id, query):
            self.calls.append(('search', user_id, query))
            return []

        def list_recent(self, user_id, episode_type, limit):
            self.calls.append(('list_recent', user_id, episode_type, limit))
            return records

        @staticmethod
        def render(record):
            return record.rendered

    store = EpisodeStore()
    monkeypatch.setattr(chat_service, 'get_episode_store', lambda: store)

    result = _export_prompt(
        monkeypatch,
        query='我最近在做什么',
        history=[],
        current_turn_seq=1,
    )
    prompt = result['prompt_markdown']

    assert store.calls == [
        ('search', 'episode-prompt-user', '我最近在做什么'),
        ('list_recent', 'episode-prompt-user', chat_service.EpisodeType.PROGRESS, 3),
    ]
    assert '#### Recent Progress Memory' in prompt
    assert (
        '<recent_progress_memory trust="untrusted" purpose="recency_fallback">'
        in prompt
    )
    assert '<episode_memory trust="untrusted" purpose="reference_only">' not in prompt
    assert "do not establish the user's current status" in prompt
    assert all(record.rendered in prompt for record in records)


def test_episode_retrieval_fails_open_only_for_transient_core_errors(
    monkeypatch,
) -> None:
    error = EpisodeReadError('Core is temporarily unavailable')
    error.code = 'storage_unavailable'
    error.retryable = True

    def fail_search(_user_id, _query):
        raise error

    monkeypatch.setattr(
        chat_service,
        'get_episode_store',
        lambda: SimpleNamespace(search=fail_search),
    )

    result = _export_prompt(
        monkeypatch,
        query='继续之前的决定',
        history=[],
        current_turn_seq=1,
    )

    assert '<episode_memory' not in result['prompt_markdown']
    assert '<recent_progress_memory' not in result['prompt_markdown']


def test_episode_retrieval_propagates_non_retryable_contract_errors(
    monkeypatch,
) -> None:
    error = EpisodeReadError('Core rejected the Episode request')
    error.code = 'storage_read_failed'
    error.retryable = False

    def fail_search(_user_id, _query):
        raise error

    monkeypatch.setattr(
        chat_service,
        'get_episode_store',
        lambda: SimpleNamespace(search=fail_search),
    )

    with pytest.raises(EpisodeReadError, match='Core rejected'):
        _export_prompt(monkeypatch, query='继续之前的决定', history=[])


def test_episode_memory_budget_is_enforced_after_xml_escaping(monkeypatch) -> None:
    monkeypatch.setattr(chat_service, '_cfg', {
        'episode_context_max_chars': 30,
        'episode_inject_topk': 2,
    })
    oversized = SimpleNamespace(
        rendered='<>&' * 10,
        episode=SimpleNamespace(id='ep-oversized'),
    )
    injected = SimpleNamespace(
        rendered='safe reference',
        episode=SimpleNamespace(id='ep-injected'),
    )

    reference, selected = chat_service._select_episode_memory_reference([
        oversized,
        injected,
    ])

    body = reference.split('<episode_memory trust="untrusted" purpose="reference_only">\n', 1)[1]
    body = body.split('\n</episode_memory>', 1)[0]
    assert body == 'safe reference'
    assert len(body) <= 30
    assert [item.episode.id for item in selected] == ['ep-injected']


def test_disabling_memory_skips_episode_retrieval_and_injection(monkeypatch) -> None:
    class EpisodeStore:
        def search(self, user_id, query):
            raise AssertionError('Episode search must stay disabled')

    monkeypatch.setattr(chat_service, 'get_episode_store', lambda: EpisodeStore())

    observed_tool_types = []
    result = _export_prompt(
        monkeypatch,
        query='不使用记忆回答',
        history=[{'role': 'user', 'content': '历史中的 EPTEST-42'}],
        use_memory=False,
        observed_tool_types=observed_tool_types,
        current_turn_seq=1,
    )

    assert '<episode_memory' not in result['prompt_markdown']
    assert 'EPTEST-42' not in result['prompt_markdown'].split('## Current Input', maxsplit=1)[1]
    assert 'MemoryTools' not in observed_tool_types[0]


async def _episode_stream_response(
    monkeypatch,
    store,
    *,
    fail: bool = False,
    current_turn_seq: int | None = None,
):
    monkeypatch.setattr(chat_service, 'AutoModel', lambda *_args, **_kwargs: object())
    monkeypatch.setattr(
        chat_service,
        'load_memory_context',
        lambda: SimpleNamespace(
            soul='identity:\n  name: LazyMind',
            profile='identity:\n  preferred_name: null',
            preference='preferences: []',
        ),
    )
    monkeypatch.setattr(chat_service, 'get_episode_store', lambda: store)
    monkeypatch.setattr(
        chat_service.AgentExecutor,
        'create_agent',
        lambda _self, _llm, _plan: object(),
    )

    async def stream_agent(_self, _agent, _plan):
        if fail:
            raise RuntimeError('model stream failed')
        yield 'final', 'completed answer'

    monkeypatch.setattr(chat_service.AgentExecutor, 'stream_agent', stream_agent)

    return await chat_service._handle_chat_impl(ChatRequest(
        message={
            'query': '继续这个决定',
            'history': [],
            'current_turn_seq': current_turn_seq,
        },
        conversation={
            'session_id': 'episode-stream-session',
            'conversation_id': 'episode-stream-conversation',
            'user_id': 'episode-stream-user',
        },
        retrieval={'filters': {}},
        runtime={'llm_config': {}},
        personalization={'use_memory': True},
        agent={'disabled_tools': [], 'available_skills': [], 'enable_subagent': False},
        plugin={'enable_plugin': False},
    ))


class _StreamingEpisodeStore:
    def __init__(self, *, injected: bool = True):
        self.injected = injected
        self.hit_calls = []

    def search(self, user_id, query):
        if not self.injected:
            return []
        return [SimpleNamespace(
            rendered='- occurred_at: 2026-07-23T15:20:31+08:00\n'
                     '  type: decision\n'
                     '  summary: 第一阶段不做历史版本',
            episode=SimpleNamespace(id='ep-stream'),
        )]

    def increment_hits(self, user_id, episode_ids):
        self.hit_calls.append((user_id, list(episode_ids)))
        return {episode_id: True for episode_id in episode_ids}


def test_episode_hit_increments_only_after_successful_stream_completion(monkeypatch) -> None:
    store = _StreamingEpisodeStore()

    async def drive():
        response = await _episode_stream_response(monkeypatch, store)
        assert store.hit_calls == []
        return [chunk async for chunk in response.body_iterator]

    chunks = asyncio.run(drive())

    assert chunks
    assert store.hit_calls == [('episode-stream-user', ['ep-stream'])]


def test_episode_hit_does_not_increment_when_model_stream_fails(monkeypatch) -> None:
    store = _StreamingEpisodeStore()

    async def drive():
        response = await _episode_stream_response(monkeypatch, store, fail=True)
        return [chunk async for chunk in response.body_iterator]

    chunks = asyncio.run(drive())

    payloads = [json.loads(chunk) for chunk in chunks]
    terminal = payloads[-1]['data']['runtime_event']
    assert terminal['type'] == 'run_finished'
    data = dict(terminal['data'])
    metrics = data.pop('metrics')
    assert data == {
        'status': 'failed',
        'reason': 'runtime_failure',
        'partial_output': False,
        'code': 'runtime_failure',
    }
    assert metrics['schema_version'] == 1
    assert store.hit_calls == []
