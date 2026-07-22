from __future__ import annotations

import asyncio
import importlib.util
import json
import os
import sys
from pathlib import Path
from types import ModuleType, SimpleNamespace
from typing import Any

import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient

_ALGO = os.path.join(os.path.dirname(__file__), '..', '..', '..', 'algorithm')
_LAZYLLM_ROOT = os.path.join(_ALGO, 'lazyllm')
if _ALGO not in sys.path:
    sys.path.insert(0, _ALGO)
if _LAZYLLM_ROOT not in sys.path:
    sys.path.insert(0, _LAZYLLM_ROOT)


def _package(name: str) -> ModuleType:
    module = ModuleType(name)
    module.__path__ = []
    return module


class _SidDict(dict):
    def _init_sid(self, sid: str) -> None:
        self['_sid'] = sid


def _load_module(module_name: str, module_path: Path):
    spec = importlib.util.spec_from_file_location(module_name, module_path)
    assert spec is not None
    assert spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    sys.modules[module_name] = module
    spec.loader.exec_module(module)
    return module


def _load_review_modules():
    module_names = [
        'lazyllm',
        'lazyllm.tools',
        'lazyllm.tools.fs',
        'lazyllm.tools.fs.client',
        'lazymind',
        'lazymind.chat',
        'lazymind.chat.engine',
        'lazymind.chat.engine.tools',
        'lazymind.chat.engine.tools.memory',
        'lazymind.chat.service',
        'lazymind.chat.service.component',
        'lazymind.chat.service.component.history',
        'lazymind.config',
        'lazymind.model_config',
        'lazymind.review',
        'lazymind.review.api',
        'lazymind.review.api.memory_review_routes',
        'lazymind.review.memory_review',
        'lazymind.review.memory_review.prompts',
        'lazymind.review.service',
        'lazymind.review.service.memory_review',
    ]
    original_modules = {name: sys.modules.get(name) for name in module_names}

    fake_modules = {
        'lazymind': _package('lazymind'),
        'lazymind.review': _package('lazymind.review'),
        'lazymind.review.api': _package('lazymind.review.api'),
        'lazymind.review.memory_review': _package('lazymind.review.memory_review'),
        'lazymind.review.service': _package('lazymind.review.service'),
    }
    fake_lazyllm = ModuleType('lazyllm')
    fake_lazyllm.AutoModel = object
    fake_lazyllm.LOG = SimpleNamespace(
        error=lambda *_args, **_kwargs: None,
        exception=lambda *_args, **_kwargs: None,
        info=lambda *_args, **_kwargs: None,
    )
    fake_lazyllm.globals = _SidDict()
    fake_lazyllm.locals = _SidDict()
    fake_fs_client = ModuleType('lazyllm.tools.fs.client')
    fake_fs_client.FS = object
    fake_tools_pkg = ModuleType('lazymind.chat.engine.tools')

    class FakeMemoryTools:
        __public_apis__ = ['episode_create']

        def episode_create(self, *args, **kwargs):
            return None

    fake_history = ModuleType('lazymind.chat.service.component.history')
    fake_history.normalize_history_for_agent = lambda history: history
    fake_config = ModuleType('lazymind.config')
    fake_config.config = {'core_api_url': 'http://core', 'review_max_retries': 2}
    fake_model_config = ModuleType('lazymind.model_config')
    fake_model_config.inject_model_config = lambda _config: None
    fake_modules['lazyllm'] = fake_lazyllm
    fake_modules['lazyllm.tools'] = _package('lazyllm.tools')
    fake_modules['lazyllm.tools.fs'] = _package('lazyllm.tools.fs')
    fake_modules['lazyllm.tools.fs.client'] = fake_fs_client
    fake_modules['lazymind.chat'] = _package('lazymind.chat')
    fake_modules['lazymind.chat.engine'] = _package('lazymind.chat.engine')
    fake_modules['lazymind.chat.engine.tools'] = fake_tools_pkg
    fake_memory_module = ModuleType('lazymind.chat.engine.tools.memory')
    fake_memory_module.MemoryTools = FakeMemoryTools
    fake_modules['lazymind.chat.engine.tools.memory'] = fake_memory_module
    fake_modules['lazymind.chat.service'] = _package('lazymind.chat.service')
    fake_modules['lazymind.chat.service.component'] = _package('lazymind.chat.service.component')
    fake_modules['lazymind.chat.service.component.history'] = fake_history
    fake_modules['lazymind.config'] = fake_config
    fake_modules['lazymind.model_config'] = fake_model_config

    try:
        sys.modules.update(fake_modules)
        memory_prompts = _load_module(
            'lazymind.review.memory_review.prompts',
            Path(_ALGO) / 'lazymind/review/memory_review/prompts.py',
        )
        memory_review = _load_module(
            'lazymind.review.service.memory_review',
            Path(_ALGO) / 'lazymind/review/service/memory_review.py',
        )
        memory_review_routes = _load_module(
            'lazymind.review.api.memory_review_routes',
            Path(_ALGO) / 'lazymind/review/api/memory_review_routes.py',
        )
        return SimpleNamespace(
            memory_prompts=memory_prompts,
            memory_review=memory_review,
            memory_review_routes=memory_review_routes,
        )
    finally:
        for name, original in original_modules.items():
            if original is None:
                sys.modules.pop(name, None)
            else:
                sys.modules[name] = original


