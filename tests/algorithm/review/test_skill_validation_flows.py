from __future__ import annotations

import pytest
from lazymind.review.service.skill_review import _apply_skill_review_record
from lazymind.review.skill_review.schemas import SkillReviewResolution


def test_review_apply_rejects_invalid_document_before_accessing_store():
    record = SkillReviewResolution(
        id='resolution-1',
        skill_name='expected',
        type='new',
        skill_content='---\nname: different\ndescription: Invalid.\n---\nBody.\n',
    )

    with pytest.raises(ValueError, match='must match expected name'):
        _apply_skill_review_record(record, object())
