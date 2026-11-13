import asyncio
import json
from types import SimpleNamespace
from unittest.mock import MagicMock

import pytest

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


@pytest.mark.parametrize('user_query,context,expected', [
    ('Please create a presentation.', '本任务的工作区：未绑定',
     'Please provide the required information to continue.'),
    ('请帮我制作演示文稿。', 'Workspace: unbound', '请补充必要信息后继续。'),
])
def test_tool_preview_language_uses_original_user_query(monkeypatch, tmp_path, provider, user_query, context, expected):
    from lazymind.chat.engine.tools import temporary_retrieval_session as session
    from lazymind.chat.engine.subagent.db import TaskQueryDB
    from lazymind.chat.workflow import workflow_manager
    from lazymind.config import config

    monkeypatch.setattr(session, 'workspace_for_request', lambda **kwargs: str(tmp_path))
    monkeypatch.setattr(session, 'is_model_role_available', lambda role: False)
    monkeypatch.setattr(TaskQueryDB, '_get', lambda *a, **kw: {'tasks': []})
    monkeypatch.setattr(chat_service, 'AutoModel', lambda *a, **kw: chat_service.lazyllm.OnlineChatModule(
        source='openai', model='preview-language', url=provider.url, api_key='offline-key', stream=False,
    ))
    monkeypatch.setattr(chat_service, 'load_memory_context', lambda: SimpleNamespace(soul='', profile='', preference=''))
    client = MagicMock()
    runtime = {'clarification_fields': [{
        'id': 'topic', 'question': '主题是什么？', 'type': 'text', 'required': True,
        'reason': '主题决定内容范围。',
    }]}
    client.get_workflow.return_value.result = {'revision_id': 'r1', 'runtime': runtime}
    monkeypatch.setattr(workflow_manager, '_client', lambda: client)
    entry = {'workflow_ref': 'builtin:writer', 'workflow_id': 'writer', 'revision_id': 'r1', 'runtime': runtime}
    provider.script['preview-language'] = [{'message': {'role': 'assistant', 'content': '', 'tool_calls': [{
        'id': 'init-language', 'type': 'function',
        'function': {'name': 'trigger_writer_workflow', 'arguments': '{}'},
    }]}}]

    async def drive():
        response = await chat_service._handle_chat_impl(ChatRequest(
            message={'query': f'{context}\n\nUser query:\n{user_query}', 'user_query': user_query},
            conversation={'session_id': 'language-session', 'conversation_id': 'language-conversation',
                          'run_id': 'language-run', 'history_id': 'language-history'},
            runtime={'llm_config': {}}, personalization={'use_memory': False},
            agent={'enable_subagent': False, 'disabled_tools': [
                'kb', 'wikipedia', 'arxiv', 'sciverse', 'google', 'bing', 'bocha', 'url_fetch',
                'multimodal', 'vocab_learn', 'skill_editor', 'feishu',
            ]},
            workflow={'enable_workflow': True, 'catalog': [entry],
                      'activations': [{**entry, 'tool_name': 'trigger_writer_workflow'}]},
        ))
        return [json.loads(chunk)['data'] async for chunk in response.body_iterator]

    with config.temp('context_compression_enabled', False):
        frames = asyncio.run(drive())
    text = ''.join(frame.get('text') or '' for frame in frames)
    assert expected in text
    assert any(frame.get('ask_pending') for frame in frames)
    assert len(provider.requests) == 1
    # Language selection must not remove system context from the model's input.
    assert context in str(provider.requests[0]['messages'])


