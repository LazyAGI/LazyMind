from pathlib import Path

import pytest
from pydantic import ValidationError

from lazymind.common.skill.document import parse_skill_document
from lazymind.common.skill.remote_store import SkillRemoteStore
from lazymind.review.service.skill_organize import _apply_fs_draft, _load_source_skills
from lazymind.review.skill_organize import metadata_client
from lazymind.review.skill_organize.materializer import materialize_fs_draft
from lazymind.review.skill_organize.parser import parse_skill_summaries
from lazymind.review.skill_organize.schemas import SkillFsDraft, SkillOrganizePlan, SkillOrganizeRequest, SourceSkill
from lazymind.review.skill_organize.validator import validate_fs_draft, validate_plan

CONTENT = '---\nname: demo\ndescription: Before.\nlicense: MIT\n---\n\nRun `scripts/do.py`.  \n'


class LocalFS:
    def exists(self, path):
        return Path(path).exists()

    def ls(self, path, detail=True):
        return [dict(name=str(item), type='directory' if item.is_dir() else 'file')
                for item in Path(path).iterdir()]

    def open(self, path, *args, **kwargs):
        return open(path, *args, **kwargs)

    def write(self, path, content):
        Path(path).write_text(content)


@pytest.mark.parametrize('category', ['internal', 'external', 'search', 'design'])
def test_light_organizes_existing_categories_and_persists_only_description_and_sidecar(tmp_path, monkeypatch, category):
    key = f'{category}/demo'
    package = tmp_path / category / 'demo'
    package.mkdir(parents=True)
    (package / 'SKILL.md').write_text(CONTENT)
    (package / 'image.bin').write_bytes(b'\xff\x00\xfe')
    request = SkillOrganizeRequest(requestid='org', user_id='u', skills=[key], mode='light')
    store = SkillRemoteStore(fs=LocalFS(), existing_skill_keys=request.skills)
    store.root = str(tmp_path)
    sources = _load_source_skills(request, store)
    assert parse_skill_summaries(sources)[0].category == category
    plan = SkillOrganizePlan(plans=[dict(
        type='refactor', source_keys=[key], target_name='demo', target_description='After.',
        target_metadata={'field': 'coding', 'aliases': ['Demo alias']},
        step_handling_policy='keep_steps', reason='Clarify discovery',
    )])
    calls = []

    def post(path, payload):
        calls.append((path, payload))
        return {'response': {'data': {'skills': [dict(skill_key=key, aliases=['Before alias'])]}}}

    monkeypatch.setattr(metadata_client, 'post_core_api', post)
    assert metadata_client.load_search_metadata([key])[key]['aliases'] == ['Before alias']
    draft = materialize_fs_draft(plan, sources, None, mode='light')
    result = _apply_fs_draft(draft, store, sources, mode='light')
    assert result['upserted_keys'] == [key]
    assert sorted(item.relative_to(tmp_path).as_posix() for item in tmp_path.rglob('*') if item.is_file()) == [
        f'{category}/demo/SKILL.md', f'{category}/demo/image.bin',
    ]
    after = parse_skill_document((package / 'SKILL.md').read_text())
    assert after.body == '\nRun `scripts/do.py`.  \n'
    assert dict(after.metadata) == {'name': 'demo', 'description': 'After.', 'license': 'MIT'}
    assert (package / 'image.bin').read_bytes() == b'\xff\x00\xfe'
    assert calls[-1] == ('/internal/skills:metadata:update', {
        'updates': [dict(skill_key=key, field='coding', aliases=['Demo alias'])],
    })


@pytest.mark.parametrize('category', ['external', 'search', 'design'])
def test_deep_rejects_non_internal_requests(category):
    with pytest.raises(ValidationError, match='deep.*internal'):
        SkillOrganizeRequest(requestid='org', user_id='u', skills=[f'{category}/demo'], mode='deep')


@pytest.mark.parametrize('category', ['external', 'search'])
def test_deep_validators_reject_non_internal_sources_before_keep_or_empty_draft(category):
    key = f'{category}/demo'
    source = SourceSkill(key=key, category=category, name='demo', content=CONTENT)
    plan = SkillOrganizePlan(plans=[dict(type='keep', source_keys=[key], reason='Already clear')])
    with pytest.raises(ValueError, match='deep.*internal'):
        validate_plan(plan, [source], mode='deep')
    with pytest.raises(ValueError, match='deep.*internal'):
        validate_fs_draft(SkillFsDraft(), [source], mode='deep')


@pytest.mark.parametrize('key', ['../demo', 'search/..', 'search/a/b', 'bad category/demo', 'search/bad name'])
def test_light_rejects_unsafe_keys(key):
    with pytest.raises(ValidationError):
        SkillOrganizeRequest(requestid='org', user_id='u', skills=[key], mode='light')


def test_existing_key_opt_in_does_not_broaden_create_rename_or_unselected_packages(tmp_path):
    store = SkillRemoteStore(fs=LocalFS(), existing_skill_keys=['search/demo'])
    store.root = str(tmp_path)
    with pytest.raises(ValueError, match='internal.*external'):
        SkillRemoteStore(fs=LocalFS()).package_dir('search', 'demo')
    with pytest.raises(ValueError, match='internal.*external'):
        store.package_dir('search', 'unselected')
    with pytest.raises(ValueError, match='internal.*external'):
        store.create('search', 'demo', CONTENT)
    with pytest.raises(ValueError, match='internal.*external'):
        store.rename('search', 'demo', 'search', 'renamed', skill_content=CONTENT)
    with pytest.raises(FileNotFoundError):
        store.replace_files('search', 'demo', {}, {'SKILL.md': CONTENT})
    assert list(tmp_path.iterdir()) == []
