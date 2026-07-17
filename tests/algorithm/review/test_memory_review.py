from __future__ import annotations

import asyncio
import importlib.util
import os
import sys
from pathlib import Path
from types import ModuleType, SimpleNamespace
from typing import Any

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
        'lazymind.chat.engine.tools.infra',
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
        exception=lambda *_args, **_kwargs: None,
        info=lambda *_args, **_kwargs: None,
    )
    fake_lazyllm.globals = _SidDict()
    fake_lazyllm.locals = _SidDict()
    fake_fs_client = ModuleType('lazyllm.tools.fs.client')
    fake_fs_client.FS = object
    fake_tools_pkg = ModuleType('lazymind.chat.engine.tools')
    fake_tools_pkg.memory_editor = lambda *args, **kwargs: None
    fake_tools_pkg.read_memory = lambda *args, **kwargs: None
    fake_infra = ModuleType('lazymind.chat.engine.tools.infra')
    fake_infra.MemoryRemoteStore = object
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
    fake_modules['lazymind.chat.engine.tools.infra'] = fake_infra
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
    memory_editor,
    config: dict[str, Any],
    read_memory=None,
    memory_store=None,
    inject_model_config=None,
    normalize_history_for_agent=None,
) -> None:
    if inject_model_config is None:
        inject_model_config = lambda _config: None
    if read_memory is None:
        def read_memory(*_args, **_kwargs):
            return None
    if memory_store is None:
        class MemoryStore:
            def read(self, target):
                return ''

        memory_store = MemoryStore
    if normalize_history_for_agent is None:
        def normalize_history_for_agent(history):
            return history

    monkeypatch.setattr(memory_review, 'lazyllm', lazyllm_module)
    monkeypatch.setattr(memory_review, 'AutoModel', auto_model)
    monkeypatch.setattr(memory_review, 'FS', fs)
    monkeypatch.setattr(memory_review, 'memory_editor', memory_editor)
    monkeypatch.setattr(memory_review, 'read_memory', read_memory)
    monkeypatch.setattr(memory_review, 'MemoryRemoteStore', memory_store)
    monkeypatch.setattr(memory_review, '_cfg', config)
    monkeypatch.setattr(memory_review, 'inject_model_config', inject_model_config)
    monkeypatch.setattr(
        memory_review,
        'normalize_history_for_agent',
        normalize_history_for_agent,
    )


def test_memory_review_prompt_excludes_preferences_and_workflows():
    memory_review = _load_memory_review_module()

    prompt = memory_review.build_memory_review_prompt(
        memory='',
        user='',
    )

    assert 'operations' not in prompt
    assert '# Task' in prompt
    assert '# Available Targets' in prompt
    assert '# What to Save or Skip' in prompt
    assert '# Existing State and Conflict Rules' in prompt
    assert '# Tool Contract' not in prompt
    assert 'Make at most one memory_editor call' in prompt
    assert 'When in doubt, do not save memory' in prompt
    assert "memory_editor(target='memory'" not in prompt
    assert "memory_editor(target='user_preference'" not in prompt
    assert "op='patch', old_text='...', new_text='...'" not in prompt
    assert "op='append', content='...'" not in prompt
    assert '{"op": "replace_all"' not in prompt
    assert 'Determine the language of new or rewritten memory/user profile content from the selected target' in prompt
    assert "use the dominant language of the user's messages in the conversation history" in prompt
    assert 'do not switch to English just because these instructions are written in English' in prompt
    assert 'memory_review table' not in prompt
    assert 'Current agent working memory' in prompt
    assert 'Current user profile' in prompt
    assert 'Environment context' not in prompt
    assert 'Do NOT save step-by-step SOPs' in prompt
    assert 'stable preference' in prompt
    assert 'currently doing is not necessarily something the user likes or prefers' in prompt
    assert 'consider saving it to memory instead' in prompt
    assert 'skill_editor' not in prompt
    assert '# User Profile Notes' in prompt
    assert "follow memory_editor's user_preference contract" in prompt
    assert 'agent_persona' not in prompt
    assert 'preferred_name' not in prompt
    assert 'response_style' not in prompt
    assert '智能体角色' not in prompt
    assert '用户称谓' not in prompt
    assert '回复风格' not in prompt
    assert 'role the user wants the agent to play' not in prompt
    assert '100 characters or less' not in prompt
    assert 'explicit stable agent persona' in prompt
    assert 'ordinary preferences in the Markdown body' in prompt
    assert 'keep existing frontmatter values unchanged' in prompt
    assert 'do not rewrite it wholesale with memory_editor' in prompt


def test_user_review_prompt_excludes_session_history():
    memory_review = _load_memory_review_module()

    prompt = memory_review.build_memory_review_prompt(
        memory='旧记忆',
        user='旧用户画像',
    )

    assert '旧记忆' in prompt
    assert '旧用户画像' in prompt
    assert 'Choose the single most appropriate target' in prompt
    assert "memory_editor(target='user_preference'" not in prompt
    assert "Do not call memory_editor with target='memory'" not in prompt


