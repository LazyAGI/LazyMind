from __future__ import annotations

import json

import pytest
from pydantic import ValidationError

from lazymind.review.skill_organize.parser import parse_skill_summaries
from lazymind.review.skill_organize.planner import build_organize_plan
from lazymind.review.skill_organize.materializer import materialize_fs_draft
from lazymind.review.skill_organize.schemas import (
    SkillFsDraft,
    SkillFsDraftItem,
    SkillOrganizePlan,
    SkillOrganizeRequest,
    SkillPlan,
    SourceSkill,
)
from lazymind.review.skill_organize.validator import validate_plan
from lazymind.review.service.skill_organize import _apply_fs_draft, _load_source_skills


class _FakeFS:
    def __init__(self, store):
        self.store = store

    def exists(self, path):
        return path in {
            self.store.package_dir(category, name)
            for category, name in self.store.packages
        }


class _FakeStore:
    def __init__(self, packages):
        self.packages = {
            key: dict(files)
            for key, files in packages.items()
        }
        self.calls = []
        self.fs = _FakeFS(self)

    def package_dir(self, category, name):
        return f'remote://skills/{category}/{name}'

    def list_files(self, category, name):
        self.calls.append(('list_files', category, name))
        return dict(self.packages[(category, name)])

    def replace_files(self, category, name, before, after):
        self.calls.append(('replace_files', category, name, before, after))
        self.packages[(category, name)] = dict(after)
        return {'written': ['SKILL.md'], 'deleted': []}

    def rename(self, old_category, old_name, new_category, new_name, *, skill_content):
        self.calls.append((
            'rename',
            old_category,
            old_name,
            new_category,
            new_name,
            skill_content,
        ))
        files = self.packages.pop((old_category, old_name))
        files['SKILL.md'] = skill_content
        self.packages[(new_category, new_name)] = files
        return {'action': 'rename'}

    def remove(self, category, name):
        self.calls.append(('remove', category, name))
        self.packages.pop((category, name))
        return {'action': 'remove'}


def test_skill_organize_request_rejects_unsupported_storage_category():
    with pytest.raises(ValidationError, match='internal.*external'):
        SkillOrganizeRequest(
            requestid='org-category',
            user_id='user-1',
            skills=['research/demo'],
        )


def test_source_skill_uses_request_key_as_storage_identity():
    class Store:
        def list_files(self, category, name):
            assert (category, name) == ('internal', 'demo')
            return {
                'SKILL.md': (
                    '---\n'
                    'name: ignored-frontmatter-name\n'
                    'category: research\n'
                    'description: Legacy content metadata.\n'
                    '---\n'
                    'Use this skill for a demo.\n'
                )
            }

    request = SkillOrganizeRequest(
        requestid='org-category',
        user_id='user-1',
        skills=['internal/demo'],
    )

    source = _load_source_skills(request, Store())[0]

    assert source.key == 'internal/demo'
    assert source.category == 'internal'
    assert source.name == 'demo'
    assert source.model_dump()['category'] == 'internal'


def test_planner_uses_full_keys_and_target_source_instead_of_model_category():
    sources = [
        SourceSkill(
            key='internal/alpha',
            category='internal',
            name='alpha',
            content=(
                '---\nname: alpha\ndescription: Alpha workflow.\n---\n'
                'Use alpha.\n'
            ),
        ),
        SourceSkill(
            key='external/beta',
            category='external',
            name='beta',
            content=(
                '---\nname: beta\ndescription: Beta workflow.\n---\n'
                'Use beta.\n'
            ),
        ),
    ]

    class LLM:
        prompt = ''

        def __call__(self, prompt, **_kwargs):
            self.prompt = prompt
            return json.dumps({
                'plans': [{
                    'type': 'merge',
                    'source_keys': ['internal/alpha', 'external/beta'],
                    'target_source_key': 'external/beta',
                    'target_name': 'merged-workflow',
                    'target_description': 'Use for the merged workflow.',
                    'step_handling_policy': 'merge_and_deduplicate_existing_steps',
                    'reason': 'The workflows have the same reusable boundary.',
                }],
            })

    llm = LLM()
    plan = build_organize_plan(parse_skill_summaries(sources), sources, llm)

    assert plan.plans[0].source_keys == ['internal/alpha', 'external/beta']
    assert plan.plans[0].target_source_key == 'external/beta'
    assert plan.plans[0].target_name == 'merged-workflow'
    assert 'target_category' not in plan.plans[0].model_dump()
    assert 'target_category' not in llm.prompt