@pytest.mark.parametrize('guidance_timing', [None, 'before_card', 'after_card'])
def test_startup_card_preserves_accepted_steering_without_repeating_card(
    monkeypatch, tmp_path, provider, guidance_timing,
):
    from lazymind.chat.engine.agent_runtime.steering import steering_registry
    from lazymind.chat.engine.tools import temporary_retrieval_session as session
    from lazymind.chat.engine.subagent.db import TaskQueryDB
    from lazymind.chat.workflow import workflow_manager
    from lazymind.config import config

    monkeypatch.setattr(session, 'workspace_for_request', lambda **kwargs: str(tmp_path))
    monkeypatch.setattr(session, 'is_model_role_available', lambda role: False)
    monkeypatch.setattr(TaskQueryDB, '_get', lambda *a, **kw: {'tasks': []})
    monkeypatch.setattr(chat_service, 'AutoModel', lambda *a, **kw: chat_service.lazyllm.OnlineChatModule(
        source='openai', model='startup-guidance', url=provider.url, api_key='offline-key', stream=False,
    ))
    monkeypatch.setattr(chat_service, 'load_memory_context', lambda: SimpleNamespace(
        soul='', profile='', preference='',
    ))
    client = MagicMock()
    client.get_workflow.return_value.result = {'revision_id': 'r1', 'runtime': {'clarification_fields': [{
        'id': 'topic', 'question': '文章的主题是什么？', 'type': 'text', 'required': True,
        'reason': '主题决定正文内容。',
    }]}}
    monkeypatch.setattr(workflow_manager, '_client', lambda: client)
    entry = {'workflow_ref': 'builtin:writer', 'workflow_id': 'writer', 'revision_id': 'r1',
             'runtime': client.get_workflow.return_value.result['runtime']}
    provider.script['startup-guidance'] = [{'message': {'role': 'assistant', 'content': '收到补充要求'}}]
    guides = ['主题是垃圾分类', '正文面向小学生']

    def accept_guidance():
        for index, guide in enumerate(guides):
            assert steering_registry.accept('startup-run', 'startup-conversation', str(index), guide) == (True, '')

    async def drive():
        response = await chat_service._handle_chat_impl(ChatRequest(
            message={'query': '帮我写一篇文章', 'history': [
                {'role': 'user', 'content': '请用中文回答'},
                {'role': 'assistant', 'content': '好的'},
            ]},
            conversation={'session_id': 'startup-session', 'conversation_id': 'startup-conversation',
                          'run_id': 'startup-run', 'history_id': 'startup-history'},
            runtime={'llm_config': {}},
            personalization={'use_memory': False},
            agent={'enable_subagent': False, 'disabled_tools': [
                'kb', 'wikipedia', 'arxiv', 'sciverse', 'google', 'bing', 'bocha', 'url_fetch',
                'multimodal', 'vocab_learn', 'skill_editor', 'feishu',
            ]},
            workflow={'enable_workflow': True, 'catalog': [entry],
                      'activations': [{**entry, 'tool_name': 'trigger_writer_workflow'}], 'clarification': {
                'workflow_ref': 'builtin:writer', 'revision_id': 'r1', 'startup_request_id': 'launch',
                'original_request': '帮我写一篇文章', 'values': {},
            }},
        ))
        if guidance_timing == 'before_card':
            accept_guidance()
        frames = []
        async for chunk in response.body_iterator:
            payload = json.loads(chunk)['data']
            frames.append(payload)
            if payload.get('ask_pending') and guidance_timing == 'after_card':
                accept_guidance()
        return frames

    with config.temp('context_compression_enabled', False):
        frames = asyncio.run(drive())
    cards = [frame['ask_pending'] for frame in frames if frame.get('ask_pending')]
    assert len(cards) == 1
    assert cards[0]['questions'][0]['id'] == 'topic'
    assert frames[-1]['runtime_event']['data']['status'] == 'completed'
    client.prepare.assert_not_called()
    if guidance_timing is None:
        assert provider.requests == []
    else:
        assert len(provider.requests) == 1
        contents = '\n'.join(str(message.get('content', ''))
                             for message in provider.requests[0]['messages'])
        assert '帮我写一篇文章' in contents and '请用中文回答' in contents
        assert all(guide in contents for guide in guides)
        applied = [item for frame in frames for item in frame.get('steering_applied', [])]
        assert [item['text'] for item in applied] == guides
        assert '收到补充要求' in ''.join(frame.get('text') or '' for frame in frames)