def test_memory_review_payload_allows_missing_or_null_llm_config():
    memory_review_routes = _load_memory_review_routes_module()

    missing = memory_review_routes.MemoryReviewPayload(
        task_id=' memory_review_core-task-missing-config ',
        user_id=' user-1 ',
        history=[{'role': 'user', 'content': '你好'}],
    )
    explicit_null = memory_review_routes.MemoryReviewPayload(
        task_id='memory_review_core-task-null-config',
        user_id='user-1',
        history=[{'role': 'user', 'content': '你好'}],
        llm_config=None,
    )

    assert missing.user_id == 'user-1'
    assert missing.llm_config is None
    assert explicit_null.llm_config is None


def test_memory_review_route_returns_task_id(monkeypatch):
    memory_review_routes = _load_memory_review_routes_module()

    def fake_review_memory(**kwargs):
        assert kwargs == {
            'task_id': 'memory_review_core-task-123',
            'user_id': 'user-1',
            'history': [{'role': 'user', 'content': '你好'}],
            'llm_config': None,
        }
        return memory_review_routes.MemoryReviewResult(status='success', task_id=kwargs['task_id'])

    monkeypatch.setattr(memory_review_routes, 'review_memory', fake_review_memory)
    payload = memory_review_routes.MemoryReviewPayload(
        task_id='memory_review_core-task-123',
        user_id='user-1',
        history=[{'role': 'user', 'content': '你好'}],
    )

    result = asyncio.run(memory_review_routes.memory_review(payload))

    assert result == {'status': 'success', 'task_id': 'memory_review_core-task-123'}


def test_review_memory_runs_agent_with_memory_editor_tool(monkeypatch):
    memory_review = _load_memory_review_module()

    calls = {}

    class FakeMemoryStore:
        def __init__(self):
            calls['store_created'] = True

        def read(self, target):
            calls.setdefault('store_reads', []).append(target)
            return {
                'memory': 'RemoteFS 记忆',
                'user_preference': 'RemoteFS 用户画像',
            }[target]

    class FakeModel:
        def __init__(self, *args, **kwargs):
            calls['model_args'] = (args, kwargs)

    class FakeReactAgent:
        def __init__(self, **kwargs):
            calls['agent_kwargs'] = kwargs

        def __call__(self, prompt, llm_chat_history=None):
            calls['prompt'] = prompt
            calls['history'] = llm_chat_history
            return '已保存。'

    fake_lazyllm = SimpleNamespace(
        globals=_SidDict(),
        locals=_SidDict({'_lazyllm_agent': {'completed': [{'stale': True}]}}),
        tools=SimpleNamespace(agent=SimpleNamespace(ReactAgent=FakeReactAgent)),
    )

    def memory_editor(*args, **kwargs):
        return None

    def read_memory(*args, **kwargs):
        return None

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
        memory_editor=memory_editor,
        read_memory=read_memory,
        memory_store=FakeMemoryStore,
        config={'core_api_url': 'http://core', 'review_max_retries': 2},
        inject_model_config=inject_model_config,
        normalize_history_for_agent=normalize_history_for_agent,
    )

    result = memory_review.review_memory(
        task_id='memory_review_core-task-123',
        user_id='user-1',
        history=[{'role': 'user', 'content': '以后请用中文简洁回答'}],
        llm_config={'llm': {'model': 'test'}},
    )

    assert result.model_dump() == {'status': 'success', 'task_id': 'memory_review_core-task-123'}
    assert [tool.__name__ for tool in calls['agent_kwargs']['tools']] == ['read_memory', 'memory_editor']
    assert calls['normalizer_input'] == [{'role': 'user', 'content': '以后请用中文简洁回答'}]
    assert calls['history'] == [{'role': 'user', 'content': 'normalized'}]
    assert 'RemoteFS 记忆' in calls['prompt']
    assert 'RemoteFS 用户画像' in calls['prompt']
    assert calls['store_reads'] == ['memory', 'user_preference']
    assert fake_lazyllm.globals['agentic_config']['user_id'] == 'user-1'
    assert fake_lazyllm.globals['agentic_config']['task_id'] == 'memory_review_core-task-123'
    assert 'memory' not in fake_lazyllm.globals['agentic_config']
    assert 'user_preference' not in fake_lazyllm.globals['agentic_config']
    assert 'core_api_url' not in fake_lazyllm.globals['agentic_config']
    assert calls['model_config'] == {'llm': {'model': 'test'}}
    assert calls['model_args'] == ((), {'model': 'llm'})


def test_review_memory_returns_success_when_no_tool_submission(monkeypatch):
    memory_review = _load_memory_review_module()
    calls = {}

    class FakeMemoryStore:
        def read(self, target):
            return ''

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

    def memory_editor(*args, **kwargs):
        return None

    def inject_model_config(config):
        calls['model_config'] = config

    _patch_runtime_bindings(
        monkeypatch,
        memory_review,
        lazyllm_module=fake_lazyllm,
        auto_model=FakeModel,
        fs=object,
        memory_editor=memory_editor,
        memory_store=FakeMemoryStore,
        config={'core_api_url': 'http://core', 'review_max_retries': 2},
        inject_model_config=inject_model_config,
    )

    result = memory_review.review_memory(
        task_id='memory_review_core-task-no-submission',
        user_id='user-1',
        history=[{'role': 'user', 'content': '你好'}],
    )

    assert result.model_dump() == {'status': 'success', 'task_id': 'memory_review_core-task-no-submission'}
    assert calls['model_config'] is None
