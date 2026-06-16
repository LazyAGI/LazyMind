from __future__ import annotations

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
        'lazymind.chat.service',
        'lazymind.chat.service.component',
        'lazymind.chat.service.component.history',
        'lazymind.config',
        'lazymind.model_config',
        'lazymind.review',
        'lazymind.review.memory_review',
        'lazymind.review.memory_review.prompts',
        'lazymind.review.service',
        'lazymind.review.service.memory_review',
    ]
    original_modules = {name: sys.modules.get(name) for name in module_names}

    fake_modules = {
        'lazymind': _package('lazymind'),
        'lazymind.review': _package('lazymind.review'),
        'lazymind.review.memory_review': _package('lazymind.review.memory_review'),
        'lazymind.review.service': _package('lazymind.review.service'),
    }
    fake_lazyllm = ModuleType('lazyllm')
    fake_lazyllm.AutoModel = object
    fake_lazyllm.globals = _SidDict()
    fake_lazyllm.locals = _SidDict()
    fake_fs_client = ModuleType('lazyllm.tools.fs.client')
    fake_fs_client.FS = object
    fake_tools_pkg = ModuleType('lazymind.chat.engine.tools')
    fake_tools_pkg.memory_editor = lambda *args, **kwargs: None
    fake_history = ModuleType('lazymind.chat.service.component.history')
    fake_history.normalize_history_for_agent = lambda history: history
    fake_config = ModuleType('lazymind.config')
    fake_config.config = {'core_api_url': 'http://core', 'review_max_retries': 2}
    fake_model_config = ModuleType('lazymind.model_config')
    fake_model_config.get_config_path = lambda: '/tmp/config.yaml'
    fake_model_config.inject_model_config = lambda _config: None
    fake_modules['lazyllm'] = fake_lazyllm
    fake_modules['lazyllm.tools'] = _package('lazyllm.tools')
    fake_modules['lazyllm.tools.fs'] = _package('lazyllm.tools.fs')
    fake_modules['lazyllm.tools.fs.client'] = fake_fs_client
    fake_modules['lazymind.chat'] = _package('lazymind.chat')
    fake_modules['lazymind.chat.engine'] = _package('lazymind.chat.engine')
    fake_modules['lazymind.chat.engine.tools'] = fake_tools_pkg
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
        return SimpleNamespace(
            memory_prompts=memory_prompts,
            memory_review=memory_review,
        )
    finally:
        for name, original in original_modules.items():
            if original is None:
                sys.modules.pop(name, None)
            else:
                sys.modules[name] = original


def _load_memory_review_module():
    return _load_review_modules().memory_review


def _patch_runtime_bindings(
    monkeypatch,
    memory_review,
    *,
    lazyllm_module,
    auto_model,
    fs,
    memory_editor,
    config: dict[str, Any],
    get_config_path=None,
    inject_model_config=None,
    normalize_history_for_agent=None,
) -> None:
    if get_config_path is None:
        get_config_path = lambda: '/tmp/config.yaml'
    if inject_model_config is None:
        inject_model_config = lambda _config: None
    if normalize_history_for_agent is None:
        def normalize_history_for_agent(history):
            return history

    monkeypatch.setattr(memory_review, 'lazyllm', lazyllm_module)
    monkeypatch.setattr(memory_review, 'AutoModel', auto_model)
    monkeypatch.setattr(memory_review, 'FS', fs)
    monkeypatch.setattr(memory_review, 'memory_editor', memory_editor)
    monkeypatch.setattr(memory_review, '_cfg', config)
    monkeypatch.setattr(memory_review, 'get_config_path', get_config_path)
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

    assert "memory_editor(target='memory'" in prompt
    assert "memory_editor(target='user'" in prompt
    assert 'operations' in prompt
    assert '# Task' in prompt
    assert '# Available Targets' in prompt
    assert '# What to Save or Skip' in prompt
    assert '# Existing State and Conflict Rules' in prompt
    assert '# Tool Contract' in prompt
    assert 'Make at most one memory_editor call' in prompt
    assert 'When in doubt, do not save memory' in prompt
    assert '{"op": "replace_text", "old": "...", "new": "..."}' in prompt
    assert '{"op": "replace_all", "content": "..."}' in prompt
    assert 'Prefer replace_text with exact old text copied from the selected target' in prompt
    assert 'Determine the language of new or rewritten memory/user profile content from the selected target' in prompt
    assert "use the dominant language of the user's messages in the conversation history" in prompt
    assert 'do not switch to English just because these instructions are written in English' in prompt
    assert 'memory_editor requires exactly target and operations' in prompt
    assert 'Current agent working memory' in prompt
    assert 'Current user profile' in prompt
    assert 'Environment context' not in prompt
    assert 'Do NOT save multi-step reusable workflows' in prompt
    assert 'reusable workflows' in prompt
    assert 'skill_editor' not in prompt
    assert '# User Profile Format' in prompt
    assert 'agent_persona' in prompt
    assert 'user_address' in prompt
    assert 'response_style' in prompt
    assert 'role the user wants the agent to play' in prompt
    assert 'how the user wants the agent to address them' in prompt
    assert 'display/use exactly one of 简洁, 详细, 幽默, 正式' in prompt
    assert '简洁, 详细, 幽默, 正式' in prompt
    assert 'concise, detailed, humorous, formal' in prompt
    assert 'existing valid response_style in either language' in prompt
    assert 'Do not put language preferences' in prompt
    assert 'verbs, or full instructions' in prompt
    assert 'response_style is unknown' in prompt
    assert 'use ""' in prompt
    assert 'never use generic acknowledgement text' in prompt
    assert 'only when the user explicitly asks to change that specific field' in prompt
    assert 'use replace_all to rewrite the whole user profile into the frontmatter-plus-body format' in prompt


