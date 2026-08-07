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

        ns = ModuleType('test_rewrite_module')
        ns.BadRequestError = base.BadRequestError
        ns.UnprocessableContentError = base.UnprocessableContentError
        ns._PROMPT_BUILDERS = base._PROMPT_BUILDERS
        ns.RewriteTaskType = base.RewriteTaskType
        ns._format_inputs_block = base._format_inputs_block
        ns._validate_generated_content = base._validate_generated_content
        ns.rewrite_content = base.rewrite_content
        return ns
    finally:
        for name, original in original_modules.items():
            if original is None:
                sys.modules.pop(name, None)
            else:
                sys.modules[name] = original


rewrite = _load_rewrite_module()
BadRequestError = rewrite.BadRequestError
_PROMPT_BUILDERS = rewrite._PROMPT_BUILDERS
_format_inputs_block = rewrite._format_inputs_block
_validate_generated_content = rewrite._validate_generated_content
rewrite_content = rewrite.rewrite_content


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
            task_type='skill',
            content='old content',
            user_instruct='  ',
        )
    except BadRequestError as exc:
        assert "'user_instruct' must be a non-empty string." == str(exc)
    else:
        raise AssertionError('Expected BadRequestError')


def test_skill_prompt_includes_stale_content_governance():
    prompt = _PROMPT_BUILDERS['skill'](
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
        json={'task_type': 'skill', 'content': 'old content', 'llm_config': {}},
    )

    assert response.status_code == 422

    response = client.post(
        '/api/chat/rewrite',
        json={'task_type': 'skill', 'content': 'old content', 'user_instruct': 'Apply change'},
    )

    assert response.status_code == 422


@pytest.mark.parametrize('unsupported_task_type', ['memory', 'user_preference', 'unknown'])
def test_rewrite_route_rejects_removed_and_unknown_task_types(unsupported_task_type):
    rewrite_routes = _load_rewrite_routes_module()
    app = FastAPI()
    app.include_router(rewrite_routes.router)
    client = TestClient(app)

    response = client.post(
        '/api/chat/rewrite',
        json={
            'task_type': unsupported_task_type,
            'content': 'old content',
            'user_instruct': 'Apply change',
            'llm_config': {},
        },
    )

    assert response.status_code == 422
