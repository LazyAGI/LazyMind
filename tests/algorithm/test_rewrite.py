import importlib
import sys
from pathlib import Path
from types import ModuleType

import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient


def _load_rewrite_module():
    fake_lazyllm = ModuleType('lazyllm')
    fake_lazyllm.AutoModel = lambda *args, **kwargs: object()
    fake_lazyllm.config = {}
    fake_lazyllm_configs = ModuleType('lazyllm.configs')

    class FakeConfig(dict):
        def __init__(self, *args, **kwargs):
            super().__init__()

        def add(self, name, _type, default, *_args, **_kwargs):
            self[name] = default

    fake_lazyllm_configs.Config = FakeConfig

    fake_tool_infra = ModuleType('lazymind.chat.engine.tools.infra')
    fake_load_config = ModuleType('lazymind.model_config')
    fake_load_config.get_config_path = lambda: ''

    original_modules = {
        'lazyllm': sys.modules.get('lazyllm'),
        'lazyllm.configs': sys.modules.get('lazyllm.configs'),
        'lazymind.chat.engine.tools.infra': sys.modules.get('lazymind.chat.engine.tools.infra'),
        'lazymind.model_config': sys.modules.get('lazymind.model_config'),
    }

    try:
        sys.modules['lazyllm'] = fake_lazyllm
        sys.modules['lazyllm.configs'] = fake_lazyllm_configs
        sys.modules['lazymind.chat.engine.tools.infra'] = fake_tool_infra
        sys.modules['lazymind.model_config'] = fake_load_config

        from algorithm.lazymind.rewrite import base
        from algorithm.lazymind.rewrite import memory
        from algorithm.lazymind.rewrite import preference

        ns = ModuleType('test_rewrite_module')
        ns.BadRequestError = base.BadRequestError
        ns.UnprocessableContentError = base.UnprocessableContentError
        ns._apply_memory_edit_operations = memory._apply_memory_edit_operations
        ns._apply_user_preference_edit_operations = preference._apply_user_preference_edit_operations
        ns._PROMPT_BUILDERS = base._PROMPT_BUILDERS
        ns.RewriteTaskType = base.RewriteTaskType
        ns._format_inputs_block = base._format_inputs_block
        ns._validate_generated_content = base._validate_generated_content
        ns.rewrite_content = base.rewrite_content
        ns._MAX_MANAGED_CONTENT_CHARS = base._MAX_MANAGED_CONTENT_CHARS
        return ns
    finally:
        for name, original in original_modules.items():
            if original is None:
                sys.modules.pop(name, None)
            else:
                sys.modules[name] = original


rewrite = _load_rewrite_module()
BadRequestError = rewrite.BadRequestError
UnprocessableContentError = rewrite.UnprocessableContentError
_apply_memory_edit_operations = rewrite._apply_memory_edit_operations
_apply_user_preference_edit_operations = rewrite._apply_user_preference_edit_operations
_PROMPT_BUILDERS = rewrite._PROMPT_BUILDERS
_format_inputs_block = rewrite._format_inputs_block
_validate_generated_content = rewrite._validate_generated_content
rewrite_content = rewrite.rewrite_content
_MAX_MANAGED_CONTENT_CHARS = rewrite._MAX_MANAGED_CONTENT_CHARS


def _load_rewrite_routes_module():
    module_path = (
        Path(__file__).resolve().parents[2]
        / 'algorithm/lazymind/rewrite/api/rewrite_routes.py'
    )
    spec = importlib.util.spec_from_file_location('test_rewrite_routes', module_path)
    assert spec is not None
    assert spec.loader is not None

    fake_lazyllm = ModuleType('lazyllm')
    fake_lazyllm.globals = type('Globals', (), {'_init_sid': lambda self, sid=None: None})()
    fake_lazyllm.locals = type('Locals', (), {'_init_sid': lambda self, sid=None: None})()
    fake_model_config = ModuleType('lazymind.model_config')
    fake_model_config.inject_model_config = lambda *_args, **_kwargs: None

    original_modules = {
        'lazyllm': sys.modules.get('lazyllm'),
        'lazymind.model_config': sys.modules.get('lazymind.model_config'),
        'lazymind.rewrite': sys.modules.get('lazymind.rewrite'),
    }

    module = importlib.util.module_from_spec(spec)
    try:
        sys.modules['lazyllm'] = fake_lazyllm
        sys.modules['lazymind.model_config'] = fake_model_config
        sys.modules['lazymind.rewrite'] = rewrite
        sys.modules[spec.name] = module
        spec.loader.exec_module(module)
        module.RewritePayload.model_rebuild()
        return module
    finally:
        for name, original in original_modules.items():
            if original is None:
                sys.modules.pop(name, None)
            else:
                sys.modules[name] = original


