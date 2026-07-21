from __future__ import annotations

import pytest

from lazymind.rewrite import base


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
