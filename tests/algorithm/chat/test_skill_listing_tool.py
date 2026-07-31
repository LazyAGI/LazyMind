from __future__ import annotations

from lazymind.chat.engine.tools.skill_listing import build_list_skills_tool


def test_list_skills_returns_normalized_available_skill_names():
    tool = build_list_skills_tool([
        'personal/bid-tech-proposal',
        ' personal/bid-tech-proposal ',
        '',
        'shared/research',
    ])

    assert tool.__name__ == 'list_skills'
    assert tool() == {
        'status': 'ok',
        'count': 2,
        'skills': ['personal/bid-tech-proposal', 'shared/research'],
    }


def test_list_skills_handles_an_empty_skill_library():
    tool = build_list_skills_tool(None)

    assert tool() == {'status': 'ok', 'count': 0, 'skills': []}