def _load_memory_review_module():
    return _load_review_modules().memory_review


def _load_memory_review_routes_module():
    return _load_review_modules().memory_review_routes


def _patch_runtime_bindings(
    monkeypatch,
    memory_review,
    *,
    lazyllm_module,
    auto_model,
    fs,
    memory_tools,
    config: dict[str, Any],
    inject_model_config=None,
    normalize_history_for_agent=None,
) -> None:
    if inject_model_config is None:
        def inject_model_config(_config):
            return None
    if normalize_history_for_agent is None:
        def normalize_history_for_agent(history):
            return history

    monkeypatch.setattr(memory_review, 'lazyllm', lazyllm_module)
    monkeypatch.setattr(memory_review, 'AutoModel', auto_model)
    monkeypatch.setattr(memory_review, 'FS', fs)
    monkeypatch.setattr(memory_review, 'MemoryTools', lambda: memory_tools)
    monkeypatch.setattr(memory_review, '_cfg', config)
    monkeypatch.setattr(memory_review, 'inject_model_config', inject_model_config)
    monkeypatch.setattr(
        memory_review,
        'normalize_history_for_agent',
        normalize_history_for_agent,
    )


def _run_review_with_tool_results(monkeypatch, tool_results, *, response='Review complete.'):
    memory_review = _load_memory_review_module()

    class FakeModel:
        def __init__(self, *args, **kwargs):
            pass

    class FakeReactAgent:
        def __init__(self, **kwargs):
            pass

        def __call__(self, prompt, llm_chat_history=None):
            config = fake_lazyllm.globals['agentic_config']
            config['memory_tool_results'].extend(tool_results)
            return response

    fake_lazyllm = SimpleNamespace(
        globals=_SidDict(),
        locals=_SidDict(),
        tools=SimpleNamespace(agent=SimpleNamespace(ReactAgent=FakeReactAgent)),
    )

    class FakeMemoryTools:

        def episode_create(self, *args, **kwargs):
            return None

    memory_tools = FakeMemoryTools()
    _patch_runtime_bindings(
        monkeypatch,
        memory_review,
        lazyllm_module=fake_lazyllm,
        auto_model=FakeModel,
        fs=object,
        memory_tools=memory_tools,
        config={'core_api_url': 'http://core', 'review_max_retries': 2},
    )
    return memory_review.review_memory(
        task_id='memory_review_core-task-results',
        user_id='user-1',
        conversation_id='conversation-1',
        history=[{'role': 'user', 'content': '记住这个决定'}],
    )


def _episode_success(
    key: str,
    *,
    status: str = 'created',
    mutation: bool = True,
) -> dict[str, Any]:
    return {
        'tool': 'episode_create',
        'success': True,
        'mutation': mutation,
        'result': {'status': status, 'idempotency_key': key},
        'retryable': False,
    }


def _tool_failure(
    *,
    tool: str = 'episode_create',
    key: str | None = None,
    mutation: bool | None = False,
    retryable: bool = True,
    code: str = 'storage_unavailable',
    message: str = 'Episode storage is unavailable.',
) -> dict[str, Any]:
    entry: dict[str, Any] = {
        'tool': tool,
        'success': False,
        'mutation': mutation,
        'retryable': retryable,
        'error': {
            'code': code,
            'message': message,
            'detail': {'internal_context': 'must not reach the response'},
        },
    }
    if key is not None:
        entry['result'] = {'idempotency_key': key}
    return entry