def test_format_inputs_block_includes_required_user_instruct():
    block = _format_inputs_block(
        content='old content',
        user_instruct='rewrite this',
    )

    assert '2) user_instruct' in block
    assert '2) suggestions' not in block


def test_rewrite_content_requires_user_instruct():
    try:
        rewrite_content(
            task_type='memory',
            content='old content',
            user_instruct='  ',
        )
    except BadRequestError as exc:
        assert "'user_instruct' must be a non-empty string." == str(exc)
    else:
        raise AssertionError('Expected BadRequestError')


def test_generate_prompts_include_stale_content_governance():
    for task_type in ('skill', 'memory'):
        prompt = _PROMPT_BUILDERS[task_type](
            content='old content that may now be stale',
            user_instruct='Outdated=TRUE: replace old KB failure diagnosis with the current service-level cause.',
        )

        assert 'bounded, continuously maintained store' in prompt
        assert 'not an append-only log' in prompt
        assert 'Outdated=TRUE is only one stale signal' in prompt
        assert 'Even when the limit is not exceeded' in prompt
        assert 'proactively compress, consolidate, or delete stale information' in prompt
        assert 'Current content length after removing whitespace' in prompt
        assert 'Remaining budget before applying user_instruct' in prompt
        assert 'upsert' not in prompt


def test_skill_prompt_does_not_require_frontmatter_category():
    prompt = _PROMPT_BUILDERS['skill'](
        content='---\nname: example\ndescription: Example skill.\n---\nUse it.\n',
        user_instruct='Make the steps clearer.',
    )

    assert 'non-empty name and description fields' in prompt
    assert 'name, category, and description' not in prompt


def test_skill_rewrite_validation_ignores_frontmatter_category():
    category_free = (
        '---\n'
        'name: category-free\n'
        'description: Category-free skill.\n'
        '---\n'
        'Use it.\n'
    )
    arbitrary_category = (
        '---\n'
        'name: arbitrary-category\n'
        'category: "任意/上游 category"\n'
        'description: Arbitrary-category skill.\n'
        '---\n'
        'Use it too.\n'
    )

    assert _validate_generated_content('skill', category_free) == category_free
    assert _validate_generated_content('skill', arbitrary_category) == arbitrary_category


def test_polish_prompt_asks_model_to_rewrite_without_answering():
    prompt = _PROMPT_BUILDERS['polish'](
        content='怎么写一个RAG系统',
        user_instruct='让问题更清晰',
    )

    assert 'task type: polish' in prompt
    assert 'Do not answer the prompt.' in prompt
    assert '{"content": "<new complete text>"}' in prompt


def test_user_preference_prompt_requires_yaml_frontmatter():
    prompt = _PROMPT_BUILDERS['user_preference'](
        content='Prefers concise replies',
        user_instruct='Keep replies in Chinese.',
    )

    assert 'Format requirements' in prompt
    assert 'agent_persona' in prompt
    assert 'preferred_name' in prompt
    assert 'response_style' in prompt
    assert '智能体角色' not in prompt
    assert '用户称谓' not in prompt
    assert '回复风格' not in prompt
    assert '技术助理' not in prompt
    assert 'legacy/free-form' not in prompt
    assert 'free-form without any YAML frontmatter' in prompt
    assert 'frontmatter-plus-body format' in prompt
    assert 'containing exactly agent_persona, preferred_name, and response_style fields' in prompt
    assert 'explicit stable agent persona' in prompt
    assert 'preferred name/address' in prompt
    assert 'short response style' in prompt
    assert '100 characters or less' in prompt
    assert 'existing response_style' in prompt
    assert 'Do not put language preferences' in prompt
    assert 'verbs, or full instructions' in prompt
    assert 'keep existing frontmatter values unchanged' in prompt
    assert 'response_style is unknown' in prompt
    assert 'use ""' in prompt
    assert 'never use generic acknowledgement text' in prompt
    assert 'only when user_instruct explicitly asks to change that specific field' in prompt


