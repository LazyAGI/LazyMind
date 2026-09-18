from __future__ import annotations

from typing import get_args

import pytest

from lazymind.rewrite import RewriteTaskType, base


def test_rewrite_registry_exposes_supported_tasks():
    assert get_args(RewriteTaskType) == ('skill', 'polish', 'learning')
    assert set(base._PROMPT_BUILDERS) == {'skill', 'polish', 'learning'}


def test_learning_prompt_explicitly_excludes_skill_documents():
    prompt = base._PROMPT_BUILDERS['learning'](
        content='道路类型',
        user_instruct='返回中文 JSON 解释',
    )
    assert 'not a request to create a Skill' in prompt
    assert 'SKILL.md' in prompt
    assert '道路类型' in prompt
    assert set(base._EDIT_DISPATCH) == {'skill'}


def test_skill_rewrite_rejects_generated_non_string_required_metadata(monkeypatch):
    invalid_content = '---\nname: 123\ndescription: Example.\n---\nBody.\n'

    class FakeModel:
        def __call__(self, prompt):
            return {'content': invalid_content}

    monkeypatch.setattr(base, 'AutoModel', lambda model: FakeModel())
    monkeypatch.setitem(base._PROMPT_BUILDERS, 'skill', lambda **kwargs: 'prompt')

    with pytest.raises(
        base.UnprocessableContentError,
        match="field 'name' must be a string",
    ):
        base.rewrite_content('skill', 'old content', 'rewrite it')
