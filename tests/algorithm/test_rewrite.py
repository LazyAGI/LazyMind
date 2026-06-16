import importlib
import sys
from pathlib import Path
from types import ModuleType

import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient


def _load_rewrite_module():
    validation_path = (
        Path(__file__).resolve().parents[2]
        / 'algorithm/lazymind/chat/engine/tools/infra/user_preference_validation.py'
    )
    validation_spec = importlib.util.spec_from_file_location(
        'test_user_preference_validation',
        validation_path,
    )
    assert validation_spec is not None
    assert validation_spec.loader is not None
    user_preference_validation = importlib.util.module_from_spec(validation_spec)
    validation_spec.loader.exec_module(user_preference_validation)

    fake_lazyllm = ModuleType('lazyllm')
    fake_lazyllm.AutoModel = lambda *args, **kwargs: object()

    fake_tool_infra = ModuleType('lazymind.chat.engine.tools.infra')
    fake_tool_infra.parse_user_preference_frontmatter = (
        user_preference_validation.parse_user_preference_frontmatter
    )
    fake_tool_infra.validate_skill_content = lambda *_args, **_kwargs: None
    fake_tool_infra.validate_user_preference_content = (
        user_preference_validation.validate_user_preference_content
    )

    fake_load_config = ModuleType('lazymind.model_config')
    fake_load_config.get_config_path = lambda: ''

    original_modules = {
        'lazyllm': sys.modules.get('lazyllm'),
        'lazymind.chat.engine.tools.infra': sys.modules.get('lazymind.chat.engine.tools.infra'),
        'lazymind.model_config': sys.modules.get('lazymind.model_config'),
    }

    try:
        sys.modules['lazyllm'] = fake_lazyllm
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
        ns._compact_memory_to_recent_week = memory._compact_memory_to_recent_week
        ns._format_inputs_block = base._format_inputs_block
        ns.parse_user_preference_frontmatter = (
            user_preference_validation.parse_user_preference_frontmatter
        )
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
UnprocessableContentError = rewrite.UnprocessableContentError
_apply_memory_edit_operations = rewrite._apply_memory_edit_operations
_apply_user_preference_edit_operations = rewrite._apply_user_preference_edit_operations
_PROMPT_BUILDERS = rewrite._PROMPT_BUILDERS
_compact_memory_to_recent_week = rewrite._compact_memory_to_recent_week
_format_inputs_block = rewrite._format_inputs_block
parse_user_preference_frontmatter = rewrite.parse_user_preference_frontmatter
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
            task_type='memory',
            content='old content',
            user_instruct='  ',
        )
    except BadRequestError as exc:
        assert "'user_instruct' must be a non-empty string." == str(exc)
    else:
        raise AssertionError('Expected BadRequestError')


def test_generate_prompts_include_stale_content_governance():
    for task_type in ('skill', 'memory', 'user_preference'):
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
    assert 'user_address' in prompt
    assert 'response_style' in prompt
    assert 'legacy/free-form' in prompt
    assert 'frontmatter-plus-body format' in prompt
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
    assert 'only when user_instruct explicitly asks to change that specific field' in prompt


def test_user_preference_validation_requires_yaml_frontmatter():
    valid_english = (
        '---\n'
        'agent_persona: "algorithm collaborator"\n'
        'user_address: ""\n'
        'response_style: "concise"\n'
        '---\n'
        '- Prefer manual git commits.\n'
        '- Prefer algorithm-side changes only.'
    )
    valid_chinese = (
        '---\n'
        'agent_persona: "算法协作者"\n'
        'user_address: ""\n'
        'response_style: "简洁"\n'
        '---\n'
        '- 偏好手动提交 git。'
    )

    parsed, body = parse_user_preference_frontmatter(valid_english)
    assert parsed['agent_persona'] == 'algorithm collaborator'
    assert body == '- Prefer manual git commits.\n- Prefer algorithm-side changes only.'
    assert _validate_generated_content('user_preference', valid_english) == valid_english
    assert _validate_generated_content('user_preference', valid_chinese) == valid_chinese
    assert _validate_generated_content(
        'user_preference',
        (
            '---\n'
            'agent_persona: "algorithm collaborator"\n'
            'user_address: ""\n'
            'response_style: ""\n'
            '---\n'
            '- Prefer manual git commits.'
        ),
    )

    invalid_cases = [
        'agent_persona: "x"\nuser_address: ""\nresponse_style: "concise"\n\nbody',
        '---\nagent_persona: "x"\nresponse_style: "concise"\n---\nbody',
        '---\nagent_persona: "x"\nuser_address: ""\nresponse_style: "concise"\n---\n',
        '---\nagent_persona: "x"\nuser_address: ""\nresponse_style: "concise, direct"\n---\nbody',
        '---\nagent_persona: "x"\nuser_address: ""\nresponse_style: "轻松"\n---\nbody',
        '- not: a mapping',
    ]
    for invalid in invalid_cases:
        with pytest.raises(UnprocessableContentError):
            _validate_generated_content('user_preference', invalid)


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


def test_memory_compaction_keeps_recent_week_and_summarizes_older_records():
    older_days = []
    for day in range(1, 15):
        older_days.append(
            f'- 2026-05-{day:02d}\n'
            '  我们讨论了:\n'
            f'  - old topic {day} ' + ('detail ' * 20)
        )
    recent_days = (
        '- 2026-05-20\n'
        '  用户在做:\n'
        '  - recent task\n'
        '- 2026-05-21\n'
        '  状态/冲突:\n'
        '  - recent status'
    )

    compacted = _compact_memory_to_recent_week('\n'.join(older_days + [recent_days]))

    assert '一周前摘要' in compacted
    assert '2026-05-01' in compacted
    assert '- 2026-05-20' in compacted
    assert '- 2026-05-21' in compacted
    summary_line = next(line for line in compacted.splitlines() if '2026-05-01' in line)
    assert len(summary_line.strip()[2:]) <= 500


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
