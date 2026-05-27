import importlib.util
import sys
from pathlib import Path
from types import ModuleType

from fastapi import FastAPI
from fastapi.testclient import TestClient


def _load_memory_generate_module():
    module_path = (
        Path(__file__).resolve().parents[2]
        / 'algorithm/lazymind/review/service/memory_generate.py'
    )
    spec = importlib.util.spec_from_file_location('test_memory_generate_module', module_path)
    assert spec is not None
    assert spec.loader is not None

    fake_lazyllm = ModuleType('lazyllm')
    fake_lazyllm.AutoModel = lambda *args, **kwargs: object()

    fake_skill_manager = ModuleType('lazymind.chat.engine.tools.skill_manager')
    fake_skill_manager._validate_skill_content = lambda *_args, **_kwargs: None

    fake_load_config = ModuleType('lazymind.model_config')
    fake_load_config.get_config_path = lambda: ''

    original_modules = {
        'lazyllm': sys.modules.get('lazyllm'),
        'lazymind.chat.engine.tools.skill_manager': sys.modules.get('lazymind.chat.engine.tools.skill_manager'),
        'lazymind.model_config': sys.modules.get('lazymind.model_config'),
    }

    module = importlib.util.module_from_spec(spec)
    try:
        sys.modules['lazyllm'] = fake_lazyllm
        sys.modules['lazymind.chat.engine.tools.skill_manager'] = fake_skill_manager
        sys.modules['lazymind.model_config'] = fake_load_config
        sys.modules[spec.name] = module
        spec.loader.exec_module(module)
        return module
    finally:
        for name, original in original_modules.items():
            if original is None:
                sys.modules.pop(name, None)
            else:
                sys.modules[name] = original


memory_generate = _load_memory_generate_module()
BadRequestError = memory_generate.BadRequestError
_apply_memory_edit_operations = memory_generate._apply_memory_edit_operations
_apply_user_preference_edit_operations = memory_generate._apply_user_preference_edit_operations
_build_generate_prompt = memory_generate._build_generate_prompt
_format_inputs_block = memory_generate._format_inputs_block
generate_memory_content = memory_generate.generate_memory_content


def _load_memory_generate_routes_module():
    module_path = (
        Path(__file__).resolve().parents[2]
        / 'algorithm/lazymind/chat/service/api/memory_generate_routes.py'
    )
    spec = importlib.util.spec_from_file_location('test_memory_generate_routes', module_path)
    assert spec is not None
    assert spec.loader is not None

    original_modules = {
        'lazymind.review.service.memory_generate': sys.modules.get('lazymind.review.service.memory_generate'),
    }

    module = importlib.util.module_from_spec(spec)
    try:
        sys.modules['lazymind.review.service.memory_generate'] = memory_generate
        sys.modules[spec.name] = module
        spec.loader.exec_module(module)
        module.GeneratePayload.model_rebuild()
        return module
    finally:
        for name, original in original_modules.items():
            if original is None:
                sys.modules.pop(name, None)
            else:
                sys.modules[name] = original


def test_format_inputs_block_includes_only_suggestions_when_user_instruct_missing():
    block = _format_inputs_block(
        content='old content',
        suggestions=[{'title': 't', 'content': 'c'}],
        user_instruct=None,
    )

    assert '2) suggestions' in block
    assert '3) user_instruct' not in block


def test_format_inputs_block_includes_only_user_instruct_when_suggestions_missing():
    block = _format_inputs_block(
        content='old content',
        suggestions=[],
        user_instruct='rewrite this',
    )

    assert '2) user_instruct' in block
    assert '2) suggestions' not in block


def test_generate_memory_content_requires_suggestions_or_user_instruct():
    try:
        generate_memory_content(
            memory_type='memory',
            content='old content',
            suggestions=[],
            user_instruct='  ',
        )
    except BadRequestError as exc:
        assert "At least one of 'suggestions' or 'user_instruct' must be provided." == str(exc)
    else:
        raise AssertionError('Expected BadRequestError')


def test_generate_prompts_include_stale_content_governance():
    suggestions = [{
        'title': 'Update stale memory',
        'content': 'Replace old KB failure diagnosis with the current service-level cause.',
        'reason': 'Previous memory is outdated',
        'outdated': True,
    }]

    for memory_type in ('skill', 'memory', 'user_preference'):
        prompt = _build_generate_prompt(
            memory_type=memory_type,
            content='old content that may now be stale',
            suggestions=suggestions,
            user_instruct=None,
        )

        assert 'bounded, continuously maintained store' in prompt
        assert 'not an append-only log' in prompt
        assert 'Outdated=TRUE is only one stale signal' in prompt
        assert 'Even when the limit is not exceeded' in prompt
        assert 'proactively compress, consolidate, or delete stale information' in prompt
        assert 'Current content length after removing whitespace' in prompt
        assert 'Remaining budget before merging suggestions' in prompt


def test_memory_edit_operations_preserve_upsert_day_before_replace_text():
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
                    'op': 'upsert_day',
                    'date': '2026-05-15',
                    'doing': ['new task'],
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


def test_memory_edit_operations_can_clear_all_memory_via_upsert_replace():
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
                    'op': 'upsert_day',
                    'date': '2026-05-14',
                    'replace': ['doing'],
                    'doing': [],
                },
            ],
        },
    )

    assert edited == ''


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


def test_memory_generate_route_accepts_suggestions_without_user_instruct(monkeypatch):
    memory_generate_routes = _load_memory_generate_routes_module()
    app = FastAPI()
    app.include_router(memory_generate_routes.router)
    client = TestClient(app)

    def fake_generate_memory_content(**kwargs):
        assert kwargs['suggestions'] == [{
            'title': 'Update',
            'content': 'Apply change',
            'reason': None,
            'outdated': None,
        }]
        assert kwargs['user_instruct'] is None
        return 'new content'

    monkeypatch.setattr(
        memory_generate_routes,
        'generate_memory_content',
        fake_generate_memory_content,
    )

    response = client.post(
        '/api/chat/memory/generate',
        json={
            'content': 'old content',
            'suggestions': [{'title': 'Update', 'content': 'Apply change'}],
        },
    )

    assert response.status_code == 200
    assert response.json() == {'content': 'new content'}


def test_memory_generate_route_rejects_missing_suggestions_and_user_instruct():
    memory_generate_routes = _load_memory_generate_routes_module()
    app = FastAPI()
    app.include_router(memory_generate_routes.router)
    client = TestClient(app)

    response = client.post(
        '/api/chat/memory/generate',
        json={'content': 'old content'},
    )

    assert response.status_code == 422
