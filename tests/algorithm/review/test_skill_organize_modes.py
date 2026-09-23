from __future__ import annotations

import pytest
from pydantic import ValidationError

from lazymind.common.skill.document import parse_skill_document
from lazymind.review.skill_organize.schemas import (
    SkillOrganizeRequest, SkillOrganizePlan, SourceSkill, SkillFsDraft, SkillFsDraftItem,
)
from lazymind.review.skill_organize.validator import validate_plan, validate_fs_draft
from lazymind.review.skill_organize.materializer import materialize_fs_draft
from lazymind.review.service.skill_organize import _apply_fs_draft, _with_evolution_or_chat_llm
from test_skill_organize_category import _FakeStore

CONTENT = '---\nname: demo\ndescription: Before.\nlicense: MIT\n---\n\n## Steps\nRun `scripts/do.py`.  \n'
SOURCE = SourceSkill(key='internal/demo', category='internal', name='demo', content=CONTENT)


def test_with_evolution_or_chat_llm_prefers_evo():
    configs = _with_evolution_or_chat_llm({
        'llm': {'source': 'deepseek', 'model': 'deepseek-v4-flash'},
        'evo_llm': {'source': 'openai', 'model': 'Qwen/Qwen3.8-Flash-Next', 'base_url': 'http://example/v1/'},
    })
    assert configs['llm']['model'] == 'Qwen/Qwen3.8-Flash-Next'
    assert configs['llm']['source'] == 'openai'


def test_with_evolution_or_chat_llm_keeps_chat_when_evo_missing():
    configs = _with_evolution_or_chat_llm({
        'llm': {'source': 'deepseek', 'model': 'deepseek-v4-flash'},
    })
    assert configs['llm']['model'] == 'deepseek-v4-flash'


def test_request_defaults_to_light_and_rejects_unknown_mode():
    args = dict(requestid='org-1', user_id='u', skills=['internal/demo'])
    assert SkillOrganizeRequest(**args).mode == 'light'
    with pytest.raises(ValidationError):
        SkillOrganizeRequest(**args, mode='unsafe')


@pytest.mark.parametrize('kind', ['merge', 'delete_duplicate'])
def test_light_rejects_destructive_plan(kind):
    plan = SkillOrganizePlan(plans=[dict(type=kind, source_keys=[SOURCE.key], reason='overlap')])
    with pytest.raises(ValueError, match='light'):
        validate_plan(plan, [SOURCE], mode='light')


def test_light_materializes_metadata_without_changing_body_or_files(monkeypatch):
    plan = SkillOrganizePlan(plans=[dict(
        type='refactor', source_keys=[SOURCE.key], target_name='demo', target_description='After.',
        target_metadata={'field': 'coding', 'tags': ['python'], 'aliases': ['演示'], 'keywords': ['script']},
        step_handling_policy='keep_steps', reason='Clarify',
    )])
    draft = materialize_fs_draft(plan, [SOURCE], None, mode='light')
    updated = parse_skill_document(draft.upsert_skills[0].content)
    assert updated.body == parse_skill_document(CONTENT).body
    assert updated.metadata['name'] == 'demo'
    assert updated.metadata['license'] == 'MIT'
    assert 'field' not in updated.metadata
    assert 'aliases' not in updated.metadata
    assert draft.upsert_skills[0].search_metadata.field == 'coding'
    assert draft.upsert_skills[0].search_metadata.aliases == ['演示']
    sent = []
    monkeypatch.setattr(
        'lazymind.review.service.skill_organize.update_search_metadata', lambda updates: sent.extend(updates),
    )
    assert updated.metadata['description'] == 'After.'
    files = {'SKILL.md': CONTENT, 'scripts/do.py': 'print(1)\n', 'assets/image.png': b'\xff\x00\xfe'}
    store = _FakeStore({('internal', 'demo'): files})
    _apply_fs_draft(draft, store, [SOURCE], mode='light')
    assert store.packages[('internal', 'demo')]['assets/image.png'] == files['assets/image.png']
    assert store.packages[('internal', 'demo')]['scripts/do.py'] == files['scripts/do.py']
    assert sent == [dict(
        skill_key=SOURCE.key, field='coding', tags=['python'], aliases=['演示'], keywords=['script'],
    )]


@pytest.mark.parametrize('change', ['body', 'name', 'license', 'delete'])
def test_light_apply_rejects_untrusted_draft_before_any_write(change):
    updated = CONTENT.replace('Before.', 'After.')
    key = SOURCE.key
    if change == 'body':
        updated += 'New procedure.\n'
    elif change == 'name':
        key = 'internal/renamed'
        updated = updated.replace('name: demo', 'name: renamed')
    elif change == 'license':
        updated = updated.replace('MIT', 'GPL')
    draft = SkillFsDraft(delete_keys=[SOURCE.key]) if change == 'delete' else SkillFsDraft(upsert_skills=[
        SkillFsDraftItem(source_key=SOURCE.key, target_key=key, content=updated),
    ])
    store = _FakeStore({('internal', 'demo'): {'SKILL.md': CONTENT}})
    with pytest.raises(ValueError, match='light'):
        _apply_fs_draft(draft, store, [SOURCE], mode='light')
    assert store.calls == []


def test_deep_preserves_refactor_and_delete_support():
    updated = CONTENT + 'Refactored procedure.\n'
    validate_fs_draft(SkillFsDraft(upsert_skills=[
        SkillFsDraftItem(source_key=SOURCE.key, target_key=SOURCE.key, content=updated),
    ]), [SOURCE], mode='deep')
    validate_fs_draft(SkillFsDraft(delete_keys=[SOURCE.key]), [SOURCE], mode='deep')


