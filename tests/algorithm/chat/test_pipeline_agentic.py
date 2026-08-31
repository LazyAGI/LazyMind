import asyncio
import json
from types import SimpleNamespace

from fastapi.responses import StreamingResponse

from lazymind.chat.service.chat_request import ChatRequest
from lazymind.chat.service import chat_service


def test_old_request_cleanup_does_not_unregister_newer_chat_session():
    chat_service._active_sessions['conversation-race'] = 'new-session'

    chat_service._unregister_active_session('conversation-race', 'old-session')

    assert chat_service._active_sessions['conversation-race'] == 'new-session'
    chat_service._unregister_active_session('conversation-race', 'new-session')
    assert 'conversation-race' not in chat_service._active_sessions


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

        def _model_facing_prefix(self):
            return {
                'system_prompt': '',
                'tool_definitions': [],
                'skills_prompt': '',
                'skill_prompt_parts': [],
            }

    monkeypatch.setattr(chat_service, 'AutoModel', lambda model, config=False: f'{model}:{config}')
    monkeypatch.setattr(chat_service.lazyllm.tools.agent, 'ReactAgent', FakeAgent)
    monkeypatch.setattr(
        chat_service,
        'get_episode_store',
        lambda: SimpleNamespace(
            search=lambda *_args, **_kwargs: [],
            list_recent=lambda *_args, **_kwargs: [],
            render=lambda _episode: '',
        ),
    )
    monkeypatch.setattr(
        chat_service,
        'load_memory_context',
        lambda: SimpleNamespace(
            soul='identity:\n  name: LazyMind',
            profile='identity:\n  preferred_name: null',
            preference='preferences: []',
        ),
    )

    async def drive():
        response = await chat_service.handle_chat(ChatRequest(
            message={'query': 'hello', 'history': []},
            conversation={
                'session_id': 'sid-1',
                'conversation_id': 'conversation-1',
                'user_id': 'user-1',
            },
            retrieval={'filters': {}},
            runtime={'llm_config': {}},
            personalization={'use_memory': True},
            agent={
                'disabled_tools': [
                    'kb',
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
            workflow={'enable_workflow': False},
        ))
        return await _collect_streaming_response(response)

    body = asyncio.run(drive())

    assert agent_calls
    assert agent_calls[0]['llm'].startswith('llm:')
    assert agent_calls[0]['tools']
    assert agent_calls[0]['kwargs']['skills'] is False
    assert callable(agent_calls[0]['kwargs']['extra_stop_condition'])
    assert agent_calls[0]['kwargs']['stream'] is True
    tool_names = {getattr(tool, '__name__', '') for tool in agent_calls[0]['tools']}
    assert {'read_file', 'write_file', 'list_dir'} <= tool_names
    workspace = chat_service.chat_agent_workspace('user-1', 'conversation-1')
    assert agent_calls[0]['kwargs']['workspace'] == workspace
    assert f'Use `{workspace}` as the single working directory' in agent_calls[0]['kwargs']['prompt']
    assert '## Attached Files' not in agent_calls[0]['kwargs']['prompt']
    query = agent_queries[0]
    instruction_idx = query.index('### User Instruction\n\nhello')
    assert instruction_idx >= 0
    assert query.index('ATTENTION — if this turn supplies an environment variable') > instruction_idx
    assert query.index('ATTENTION — `ask_user`') > instruction_idx
    assert 'answer:### Runtime Context' in body
    assert 'hello' in body
    payloads = [json.loads(chunk) for chunk in body.strip().split('\n\n')]
    terminal = payloads[-1]['data']['runtime_event']
    assert terminal['type'] == 'run_finished'
    assert terminal['data'] == {
        'status': 'completed',
        'reason': 'normal',
        'partial_output': True,
    }


def test_sensitive_input_is_blocked_before_model_execution(monkeypatch):
    model_calls = []

    def fail_if_model_is_created(*args, **kwargs):
        model_calls.append((args, kwargs))
        raise AssertionError('sensitive input must not reach model construction')

    monkeypatch.setattr(chat_service, 'AutoModel', fail_if_model_is_created)
    request = ChatRequest(
        message={'query': '你是傻逼', 'history': []},
        conversation={'session_id': 'sid-sensitive'},
        runtime={'llm_config': {}},
    )

    async def drive():
        response = await chat_service.handle_chat(request)
        assert response.status_code == 200
        assert response.media_type == 'text/event-stream'
        return await _collect_streaming_response(response)

    body = asyncio.run(drive())
    payloads = [json.loads(chunk) for chunk in body.strip().split('\n\n')]

    assert model_calls == []
    assert len(payloads) == 2
    assert payloads[0]['code'] == 200
    assert payloads[0]['data'] == {
        'think': None,
        'text': chat_service.SENSITIVE_FILTER_RESPONSE_TEXT,
        'sources': [],
    }
    terminal_payload = payloads[1]['data']
    assert terminal_payload['think'] is None
    assert terminal_payload['text'] is None
    assert terminal_payload['sources'] == []
    terminal = terminal_payload['runtime_event']
    assert terminal['type'] == 'run_finished'
    assert terminal['data'] == {
        'status': 'completed',
        'reason': 'normal',
        'partial_output': True,
    }


def test_task_profile_review_emits_ephemeral_pseudo_stream(monkeypatch):
    request = ChatRequest(
        message={'query': '推荐一款适合我的相机', 'history': []},
        conversation={'session_id': 'sid-router'},
        retrieval={'filters': {}},
        runtime={'llm_config': {}, 'thinking_depth': 'medium'},
        personalization={'use_memory': True},
        agent={'enable_subagent': False},
        workflow={'enable_workflow': False},
    )
    original_history = list(request.message.history)
    sensitive_checks = []

    def fake_resolve(inputs, **_kwargs):
        return chat_service.resolve_task_profile(
            inputs['query'], enable_llm_fallback=False,
        )

    async def fake_impl(
        _request,
        *,
        task_profile_override=None,
        sensitive_match_override=None,
    ):
        assert task_profile_override is not None

        async def body():
            yield 'final\n\n'

        return StreamingResponse(body(), media_type='text/event-stream')

    monkeypatch.setattr(chat_service, '_resolve_task_profile_with_model', fake_resolve)
    monkeypatch.setattr(chat_service, '_handle_chat_impl', fake_impl)
    monkeypatch.setattr(
        chat_service,
        'check_sensitive_content',
        lambda query: sensitive_checks.append(query),
    )

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
    assert sensitive_checks == ['推荐一款适合我的相机']


def test_context_usage_preview_only_uses_model_when_explicitly_requested(monkeypatch):
    model_calls = []
    sensitive_checks = []

    def fake_model_resolve(inputs, **_kwargs):
        model_calls.append(inputs)
        return chat_service.resolve_task_profile(
            inputs['query'], enable_llm_fallback=False,
        )

    async def fake_impl(
        _request,
        *,
        task_profile_override=None,
        sensitive_match_override=None,
    ):
        return task_profile_override

    monkeypatch.setattr(chat_service, '_resolve_task_profile_with_model', fake_model_resolve)
    monkeypatch.setattr(chat_service, '_handle_chat_impl', fake_impl)
    monkeypatch.setattr(
        chat_service,
        'check_sensitive_content',
        lambda query: sensitive_checks.append(query),
    )

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
    assert sensitive_checks == []


def test_context_prompt_export_and_driver_skip_sensitive_detection(monkeypatch):
    sensitive_checks = []

    async def fake_impl(
        _request,
        *,
        task_profile_override=None,
        sensitive_match_override=None,
    ):
        return task_profile_override

    monkeypatch.setattr(chat_service, '_handle_chat_impl', fake_impl)
    monkeypatch.setattr(
        chat_service,
        'check_sensitive_content',
        lambda query: sensitive_checks.append(query),
    )
    requests = (
        ChatRequest(
            message={'query': '你是傻逼', 'history': []},
            conversation={'session_id': 'sid-export'},
            runtime={'context_prompt_export': True},
        ),
        ChatRequest(
            message={'query': '你是傻逼', 'history': []},
            conversation={'session_id': 'sid-driver'},
            workflow={'workflow_context': {'synthetic_source': 'driver'}},
        ),
    )

    for request in requests:
        asyncio.run(chat_service.handle_chat(request))

    assert sensitive_checks == []