@pytest.mark.parametrize(
    ('target_source_key', 'expected_target_key', 'expected_delete_keys'),
    [
        ('internal/alpha', 'internal/merged-workflow', ['external/beta']),
        ('external/beta', 'external/merged-workflow', ['internal/alpha']),
    ],
)
def test_materializer_derives_merge_target_category_from_target_source_key(
    target_source_key,
    expected_target_key,
    expected_delete_keys,
):
    sources = [
        SourceSkill(
            key='internal/alpha',
            category='internal',
            name='alpha',
            content='---\nname: alpha\ndescription: Alpha.\n---\nUse alpha.\n',
        ),
        SourceSkill(
            key='external/beta',
            category='external',
            name='beta',
            content='---\nname: beta\ndescription: Beta.\n---\nUse beta.\n',
        ),
    ]
    plan = SkillOrganizePlan(plans=[SkillPlan(
        type='merge',
        source_keys=['internal/alpha', 'external/beta'],
        target_source_key=target_source_key,
        target_name='merged-workflow',
        target_description='Use for the merged workflow.',
        step_handling_policy='merge_and_deduplicate_existing_steps',
        reason='The workflows share one boundary.',
    )])
    materialized_content = (
        '---\n'
        'name: merged-workflow\n'
        'category: research\n'
        'description: Merged workflow.\n'
        '---\n'
        'Use this merged workflow.\n'
    )

    class LLM:
        def __call__(self, _prompt, **_kwargs):
            return json.dumps({'content': materialized_content})

    draft = materialize_fs_draft(plan, sources, LLM(), max_workers=1)

    assert draft.delete_keys == expected_delete_keys
    assert len(draft.upsert_skills) == 1
    assert draft.upsert_skills[0].source_key == target_source_key
    assert draft.upsert_skills[0].target_key == expected_target_key
    assert draft.upsert_skills[0].content == materialized_content


def test_apply_same_key_replaces_skill_md_and_preserves_package_files():
    old_content = '---\nname: beta\ndescription: Old.\n---\nOld body.\n'
    new_content = (
        '---\n'
        'name: beta\n'
        'category: research\n'
        'description: Updated.\n'
        '---\n'
        'Updated body.\n'
    )
    store = _FakeStore({
        ('external', 'beta'): {
            'SKILL.md': old_content,
            'assets/example.txt': 'supporting file',
        },
    })
    source = SourceSkill(
        key='external/beta',
        category='external',
        name='beta',
        content=old_content,
    )
    draft = SkillFsDraft(upsert_skills=[SkillFsDraftItem(
        source_key='external/beta',
        target_key='external/beta',
        content=new_content,
    )])

    result = _apply_fs_draft(draft, store, [source])

    assert result == {
        'deleted_keys': [],
        'upserted_keys': ['external/beta'],
    }
    assert store.packages[('external', 'beta')] == {
        'SKILL.md': new_content,
        'assets/example.txt': 'supporting file',
    }


def test_apply_cross_category_merge_keeps_target_source_category_and_package():
    alpha_content = '---\nname: alpha\ndescription: Alpha.\n---\nAlpha.\n'
    beta_content = '---\nname: beta\ndescription: Beta.\n---\nBeta.\n'
    merged_content = '---\nname: merged\ndescription: Merged.\n---\nMerged.\n'
    store = _FakeStore({
        ('internal', 'alpha'): {
            'SKILL.md': alpha_content,
            'references/alpha.md': 'alpha reference',
        },
        ('external', 'beta'): {
            'SKILL.md': beta_content,
            'references/beta.md': 'beta reference',
        },
    })
    sources = [
        SourceSkill(
            key='internal/alpha',
            category='internal',
            name='alpha',
            content=alpha_content,
        ),
        SourceSkill(
            key='external/beta',
            category='external',
            name='beta',
            content=beta_content,
        ),
    ]
    draft = SkillFsDraft(
        delete_keys=['internal/alpha'],
        upsert_skills=[SkillFsDraftItem(
            source_key='external/beta',
            target_key='external/merged',
            content=merged_content,
        )],
    )

    result = _apply_fs_draft(draft, store, sources)

    assert result == {
        'deleted_keys': ['internal/alpha'],
        'upserted_keys': ['external/merged'],
    }
    assert ('internal', 'alpha') not in store.packages
    assert ('external', 'beta') not in store.packages
    assert store.packages[('external', 'merged')] == {
        'SKILL.md': merged_content,
        'references/beta.md': 'beta reference',
    }
    assert [call[0] for call in store.calls if call[0] in {'rename', 'remove'}] == [
        'rename',
        'remove',
    ]


