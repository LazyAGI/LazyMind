import pytest

from lazymind.review.skill_review.miner import _normalize_candidate_payload
from lazymind.review.skill_review.schemas import SkillOutline


def _outline() -> SkillOutline:
    return SkillOutline(
        skill_name='valid-skill',
        applicable_scenario='Use this for a reusable workflow.',
        sop=[],
    )


def _content(name: str = 'valid-skill', description: str = 'Use this skill for reusable workflows.') -> str:
    return f"""---
name: {name}
description: {description}
---

# Valid Skill

Follow the reusable procedure.
"""


def _payload(**overrides):
    payload = {
        'skill_name': 'valid-skill',
        'category': 'general',
        'applicable_scenario': 'Use this for a reusable workflow.',
        'content': _content(),
    }
    payload.update(overrides)
    return payload


def test_normalize_candidate_payload_accepts_valid_skill_name_and_frontmatter():
    normalized = _normalize_candidate_payload(_payload(), _outline(), [], {})

    assert normalized['skill_name'] == 'valid-skill'
    assert normalized['content'].endswith('\n')


@pytest.mark.parametrize(
    'skill_name',
    [
        'Invalid-Skill',
        'invalid_skill',
        'invalid.skill',
        'invalid/skill',
        'invalid skill',
        '-invalid-skill',
        'invalid-skill-',
        'invalid--skill',
        '技能',
        'a' * 65,
    ],
)
def test_normalize_candidate_payload_rejects_illegal_skill_name(skill_name):
    with pytest.raises(ValueError, match='candidate skill_name'):
        _normalize_candidate_payload(
            _payload(skill_name=skill_name, content=_content(skill_name)),
            _outline(),
            [],
            {},
        )


def test_normalize_candidate_payload_rejects_frontmatter_name_mismatch():
    with pytest.raises(ValueError, match='must match content frontmatter name'):
        _normalize_candidate_payload(
            _payload(skill_name='valid-skill', content=_content('other-skill')),
            _outline(),
            [],
            {},
        )


def test_normalize_candidate_payload_rejects_missing_frontmatter_description():
    content = """---
name: valid-skill
---

# Valid Skill
"""

    with pytest.raises(ValueError, match='frontmatter must contain description'):
        _normalize_candidate_payload(
            _payload(content=content),
            _outline(),
            [],
            {},
        )
