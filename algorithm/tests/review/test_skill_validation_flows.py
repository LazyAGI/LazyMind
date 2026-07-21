from __future__ import annotations

import pytest
from pydantic import ValidationError

from lazymind.review.skill_organize.parser import parse_skill_summary
from lazymind.review.skill_organize.schemas import (
    SkillFsDraft,
    SkillFsDraftItem,
    SourceSkill,
)
from lazymind.review.skill_organize.validator import (
    validate_fs_draft,
    validate_source_skills,
)
from lazymind.review.service.skill_organize import _apply_fs_draft
from lazymind.review.service.skill_review import _apply_skill_review_record
from lazymind.review.skill_review.schemas import (
    CandidateSkillLLMOutput,
    SkillReviewResolution,
)


def test_review_candidate_requires_document_name_to_match_candidate_name():
    with pytest.raises(ValidationError, match='must match expected name'):
        CandidateSkillLLMOutput(
            skill_name='expected',
            applicable_scenario='Testing',
            content='---\nname: different\ndescription: Example.\n---\nBody.\n',
        )


def test_organize_source_identity_remains_storage_key_authoritative():
    source = SourceSkill(
        key='internal/stored-name',
        category='internal',
        name='stored-name',
        content=(
            '---\n'
            'name: stale-frontmatter-name\n'
            'description: Existing source.\n'
            'category: external\n'
            '---\n'
            '## Steps\n\n- Keep the stored identity.\n'
        ),
    )

    validate_source_skills([source])
    summary = parse_skill_summary(source)

    assert summary.key == 'internal/stored-name'
    assert summary.category == 'internal'
    assert summary.name == 'stored-name'
    assert summary.description == 'Existing source.'


def test_organize_summary_tolerates_malformed_historical_source_document():
    source = SourceSkill(
        key='internal/historical',
        category='internal',
        name='historical',
        content=(
            '---\n'
            'notes: |\n'
            '  ## Steps\n'
            '  - Do not parse this frontmatter list as a step.\n'
            'name: [broken\n'
            '---\n'
            '## Steps\n\n- Recover this step.\n'
        ),
    )

    summary = parse_skill_summary(source)

    assert summary.name == 'historical'
    assert summary.description == ''
    assert summary.core_steps == ['Recover this step.']


def test_organize_upsert_uses_target_key_name_and_ignores_frontmatter_category():
    source = SourceSkill(
        key='internal/source',
        category='internal',
        name='source',
        content='historical source content',
    )
    draft = SkillFsDraft(upsert_skills=[SkillFsDraftItem(
        source_key='internal/source',
        target_key='internal/target',
        content=(
            '---\n'
            'name: target\n'
            'description: Reorganized skill.\n'
            'category: external\n'
            '---\n'
            'Body.\n'
        ),
    )])

    validate_fs_draft(draft, [source])


def test_organize_upsert_rejects_frontmatter_name_mismatch():
    source = SourceSkill(
        key='internal/source',
        category='internal',
        name='source',
        content='historical source content',
    )
    draft = SkillFsDraft(upsert_skills=[SkillFsDraftItem(
        source_key='internal/source',
        target_key='internal/target',
        content='---\nname: different\ndescription: Invalid target.\n---\nBody.\n',
    )])

    with pytest.raises(ValueError, match='must match expected name'):
        validate_fs_draft(draft, [source])


def test_review_apply_rejects_invalid_document_before_accessing_store():
    record = SkillReviewResolution(
        id='resolution-1',
        skill_name='expected',
        type='new',
        skill_content='---\nname: different\ndescription: Invalid.\n---\nBody.\n',
    )

    with pytest.raises(ValueError, match='must match expected name'):
        _apply_skill_review_record(record, object())


def test_organize_apply_rejects_invalid_document_before_accessing_store():
    source = SourceSkill(
        key='internal/source',
        category='internal',
        name='source',
        content='historical source content',
    )
    draft = SkillFsDraft(upsert_skills=[SkillFsDraftItem(
        source_key='internal/source',
        target_key='internal/target',
        content='---\nname: different\ndescription: Invalid target.\n---\nBody.\n',
    )])

    with pytest.raises(ValueError, match='must match expected name'):
        _apply_fs_draft(draft, object(), [source])
