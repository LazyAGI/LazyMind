import asyncio
import json

from fastapi.responses import StreamingResponse

from lazymind.chat.service.chat_request import ChatRequest
from lazymind.chat.service import chat_service


async def _collect_streaming_response(response):
    chunks = []
    async for chunk in response.body_iterator:
        if isinstance(chunk, bytes):
            chunk = chunk.decode('utf-8')
        chunks.append(chunk)
    return ''.join(chunks)


def test_handle_chat_constructs_react_agent_from_runtime_context(monkeypatch):
    agent_calls = []
    agent_queries = []

    class FakeAgent:
        def __init__(self, llm, tools, **kwargs):
            agent_calls.append({'llm': llm, 'tools': tools, 'kwargs': kwargs})
            self._tools_manager = object()

        def forward(self, query, llm_chat_history=None):
            agent_queries.append(query)
            chat_service.lazyllm.FileSystemQueue().enqueue(json.dumps({'tag': 'text', 'delta': f'answer:{query}'}))
            return {'text': f'final:{query}'}

        __call__ = forward

        def set_stop_tools(self, stop_tools):
            self.stop_tools = stop_tools

        def _prepare_tool_context(self, _query, _history):
            return None

    monkeypatch.setattr(chat_service, 'AutoModel', lambda model, config=False: f'{model}:{config}')
    monkeypatch.setattr(chat_service.lazyllm.tools.agent, 'ReactAgent', FakeAgent)

    async def drive():
        response = await chat_service.handle_chat(ChatRequest(
            message={'query': 'hello', 'history': []},
            conversation={'session_id': 'sid-1'},
            retrieval={'filters': {}},
            runtime={'llm_config': {}},
            personalization={'use_memory': True},
            agent={
                'disabled_tools': [
                    'kb',
                    'temp_kb',
                    'wikipedia',
                    'arxiv',
                    'sciverse',
                    'google',
                    'bing',
                    'bocha',
                    'url_fetch',
                    'multimodal',
                    'vocab_learn',
                    'skill_editor',
                    'feishu',
                ],
                'available_skills': ['skill-a'],
                'enable_subagent': False,
            },
            plugin={'enable_plugin': False},
        ))
        return await _collect_streaming_response(response)

    body = asyncio.run(drive())

    assert agent_calls
    assert agent_calls[0]['llm'].startswith('llm:')
    assert agent_calls[0]['tools']
    assert agent_calls[0]['kwargs']['skills'] is False
    assert agent_calls[0]['kwargs']['stream'] is True
    assert '## Attached Files' not in agent_calls[0]['kwargs']['prompt']
    assert agent_queries[0].endswith('### User Instruction\n\nhello')
    assert 'answer:### Runtime Context' in body
    assert 'hello' in body
    assert '"status": "FINISHED"' in body


def test_task_profile_review_emits_ephemeral_pseudo_stream(monkeypatch):
    request = ChatRequest(
        message={'query': '推荐一款适合我的相机', 'history': []},
        conversation={'session_id': 'sid-router'},
        retrieval={'filters': {}},
        runtime={'llm_config': {}, 'thinking_depth': 'medium'},
        personalization={'use_memory': True},
        agent={'enable_subagent': False},
        plugin={'enable_plugin': False},
    )
    original_history = list(request.message.history)

    def fake_resolve(inputs):
        return chat_service.resolve_task_profile(
            inputs['query'], enable_llm_fallback=False,
        )

    async def fake_impl(_request, *, task_profile_override=None):
        assert task_profile_override is not None

        async def body():
            yield 'final\n\n'

        return StreamingResponse(body(), media_type='text/event-stream')

    monkeypatch.setattr(chat_service, '_resolve_task_profile_with_model', fake_resolve)
    monkeypatch.setattr(chat_service, '_handle_chat_impl', fake_impl)

    async def drive():
        response = await chat_service.handle_chat(request)
        return await _collect_streaming_response(response)

    body = asyncio.run(drive())
    assert '正在' in body
    assert '分析' in body
    assert '用户意图' in body
    assert '，请稍后' in body
    assert body.endswith('final\n\n')
    assert request.message.history == original_history


def test_context_usage_preview_only_uses_model_when_explicitly_requested(monkeypatch):
    model_calls = []

    def fake_model_resolve(inputs):
        model_calls.append(inputs)
        return chat_service.resolve_task_profile(
            inputs['query'], enable_llm_fallback=False,
        )

    async def fake_impl(_request, *, task_profile_override=None):
        return task_profile_override

    monkeypatch.setattr(chat_service, '_resolve_task_profile_with_model', fake_model_resolve)
    monkeypatch.setattr(chat_service, '_handle_chat_impl', fake_impl)

    def request(allow_llm):
        return ChatRequest(
            message={'query': '推荐一款适合我的相机', 'history': []},
            conversation={'session_id': 'sid-preview'},
            runtime={
                'thinking_depth': 'high',
                'context_usage_preview': True,
                'context_preview_allow_llm_routing': allow_llm,
            },
        )

    rule_profile = asyncio.run(chat_service.handle_chat(request(False)))
    assert rule_profile.routing_review_required is True
    assert model_calls == []

    asyncio.run(chat_service.handle_chat(request(True)))
    assert len(model_calls) == 1