def test_user_preference_validation_only_enforces_length_limit():
    valid = (
        '---\n'
        'agent_persona: "algorithm collaborator"\n'
        'preferred_name: ""\n'
        'response_style: "concise"\n'
        '---\n'
        '- Prefer manual git commits.\n'
    )
    assert _validate_generated_content('user_preference', valid) == valid
    # Legacy frontmatter shape is no longer schema-validated here.
    assert _validate_generated_content('user_preference', 'free-form preference notes') == (
        'free-form preference notes'
    )

    oversized = 'x' * (_MAX_MANAGED_CONTENT_CHARS + 1)
    with pytest.raises(UnprocessableContentError):
        _validate_generated_content('user_preference', oversized)


def test_memory_edit_operations_use_replace_text_to_add_day_and_edit_text():
    current = (
        '- 2026-05-14\n'
        '  用户在做:\n'
        '  - old task\n'
        '  状态/冲突:\n'
        '  - likes tea'
    )

    edited = _apply_memory_edit_operations(
        current,
        {
            'operations': [
                {
                    'op': 'replace_text',
                    'old': '',
                    'new': '- 2026-05-15\n  用户在做:\n  - new task',
                },
                {
                    'op': 'replace_text',
                    'old': 'likes tea',
                    'new': 'likes coffee',
                },
            ],
        },
    )

    assert edited == (
        '- 2026-05-14\n'
        '  用户在做:\n'
        '  - old task\n'
        '  状态/冲突:\n'
        '  - likes coffee\n'
        '- 2026-05-15\n'
        '  用户在做:\n'
        '  - new task'
    )


def test_memory_edit_operations_can_replace_existing_day_block():
    current = (
        '- 2026-05-14\n'
        '  用户在做:\n'
        '  - old task'
    )

    edited = _apply_memory_edit_operations(
        current,
        {
            'operations': [
                {
                    'op': 'replace_text',
                    'old': current,
                    'new': '- 2026-05-14\n  我们讨论了:\n  - new summary',
                },
            ],
        },
    )

    assert edited == '- 2026-05-14\n  我们讨论了:\n  - new summary'


def test_user_preference_edit_operations_can_clear_all_content_via_replace_all():
    edited = _apply_user_preference_edit_operations(
        'Prefers concise replies',
        {
            'operations': [
                {
                    'op': 'replace_all',
                    'content': '',
                },
            ],
        },
    )

    assert edited == ''


def test_rewrite_route_requires_user_instruct_and_llm_config(monkeypatch):
    rewrite_routes = _load_rewrite_routes_module()
    app = FastAPI()
    app.include_router(rewrite_routes.router)
    client = TestClient(app)

    def fake_rewrite_content(**kwargs):
        assert kwargs['task_type'] == 'polish'
        assert kwargs['user_instruct'] == 'Apply change'
        return 'new content'

    monkeypatch.setattr(
        rewrite_routes,
        'rewrite_content',
        fake_rewrite_content,
    )

    response = client.post(
        '/api/chat/rewrite',
        json={
            'task_type': 'polish',
            'content': 'old content',
            'user_instruct': 'Apply change',
            'llm_config': {},
        },
    )

    assert response.status_code == 200
    assert response.json() == {'content': 'new content'}


def test_rewrite_route_rejects_missing_user_instruct_or_llm_config():
    rewrite_routes = _load_rewrite_routes_module()
    app = FastAPI()
    app.include_router(rewrite_routes.router)
    client = TestClient(app)

    response = client.post(
        '/api/chat/rewrite',
        json={'task_type': 'memory', 'content': 'old content', 'llm_config': {}},
    )

    assert response.status_code == 422

    response = client.post(
        '/api/chat/rewrite',
        json={'task_type': 'memory', 'content': 'old content', 'user_instruct': 'Apply change'},
    )

    assert response.status_code == 422

    response = client.post(
        '/api/chat/rewrite',
        json={
            'task_type': 'unknown',
            'content': 'old content',
            'user_instruct': 'Apply change',
            'llm_config': {},
        },
    )

    assert response.status_code == 422