def _read_success() -> dict[str, Any]:
    return {
        'tool': 'read_memory_reference',
        'success': True,
        'mutation': False,
        'result': {'target': 'memory', 'content_length': 42},
        'retryable': False,
    }


def test_memory_review_prompt_is_episode_only():
    memory_review = _load_memory_review_module()

    prompt = memory_review.build_memory_review_prompt()

    assert '# Task' in prompt
    assert '# Episode Contract' in prompt
    assert '# What to Save or Skip' in prompt
    assert 'Create exactly one Episode per MemoryTools_episode_create call' in prompt
    assert 'multiple items in one MemoryTools_episode_create call' not in prompt
    assert 'The only write operation is MemoryTools_episode_create' in prompt
    assert 'Nothing to save' in prompt
    assert 'stable user preferences or profile facts' in prompt
    assert 'Do not invent timestamps, IDs, users, tasks, conversations, or source fields' in prompt
    assert 'memory/profile' not in prompt
    assert 'user_preference' not in prompt


def test_memory_review_prompt_does_not_embed_ordinary_memory_state():
    memory_review = _load_memory_review_module()

    prompt = memory_review.build_memory_review_prompt()

    assert 'EXISTING STATE' not in prompt
    assert 'Current agent working memory' not in prompt
    assert 'Current user profile' not in prompt
    assert 'Use the conversation history as the source of truth' in prompt


def test_memory_review_payload_allows_missing_or_null_llm_config():
    memory_review_routes = _load_memory_review_routes_module()

    missing = memory_review_routes.MemoryReviewPayload(
        task_id=' memory_review_core-task-missing-config ',
        user_id=' user-1 ',
        conversation_id=' conversation-1 ',
        history=[{'role': 'user', 'content': '你好'}],
    )
    explicit_null = memory_review_routes.MemoryReviewPayload(
        task_id='memory_review_core-task-null-config',
        user_id='user-1',
        conversation_id='conversation-1',
        history=[{'role': 'user', 'content': '你好'}],
        llm_config=None,
    )

    assert missing.user_id == 'user-1'
    assert missing.conversation_id == 'conversation-1'
    assert missing.llm_config is None
    assert explicit_null.llm_config is None


def test_memory_review_route_returns_missing_context_when_conversation_id_is_absent(monkeypatch):
    memory_review_routes = _load_memory_review_routes_module()

    assert 'conversation_id' in (
        memory_review_routes.MemoryReviewPayload.model_json_schema()['required']
    )

    monkeypatch.setattr(
        memory_review_routes,
        'review_memory',
        lambda **_kwargs: pytest.fail('review_memory must not run without conversation_id'),
    )
    app = FastAPI()
    app.include_router(memory_review_routes.router)

    response = TestClient(app).post('/api/chat/memory_review', json={
        'task_id': 'memory_review_core-task-missing-conversation',
        'user_id': 'user-1',
        'history': [{'role': 'user', 'content': '你好'}],
    })

    assert response.status_code == 200
    assert response.json() == {
        'status': 'failed',
        'task_id': 'memory_review_core-task-missing-conversation',
        'outcome': 'failed',
        'retryable': False,
        'error': {
            'code': 'missing_context',
            'message': 'conversation_id is required.',
        },
    }


def test_memory_review_route_returns_task_id(monkeypatch):
    memory_review_routes = _load_memory_review_routes_module()

    def fake_review_memory(**kwargs):
        assert kwargs == {
            'task_id': 'memory_review_core-task-123',
            'user_id': 'user-1',
            'conversation_id': 'conversation-1',
            'history': [{'role': 'user', 'content': '你好'}],
            'llm_config': None,
        }
        return memory_review_routes.MemoryReviewResult(
            status='success',
            task_id=kwargs['task_id'],
            outcome='saved',
        )

    monkeypatch.setattr(memory_review_routes, 'review_memory', fake_review_memory)
    payload = memory_review_routes.MemoryReviewPayload(
        task_id='memory_review_core-task-123',
        user_id='user-1',
        conversation_id='conversation-1',
        history=[{'role': 'user', 'content': '你好'}],
    )

    result = asyncio.run(memory_review_routes.memory_review(payload))

    assert result == {
        'status': 'success',
        'task_id': 'memory_review_core-task-123',
        'outcome': 'saved',
        'retryable': False,
    }