def test_user_review_prompt_excludes_session_history():
    memory_review = _load_memory_review_module()

    prompt = memory_review.build_memory_review_prompt(
        memory='旧记忆',
        user='旧用户画像',
    )

    assert '旧记忆' in prompt
    assert '旧用户画像' in prompt
    assert 'Choose the single most appropriate target' in prompt
    assert "memory_editor(target='user'" in prompt
    assert "Do not call memory_editor with target='memory'" not in prompt


def test_review_memory_runs_agent_with_memory_editor_tool(monkeypatch):
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
            return '已保存。'

    fake_lazyllm = SimpleNamespace(
        globals=_SidDict(),
        locals=_SidDict({'_lazyllm_agent': {'completed': [{'stale': True}]}}),
        tools=SimpleNamespace(agent=SimpleNamespace(ReactAgent=FakeReactAgent)),
    )

    def memory_editor(*args, **kwargs):
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
        config={'core_api_url': 'http://core', 'review_max_retries': 2},
        inject_model_config=inject_model_config,
        normalize_history_for_agent=normalize_history_for_agent,
    )

    result = memory_review.review_memory(
        user_id='user-1',
        history=[{'role': 'user', 'content': '以后请用中文简洁回答'}],
        memory='旧记忆',
        user='旧用户画像',
        llm_config={'llm': {'model': 'test'}},
    )

    assert result.model_dump() == {'status': 'success'}
    assert [tool.__name__ for tool in calls['agent_kwargs']['tools']] == ['memory_editor']
    assert calls['normalizer_input'] == [{'role': 'user', 'content': '以后请用中文简洁回答'}]
    assert calls['history'] == [{'role': 'user', 'content': 'normalized'}]
    assert fake_lazyllm.globals['agentic_config']['user_id'] == 'user-1'
    assert fake_lazyllm.globals['agentic_config']['memory'] == '旧记忆'
    assert fake_lazyllm.globals['agentic_config']['user'] == '旧用户画像'
    assert calls['model_config'] == {'llm': {'model': 'test'}}
    assert 'user_preference' not in fake_lazyllm.globals['agentic_config']


def test_review_memory_returns_success_when_no_tool_submission(monkeypatch):
    memory_review = _load_memory_review_module()

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

    _patch_runtime_bindings(
        monkeypatch,
        memory_review,
        lazyllm_module=fake_lazyllm,
        auto_model=FakeModel,
        fs=object,
        memory_editor=memory_editor,
        config={'core_api_url': 'http://core', 'review_max_retries': 2},
    )

    result = memory_review.review_memory(
        user_id='user-1',
        history=[{'role': 'user', 'content': '你好'}],
        memory='',
        user='',
        llm_config={'llm': {'model': 'test'}},
    )

    assert result.model_dump() == {'status': 'success'}
