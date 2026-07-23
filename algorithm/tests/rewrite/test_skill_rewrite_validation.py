from __future__ import annotations

from typing import get_args

import pytest

from lazymind.rewrite import RewriteTaskType, base


def test_rewrite_registry_only_exposes_skill_and_polish():
    assert get_args(RewriteTaskType) == ('skill', 'polish')
    assert set(base._PROMPT_BUILDERS) == {'skill', 'polish'}
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