def test_memory_review_route_returns_business_failure_without_http_error(monkeypatch):
    memory_review_routes = _load_memory_review_routes_module()

    monkeypatch.setattr(
        memory_review_routes,
        'review_memory',
        lambda **kwargs: memory_review_routes.MemoryReviewResult(
            status='failed',
            task_id=kwargs['task_id'],
            outcome='failed',
            retryable=True,
            error={
                'code': 'storage_unavailable',
                'message': 'Episode storage is unavailable.',
            },
        ),
    )
    app = FastAPI()
    app.include_router(memory_review_routes.router)
    response = TestClient(app).post('/api/chat/memory_review', json={
        'task_id': 'memory_review_core-task-failed',
        'user_id': 'user-1',
        'conversation_id': 'conversation-1',
        'history': [{'role': 'user', 'content': '记住这个决定'}],
    })

    assert response.status_code == 200
    assert response.json() == {
        'status': 'failed',
        'task_id': 'memory_review_core-task-failed',
        'outcome': 'failed',
        'retryable': True,
        'error': {
            'code': 'storage_unavailable',
            'message': 'Episode storage is unavailable.',
        },
    }


def test_memory_review_route_returns_http_500_for_unhandled_exception(monkeypatch):
    memory_review_routes = _load_memory_review_routes_module()

    def fail_review(**_kwargs):
        raise RuntimeError('database password must not leak')

    monkeypatch.setattr(memory_review_routes, 'review_memory', fail_review)
    payload = memory_review_routes.MemoryReviewPayload(
        task_id='memory_review_core-task-crashed',
        user_id='user-1',
        conversation_id='conversation-1',
        history=[{'role': 'user', 'content': '记住这个决定'}],
    )

    result = asyncio.run(memory_review_routes.memory_review(payload))

    assert result.status_code == 500
    assert json.loads(result.body) == {
        'status': 'failed',
        'task_id': 'memory_review_core-task-crashed',
        'outcome': 'failed',
        'retryable': False,
        'error': {
            'code': 'internal_error',
            'message': 'Memory Review failed unexpectedly.',
        },
    }


def test_review_memory_runs_agent_with_episode_tools(monkeypatch):
    memory_review = _load_memory_review_module()

    calls = {}

    class FakeModel:
        def __init__(self, *args, **kwargs):
            calls['model_args'] = (args, kwargs)

    class FakeReactAgent:
        def __init__(self, **kwargs):
            calls['agent_kwargs'] = kwargs

        def __call__(self, prompt, llm_chat_history=None):
            calls['prompt'] = prompt
            calls['history'] = llm_chat_history
            config = fake_lazyllm.globals['agentic_config']
            calls['initial_tool_results'] = list(config['memory_tool_results'])
            config['memory_tool_results'].append({
                'tool': 'episode_create',
                'success': True,
                'mutation': True,
                'result': {
                    'status': 'created',
                    'idempotency_key': 'episode-decision',
                },
                'retryable': False,
            })
            return '已保存。'

    fake_lazyllm = SimpleNamespace(
        globals=_SidDict(),
        locals=_SidDict({'_lazyllm_agent': {'completed': [{'stale': True}]}}),
        tools=SimpleNamespace(agent=SimpleNamespace(ReactAgent=FakeReactAgent)),
    )

    class FakeMemoryTools:
        __public_apis__ = ['episode_create']

        def episode_create(self, *args, **kwargs):
            return None

    memory_tools = FakeMemoryTools()

    def normalize_history_for_agent(history):
        calls['normalizer_input'] = history
        return [{'role': 'user', 'content': 'normalized'}]

    def inject_model_config(config):
        calls['model_config'] = config

    _patch_runtime_bindings(
        monkeypatch,
        memory_review,
        lazyllm_module=fake_lazyllm,
        auto_model=FakeModel,
        fs=object,
        memory_tools=memory_tools,
        config={'core_api_url': 'http://core', 'review_max_retries': 2},
        inject_model_config=inject_model_config,
        normalize_history_for_agent=normalize_history_for_agent,
    )
    monkeypatch.setattr(memory_review, 'time_ns', lambda: 1_234_567_890_000_000)

    result = memory_review.review_memory(
        task_id='memory_review_core-task-123',
        user_id='user-1',
        conversation_id='conversation-1',
        history=[{'role': 'user', 'content': '发布方案确定为蓝色发布'}],
        llm_config={'llm': {'model': 'test'}},
    )

    assert result.model_dump() == {
        'status': 'success',
        'task_id': 'memory_review_core-task-123',
        'outcome': 'saved',
        'retryable': False,
        'error': None,
    }
    assert calls['agent_kwargs']['tools'] == [memory_tools]
    assert calls['normalizer_input'] == [{'role': 'user', 'content': '发布方案确定为蓝色发布'}]
    assert calls['history'] == [{'role': 'user', 'content': 'normalized'}]
    assert 'The only write operation is MemoryTools_episode_create' in calls['prompt']
    assert 'EXISTING STATE' not in calls['prompt']
    assert fake_lazyllm.globals['agentic_config']['user_id'] == 'user-1'
    assert fake_lazyllm.globals['agentic_config']['task_id'] == 'memory_review_core-task-123'
    assert fake_lazyllm.globals['agentic_config']['conversation_id'] == 'conversation-1'
    assert fake_lazyllm.globals['agentic_config']['review_started_at_ms'] == 1_234_567_890
    assert calls['initial_tool_results'] == []
    assert 'memory' not in fake_lazyllm.globals['agentic_config']
    assert 'user_preference' not in fake_lazyllm.globals['agentic_config']
    assert 'core_api_url' not in fake_lazyllm.globals['agentic_config']
    assert calls['model_config'] == {'llm': {'model': 'test'}}
    assert calls['model_args'] == ((), {'model': 'llm'})