def test_light_rejects_a_source_changed_after_planning():
    draft = SkillFsDraft(upsert_skills=[SkillFsDraftItem(
        source_key=SOURCE.key, target_key=SOURCE.key, content=CONTENT.replace('Before.', 'After.'),
    )])
    current = CONTENT + 'User added a step while organization ran.\n'
    store = _FakeStore({('internal', 'demo'): {'SKILL.md': current}})
    with pytest.raises(ValueError, match='source changed'):
        _apply_fs_draft(draft, store, [SOURCE], mode='light')
    assert store.packages[('internal', 'demo')]['SKILL.md'] == current


def test_service_passes_mode_through_every_stage(monkeypatch):
    from lazymind.review.service import skill_organize as service
    recorded = []
    request = SkillOrganizeRequest(requestid='org-1', user_id='u', skills=[SOURCE.key], mode='deep')
    plan = SkillOrganizePlan(plans=[dict(type='keep', source_keys=[SOURCE.key], reason='Already clear')])
    monkeypatch.setattr(service, '_load_source_skills', lambda *_: [SOURCE])
    monkeypatch.setattr(service, 'load_search_metadata', lambda _: {SOURCE.key: {'field': 'writing'}})
    monkeypatch.setattr(service, 'write_stage_file', lambda *_: None)
    monkeypatch.setattr(service, '_record_skill_organize_stage_safely', lambda *_: None)
    monkeypatch.setattr(service, 'insert_skill_organize_result', lambda **_: 1)

    def build(*args, mode):
        recorded.append(('plan', mode))
        return plan

    def materialize(*args, mode):
        recorded.append(('draft', mode))
        return SkillFsDraft()

    def apply(*args, mode):
        recorded.append(('apply', mode))
        return {}

    monkeypatch.setattr(service, 'build_organize_plan', build)
    monkeypatch.setattr(service, 'materialize_fs_draft', materialize)
    monkeypatch.setattr(service, '_apply_fs_draft', apply)
    result = service._run_skill_organize(request, None, taskid='task-1', remote_store=None)
    assert result.success
    assert recorded == [('plan', 'deep'), ('draft', 'deep'), ('apply', 'deep')]


def test_light_keeps_imported_frontmatter_but_plans_from_system_metadata():
    from lazymind.review.skill_organize.parser import parse_skill_summary
    content = CONTENT.replace('license: MIT', 'license: MIT\nfield: old-domain\naliases: [original]')
    source = SourceSkill(key=SOURCE.key, category=SOURCE.category, name=SOURCE.name, content=content,
                         search_metadata={'field': 'system-domain', 'aliases': ['UI alias']})
    assert parse_skill_summary(source).search_metadata['aliases'] == ['UI alias']
    plan = SkillOrganizePlan(plans=[dict(
        type='refactor', source_keys=[source.key], target_name='demo',
        target_metadata={'field': 'new-domain', 'aliases': ['new alias']},
        step_handling_policy='keep_steps', reason='Metadata cleanup')])
    draft = materialize_fs_draft(plan, [source], None, mode='light')
    assert draft.upsert_skills[0].content == content
    assert draft.upsert_skills[0].search_metadata.field == 'new-domain'


def test_light_rejects_generated_search_fields_inside_l2():
    content = CONTENT.replace('license: MIT', 'license: MIT\nkeywords: [generated]')
    draft = SkillFsDraft(upsert_skills=[SkillFsDraftItem(
        source_key=SOURCE.key, target_key=SOURCE.key, content=content,
    )])
    with pytest.raises(ValueError, match='light'):
        validate_fs_draft(draft, [SOURCE], mode='light')


def test_deep_materializer_keeps_source_l2_metadata_separate_from_system_updates():
    import json
    content = CONTENT.replace('license: MIT', 'license: MIT\ntags: [imported]')
    source = SourceSkill(key=SOURCE.key, category=SOURCE.category, name=SOURCE.name, content=content)
    plan = SkillOrganizePlan(plans=[dict(
        type='refactor', source_keys=[source.key], target_name='demo', target_description='After.',
        target_metadata={'tags': ['system'], 'keywords': ['new-keyword']},
        step_handling_policy='keep_steps', reason='Clarify',
    )])

    def llm(*args, **kwargs):
        return json.dumps({'content': content.replace('imported', 'system').replace(
            'license: MIT', 'license: MIT\nkeywords: [new-keyword]',
        )})

    draft = materialize_fs_draft(plan, [source], llm, mode='deep')
    document = parse_skill_document(draft.upsert_skills[0].content)
    assert document.metadata['tags'] == ('imported',)
    assert 'keywords' not in document.metadata
    assert draft.upsert_skills[0].search_metadata.tags == ['system']


def test_search_metadata_client_uses_trusted_separate_api(monkeypatch):
    from lazymind.review.skill_organize import metadata_client
    calls = []

    def post(path, payload):
        calls.append((path, payload))
        return {'response': {'data': {'skills': [dict(skill_key=SOURCE.key, field='coding',
                tags=['db'], aliases=['UI alias'], keywords=[])]}}}

    monkeypatch.setattr(metadata_client, 'post_core_api', post)
    loaded = metadata_client.load_search_metadata([SOURCE.key])
    assert loaded[SOURCE.key]['aliases'] == ['UI alias']
    updates = [dict(skill_key=SOURCE.key, field='writing', aliases=[])]
    metadata_client.update_search_metadata(updates)
    assert calls == [('/internal/skills:metadata', {'skill_keys': [SOURCE.key]}),
                     ('/internal/skills:metadata:update', {'updates': updates})]
