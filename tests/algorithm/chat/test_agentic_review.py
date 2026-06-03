from __future__ import annotations

import os
import sys


_ALGO = os.path.join(os.path.dirname(__file__), '..', '..', '..', 'algorithm')
_LAZYLLM_ROOT = os.path.join(_ALGO, 'lazyllm')
if _ALGO not in sys.path:
    sys.path.insert(0, _ALGO)
if _LAZYLLM_ROOT not in sys.path:
    sys.path.insert(0, _LAZYLLM_ROOT)

for _module_name in list(sys.modules):
    if _module_name == 'lazyllm' or _module_name.startswith('lazyllm.'):
        del sys.modules[_module_name]

from lazymind.review.config import REVIEW_TOOLS  # noqa: E402
from lazymind.review.prompts import COMBINED_REVIEW_PROMPT  # noqa: E402
from lazymind.review.service import review as review_module  # noqa: E402
from lazymind.review.service.review import (  # noqa: E402
    _resolve_review_runtime_tools,
    _spawn_background_review,
)


def test_combined_review_uses_three_tools_and_single_choice_prompt():
    assert REVIEW_TOOLS['combined'] == ['memory_editor', 'skill_editor', 'vocab_learn']
    assert 'vocab_learn' in COMBINED_REVIEW_PROMPT
    assert 'exactly three tool choices' in COMBINED_REVIEW_PROMPT
    assert 'at most one' in COMBINED_REVIEW_PROMPT


def test_resolve_review_runtime_tools_builds_tool_groups():
    runtime_tools = _resolve_review_runtime_tools(['memory_editor', 'skill_editor', 'vocab_learn'])

    assert [tool.__name__ for tool in runtime_tools] == [
        'memory_editor',
        'skill_editor',
        'vocab_learn',
    ]


def test_spawn_background_review_passes_runtime_tool_instances(monkeypatch):
    captured = {}

    class _FakeAgent:
        def __init__(self, **kwargs):
            captured['tools'] = kwargs.get('tools')

        def __call__(self, prompt, llm_chat_history=None):
            captured['prompt'] = prompt
            captured['history'] = llm_chat_history
            return 'ok'

    monkeypatch.setattr(review_module, 'list_all_skills_with_category', lambda _skills_dir: {})
    monkeypatch.setattr(review_module.lazyllm.tools.agent, 'ReactAgent', _FakeAgent)
    monkeypatch.setattr(
        review_module,
        '_cfg',
        {
            'review_debug': True,
            'review_max_retries': 5,
            'skill_fs_url': 'remote://skills',
        },
    )

    _spawn_background_review(
        config={'session_id': 'sid-1', 'user_id': 'user-1'},
        llm=object(),
        keep_full_turns=2,
        history_snapshot=[],
        review_mode='memory',
        request_global_sid='sid-1',
    )

    assert [tool.__name__ for tool in captured['tools']] == ['memory_editor']