def test_review_memory_returns_success_when_no_tool_submission(monkeypatch):
    memory_review = _load_memory_review_module()
    calls = {}

    class FakeModel:
        def __init__(self, *args, **kwargs):
            pass

    class FakeReactAgent:
        def __init__(self, **kwargs):
            pass

        def __call__(self, prompt, llm_chat_history=None):
            return 'Nothing to save.'

    fake_lazyllm = SimpleNamespace(
        globals=_SidDict(),
        locals=_SidDict({'_lazyllm_agent': {}}),
        tools=SimpleNamespace(agent=SimpleNamespace(ReactAgent=FakeReactAgent)),
    )

    def inject_model_config(config):
        calls['model_config'] = config

    _patch_runtime_bindings(
        monkeypatch,
        memory_review,
        lazyllm_module=fake_lazyllm,
        auto_model=FakeModel,
        fs=object,
        memory_tools=object(),
        config={'core_api_url': 'http://core', 'review_max_retries': 2},
        inject_model_config=inject_model_config,
    )

    result = memory_review.review_memory(
        task_id='memory_review_core-task-no-submission',
        user_id='user-1',
        conversation_id='conversation-1',
        history=[{'role': 'user', 'content': '你好'}],
    )

    assert result.model_dump() == {
        'status': 'success',
        'task_id': 'memory_review_core-task-no-submission',
        'outcome': 'no_changes',
        'retryable': False,
        'error': None,
    }
    assert calls['model_config'] is None


def test_review_memory_reports_partial_when_one_write_succeeds_and_another_fails(monkeypatch):
    result = _run_review_with_tool_results(monkeypatch, [
        _tool_failure(key='episode-b'),
        _episode_success('episode-a'),
    ])

    assert result.model_dump() == {
        'status': 'failed',
        'task_id': 'memory_review_core-task-results',
        'outcome': 'partial',
        'retryable': False,
        'error': {
            'code': 'storage_unavailable',
            'message': 'Persistent memory storage is temporarily unavailable.',
        },
    }


def test_review_memory_fails_when_agent_makes_no_write_decision(monkeypatch):
    result = _run_review_with_tool_results(
        monkeypatch,
        [_read_success()],
        response='Review complete.',
    )

    assert result.model_dump() == {
        'status': 'failed',
        'task_id': 'memory_review_core-task-results',
        'outcome': 'no_write_decision',
        'retryable': False,
        'error': {
            'code': 'no_write_decision',
            'message': (
                'Memory Review completed without a write tool call or an explicit '
                '\'Nothing to save\' decision.'
            ),
        },
    }