def test_refactor_derives_source_and_category_from_its_only_source_key():
    source = SourceSkill(
        key='internal/alpha',
        category='internal',
        name='alpha',
        content='---\nname: alpha\ndescription: Alpha.\n---\nAlpha.\n',
    )
    plan = SkillOrganizePlan(plans=[SkillPlan(
        type='refactor',
        source_keys=['internal/alpha'],
        target_name='alpha-refined',
        target_description='Use for the refined alpha workflow.',
        step_handling_policy='keep_steps',
        reason='The description needs a clearer boundary.',
    )])

    validate_plan(plan, [source])

    class LLM:
        def __call__(self, _prompt, **_kwargs):
            return json.dumps({
                'content': (
                    '---\nname: alpha-refined\ndescription: Refined alpha.\n---\n'
                    'Use refined alpha.\n'
                ),
            })

    draft = materialize_fs_draft(plan, [source], LLM(), max_workers=1)

    assert draft.delete_keys == []
    assert draft.upsert_skills[0].source_key == 'internal/alpha'
    assert draft.upsert_skills[0].target_key == 'internal/alpha-refined'


def test_apply_preflights_all_collisions_before_first_write():
    source_a_content = '---\nname: source-a\ndescription: A.\n---\nA.\n'
    source_b_content = '---\nname: source-b\ndescription: B.\n---\nB.\n'
    store = _FakeStore({
        ('internal', 'source-a'): {'SKILL.md': source_a_content},
        ('internal', 'source-b'): {'SKILL.md': source_b_content},
        ('internal', 'taken'): {
            'SKILL.md': '---\nname: taken\ndescription: Taken.\n---\nTaken.\n',
        },
    })
    sources = [
        SourceSkill(
            key='internal/source-a',
            category='internal',
            name='source-a',
            content=source_a_content,
        ),
        SourceSkill(
            key='internal/source-b',
            category='internal',
            name='source-b',
            content=source_b_content,
        ),
    ]
    draft = SkillFsDraft(upsert_skills=[
        SkillFsDraftItem(
            source_key='internal/source-a',
            target_key='internal/source-a',
            content='---\nname: source-a\ndescription: Updated.\n---\nUpdated.\n',
        ),
        SkillFsDraftItem(
            source_key='internal/source-b',
            target_key='internal/taken',
            content='---\nname: taken\ndescription: Collision.\n---\nCollision.\n',
        ),
    ])

    with pytest.raises(FileExistsError, match='internal/taken'):
        _apply_fs_draft(draft, store, sources)

    assert not any(
        call[0] in {'replace_files', 'rename', 'remove'}
        for call in store.calls
    )


