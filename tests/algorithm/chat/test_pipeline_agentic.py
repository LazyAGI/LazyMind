import asyncio
import json

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

        def forward(self, query, llm_chat_history=None):
            agent_queries.append(query)
            chat_service.lazyllm.FileSystemQueue().enqueue(json.dumps({'tag': 'text', 'delta': f'answer:{query}'}))
            return {'text': f'final:{query}'}

        __call__ = forward

        def set_stop_tools(self, stop_tools):
            self.stop_tools = stop_tools

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
    assert agent_calls[0]['kwargs']['skills'] == ['skill-a']
    assert agent_calls[0]['kwargs']['stream'] is True
    assert '## Attached Files' not in agent_calls[0]['kwargs']['prompt']
    assert agent_queries[0] == '### User Instruction\n\nhello'
    assert 'answer:### User Instruction' in body
    assert 'hello' in body
    assert '"status": "FINISHED"' in body