def test_review_memory_does_not_hide_read_failure_behind_no_changes(monkeypatch):
    result = _run_review_with_tool_results(
        monkeypatch,
        [_tool_failure(tool='read_memory_reference', message='Memory storage is unavailable.')],
        response='Nothing to save because there are no durable facts.',
    )

    assert result.model_dump() == {
        'status': 'failed',
        'task_id': 'memory_review_core-task-results',
        'outcome': 'failed',
        'retryable': True,
        'error': {
            'code': 'storage_unavailable',
            'message': 'Persistent memory storage is temporarily unavailable.',
        },
    }


def test_review_memory_reports_partial_when_write_succeeds_after_read_failure(monkeypatch):
    result = _run_review_with_tool_results(monkeypatch, [
        _tool_failure(tool='read_memory_reference', message='Memory storage is unavailable.'),
        _episode_success('episode-a'),
    ])

    assert result.model_dump() == {
        'status': 'failed',
        'task_id': 'memory_review_core-task-results',
        'outcome': 'partial',
        'retryable': False,
        'error': {
            'code': 'storage_unavailable',
            'message': 'Persistent memory storage is temporarily unavailable.',
        },
    }


def test_review_memory_merges_read_and_write_failures(monkeypatch):
    result = _run_review_with_tool_results(monkeypatch, [
        _tool_failure(
            tool='read_memory_reference',
            code='storage_read_failed',
            message='Memory storage read failed.',
        ),
        _tool_failure(key='episode-a'),
    ])

    assert result.model_dump() == {
        'status': 'failed',
        'task_id': 'memory_review_core-task-results',
        'outcome': 'failed',
        'retryable': True,
        'error': {
            'code': 'multiple_tool_failures',
            'message': (
                'Persistent memory storage could not be read. | '
                'Persistent memory storage is temporarily unavailable.'
            ),
        },
    }


def test_review_memory_allows_retry_when_every_failed_write_has_no_side_effect(monkeypatch):
    result = _run_review_with_tool_results(monkeypatch, [
        _tool_failure(key='episode-a'),
    ])

    assert result.model_dump() == {
        'status': 'failed',
        'task_id': 'memory_review_core-task-results',
        'outcome': 'failed',
        'retryable': True,
        'error': {
            'code': 'storage_unavailable',
            'message': 'Persistent memory storage is temporarily unavailable.',
        },
    }


def test_review_memory_blocks_retry_when_write_side_effect_is_uncertain(monkeypatch):
    result = _run_review_with_tool_results(monkeypatch, [
        _tool_failure(
            key='episode-a',
            mutation=None,
            code='storage_timeout',
            message='Episode storage timed out after the write request.',
        ),
    ])

    assert result.outcome == 'failed'
    assert result.retryable is False


def test_review_memory_later_success_resolves_failure_with_same_idempotency_key(monkeypatch):
    result = _run_review_with_tool_results(monkeypatch, [
        _tool_failure(key='episode-a'),
        _episode_success('episode-a', status='idempotent', mutation=False),
    ])

    assert result.model_dump() == {
        'status': 'success',
        'task_id': 'memory_review_core-task-results',
        'outcome': 'saved',
        'retryable': False,
        'error': None,
    }


def test_review_memory_deduplicates_and_combines_unresolved_write_errors(monkeypatch):
    result = _run_review_with_tool_results(monkeypatch, [
        _tool_failure(key='episode-a'),
        _tool_failure(key='episode-b'),
        _tool_failure(
            tool='episode_create',
            retryable=False,
            code='invalid_arguments',
            message='The requested memory edit is invalid.',
        ),
    ])

    assert result.model_dump() == {
        'status': 'failed',
        'task_id': 'memory_review_core-task-results',
        'outcome': 'failed',
        'retryable': False,
        'error': {
            'code': 'multiple_write_failures',
            'message': (
                'Persistent memory storage is temporarily unavailable. | '
                'A memory tool rejected invalid arguments.'
            ),
        },
    }


def test_review_memory_does_not_expose_tool_exception_text_in_response(monkeypatch):
    result = _run_review_with_tool_results(monkeypatch, [
        _tool_failure(
            key='episode-a',
            message=(
                'OpenSearch failed at /srv/private/episodes with '
                'Authorization: Bearer secret-value'
            ),
        ),
    ])

    assert result.error is not None
    assert result.error.message == 'Persistent memory storage is temporarily unavailable.'
    assert '/srv/private' not in result.error.message
    assert 'secret-value' not in result.error.message