def test_apply_preloads_same_key_packages_before_any_rename():
    source_a_content = '---\nname: source-a\ndescription: A.\n---\nA.\n'
    source_b_content = '---\nname: source-b\ndescription: B.\n---\nB.\n'

    class UnreadableStore(_FakeStore):
        def list_files(self, category, name):
            self.calls.append(('list_files', category, name))
            if (category, name) == ('internal', 'source-b'):
                raise RuntimeError('source-b package is unreadable')
            return dict(self.packages[(category, name)])

    store = UnreadableStore({
        ('internal', 'source-a'): {'SKILL.md': source_a_content},
        ('internal', 'source-b'): {'SKILL.md': source_b_content},
    })
    sources = [
        SourceSkill(
            key='internal/source-a',
            category='internal',
            name='source-a',
            content=source_a_content,
        ),
        SourceSkill(
            key='internal/source-b',
            category='internal',
            name='source-b',
            content=source_b_content,
        ),
    ]
    draft = SkillFsDraft(upsert_skills=[
        SkillFsDraftItem(
            source_key='internal/source-a',
            target_key='internal/source-a-renamed',
            content=(
                '---\nname: source-a-renamed\ndescription: Renamed A.\n---\n'
                'Renamed A.\n'
            ),
        ),
        SkillFsDraftItem(
            source_key='internal/source-b',
            target_key='internal/source-b',
            content=(
                '---\nname: source-b\ndescription: Updated B.\n---\n'
                'Updated B.\n'
            ),
        ),
    ])

    with pytest.raises(RuntimeError, match='source-b package is unreadable'):
        _apply_fs_draft(draft, store, sources)

    assert set(store.packages) == {
        ('internal', 'source-a'),
        ('internal', 'source-b'),
    }
    assert not any(
        call[0] in {'replace_files', 'rename', 'remove'}
        for call in store.calls
    )


def test_plan_distinguishes_same_name_in_internal_and_external_categories():
    sources = [
        SourceSkill(
            key='internal/shared',
            category='internal',
            name='shared',
            content='---\nname: shared\ndescription: Internal.\n---\nInternal.\n',
        ),
        SourceSkill(
            key='external/shared',
            category='external',
            name='shared',
            content='---\nname: shared\ndescription: External.\n---\nExternal.\n',
        ),
    ]
    plan = SkillOrganizePlan(plans=[SkillPlan(
        type='merge',
        source_keys=['internal/shared', 'external/shared'],
        target_source_key='external/shared',
        target_name='shared-merged',
        target_description='Use for the shared merged workflow.',
        step_handling_policy='merge_and_deduplicate_existing_steps',
        reason='The two storage keys contain the same workflow.',
    )])

    validate_plan(plan, sources)


def test_merge_rejects_target_source_key_outside_its_sources():
    sources = [
        SourceSkill(
            key='internal/alpha',
            category='internal',
            name='alpha',
            content='---\nname: alpha\ndescription: Alpha.\n---\nAlpha.\n',
        ),
        SourceSkill(
            key='external/beta',
            category='external',
            name='beta',
            content='---\nname: beta\ndescription: Beta.\n---\nBeta.\n',
        ),
    ]
    plan = SkillOrganizePlan(plans=[SkillPlan(
        type='merge',
        source_keys=['internal/alpha', 'external/beta'],
        target_source_key='internal/missing',
        target_name='merged',
        target_description='Use for the merged workflow.',
        step_handling_policy='merge_and_deduplicate_existing_steps',
        reason='The workflows overlap.',
    )])

    with pytest.raises(ValueError, match='target_source_key must be one of source_keys'):
        validate_plan(plan, sources)


def test_plan_schema_rejects_removed_target_category_field():
    with pytest.raises(ValidationError, match='target_category'):
        SkillPlan.model_validate({
            'type': 'refactor',
            'source_keys': ['internal/alpha'],
            'target_name': 'alpha-refined',
            'target_category': 'external',
            'target_description': 'Use for refined alpha.',
            'step_handling_policy': 'keep_steps',
            'reason': 'Clarify the reusable boundary.',
        })


def test_apply_rejects_frontmatter_name_mismatch_before_writing():
    old_content = '---\nname: source\ndescription: Source.\n---\nSource.\n'
    store = _FakeStore({
        ('internal', 'source'): {'SKILL.md': old_content},
    })
    source = SourceSkill(
        key='internal/source',
        category='internal',
        name='source',
        content=old_content,
    )
    draft = SkillFsDraft(upsert_skills=[SkillFsDraftItem(
        source_key='internal/source',
        target_key='internal/source',
        content='---\nname: wrong\ndescription: Wrong.\n---\nWrong.\n',
    )])

    with pytest.raises(ValueError, match='frontmatter name .* must match expected name'):
        _apply_fs_draft(draft, store, [source])

    assert not any(
        call[0] in {'replace_files', 'rename', 'remove'}
        for call in store.calls
    )