@pytest.mark.parametrize('mcp_failure', [None, 'timeout', 'authorization', 'all_failed'])
@pytest.mark.parametrize('directories_enabled', [None, True, False])
def test_handle_chat_constructs_react_agent_from_runtime_context(
    monkeypatch, mcp_failure, directories_enabled, tmp_path,
):
    from lazymind.chat.engine.tools import temporary_retrieval_session as session
    monkeypatch.setattr(session, 'workspace_for_request', lambda **kwargs: str(tmp_path))
    monkeypatch.setattr(session, 'is_model_role_available', lambda role: False)
    agent_calls = []
    agent_queries = []
    agent_envs = []

    class FakeAgent:
        def __init__(self, llm, tools, **kwargs):
            agent_calls.append({'llm': llm, 'tools': tools, 'kwargs': kwargs})
            self._tools_manager = SimpleNamespace(tools_info={})
            self._skill_manager = None

        def forward(self, query, llm_chat_history=None):
            agent_queries.append(query)
            agent_envs.append(dict(chat_service.lazyllm.globals.get('dynamic_env_vars', {})))
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

    mcp_config = []
    if mcp_failure:
        def healthy_read():
            return 'healthy MCP'

        class MCPClient:
            def __init__(self, command_or_url, **kwargs):
                self.url = command_or_url

            def get_tools(self, **kwargs):
                if self.url.endswith('/healthy'):
                    return [healthy_read]
                if mcp_failure == 'authorization':
                    raise chat_service.MCPAuthorizationRequired()
                raise TimeoutError('token=private-secret')

        monkeypatch.setattr(chat_service, 'MCPClient', MCPClient)
        chat_service._mcp_tool_cache.clear()
        mcp_config = [{
            'name': 'Notion', 'url': 'https://mcp.example/failed', 'auth_type': 'oauth',
            'allowed_tools': ['read'], 'oauth': dict(
                user_id='user-1', server_id='notion', server_url='https://mcp.example/failed',
                grant_id='private-grant', grant_version=1),
        }]
        if mcp_failure != 'all_failed':
            mcp_config.append({'name': 'healthy', 'url': 'https://mcp.example/healthy'})

    async def drive():
        chat_service.lazyllm.globals._init_sid('sid-1')
        chat_service.lazyllm.globals['dynamic_env_vars'] = {'REMOVED_TOKEN': 'stale-secret'}
        response = await chat_service.handle_chat(ChatRequest(
            message={
                'query': 'Paper context: sex classification task.\n\nUser query:\nhello',
                'user_query': 'hello', 'history': [],
            },
            conversation={
                'session_id': 'sid-1',
                'conversation_id': 'conversation-1',
                'history_id': 'history-1', 'run_id': 'run-1',
                'user_id': 'user-1',
            },
            retrieval={'filters': {}},
            runtime={
                'llm_config': {}, 'mcp_config': mcp_config,
                'user_env_vars': {'REDFOX_API_KEY': 'runtime-only-secret'},
            },
            personalization={'use_memory': True},
            agent={
                **({'document_directories_enabled': directories_enabled} if directories_enabled is not None else {}),
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

    # An actual chat turn pins retrieval even without attachments. Configuring
    # embedding later must not change this conversation's ingestion mode.
    monkeypatch.setattr(session, 'is_model_role_available', lambda role: True)
    assert session.initialize_temporary_retrieval({
        'user_id': 'user-1', 'conversation_id': 'conversation-1',
    }) is False

    if mcp_failure:
        status = 'needs_authorization' if mcp_failure == 'authorization' else 'unavailable'
        assert status in agent_queries[0]
        assert 'Notion' in agent_queries[0]
        frames = [json.loads(chunk)['data'] for chunk in body.strip().split('\n\n')]
        assert not any(frame.get('think') for frame in frames)
        assert 'private-secret' not in body + agent_queries[0]
        assert 'private-grant' not in body + agent_queries[0]
        if mcp_failure != 'all_failed':
            assert healthy_read in agent_calls[0]['tools']

    assert agent_calls
    assert agent_calls[0]['llm'].startswith('llm:')
    assert agent_calls[0]['tools']
    # A trivial greeting may simplify answer strategy, but cannot hide host skills.
    assert agent_calls[0]['kwargs']['skills'] == ['skill-a']
    assert callable(agent_calls[0]['kwargs']['extra_stop_condition'])
    assert agent_calls[0]['kwargs']['stream'] is True
    tool_names = {getattr(tool, '__name__', '') for tool in agent_calls[0]['tools']}
    assert {'search_file_resource', 'read_file_resource', 'save_chat_artifact'} <= tool_names
    expect_directories = directories_enabled is True
    assert ('list_document_directories' in tool_names) is expect_directories
    assert ('background organization handles discoveries' in agent_queries[0]) is expect_directories
    workspace = chat_service.chat_agent_workspace('user-1', 'conversation-1')
    assert agent_calls[0]['kwargs']['workspace'] == workspace
    assert f'Use `{workspace}` as the single working directory' in agent_calls[0]['kwargs']['prompt']
    assert 'does not provide `write` or other host filesystem tools' in agent_calls[0]['kwargs']['prompt']
    assert '`workspace_path`' in agent_calls[0]['kwargs']['prompt']
    assert '## Attached Files' not in agent_calls[0]['kwargs']['prompt']
    query = agent_queries[0]
    assert 'Paper context: sex classification task.' in query
    assert 'Enabled user-level variables: ["REDFOX_API_KEY"]' in query
    assert 'runtime-only-secret' not in query
    assert 'REMOVED_TOKEN' not in query
    assert agent_envs == [{'REDFOX_API_KEY': 'runtime-only-secret'}]
    instruction_idx = query.index('### User Instruction\n\nhello')
    assert instruction_idx >= 0
    assert query.index('Call set_session_env only after the user explicitly asks') > instruction_idx
    # A ready greeting keeps normal tools and skills, but needs no clarification.
    assert 'ask_user' not in tool_names
    assert 'ATTENTION — `ask_user`' not in query
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
    assert payloads[-1]['data']['performance_metrics']['schema_version'] == 1


@pytest.mark.parametrize('message', [
    {'query': '你是傻逼', 'history': []},
    {'query': '服务端论文上下文', 'user_query': '你是傻逼', 'history': []},
])
def test_sensitive_input_is_blocked_before_model_execution(monkeypatch, message):
    model_calls = []

    def fail_if_model_is_created(*args, **kwargs):
        model_calls.append((args, kwargs))
        raise AssertionError('sensitive input must not reach model construction')

    monkeypatch.setattr(chat_service, 'AutoModel', fail_if_model_is_created)
    request = ChatRequest(
        message=message,
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
        'status': 'failed',
        'reason': 'runtime_failure',
        'code': 'sensitive_content_blocked',
        'partial_output': True,
        'model_invoked': False,
    }


@pytest.mark.parametrize('user_query,context,expected', [
    ('推荐一款适合我的相机', '', '正在分析用户意图，请稍后'),
    ('推荐一款适合我的相机', 'Workspace: unbound', '正在分析用户意图，请稍后'),
    ('Recommend a camera that suits me.', '本任务的工作区：未绑定', 'Analyzing your request, please wait…'),
])
def test_task_profile_review_emits_ephemeral_pseudo_stream(monkeypatch, user_query, context, expected):
    request = ChatRequest(
        message={'query': f'{context}\n\nUser query:\n{user_query}' if context else user_query,
                 **({'user_query': user_query} if context else {}), 'history': []},
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
    frames = [json.loads(chunk)['data'] for chunk in body.split('\n\n') if chunk and chunk != 'final']
    assert ''.join(frame.get('think') or '' for frame in frames) == expected
    assert body.endswith('final\n\n')
    assert request.message.history == original_history
    assert sensitive_checks == [user_query]


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
