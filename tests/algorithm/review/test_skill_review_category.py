from __future__ import annotations

from concurrent.futures import ThreadPoolExecutor
import importlib.util
from pathlib import Path
import sys
from types import ModuleType, SimpleNamespace

import pytest


_ALGO = Path(__file__).resolve().parents[3] / 'algorithm'


def _package(name: str) -> ModuleType:
    module = ModuleType(name)
    module.__path__ = []
    return module


def _module(name: str, **attrs) -> ModuleType:
    module = ModuleType(name)
    for key, value in attrs.items():
        setattr(module, key, value)
    return module


def _load_module(module_name: str, relative_path: str):
    path = _ALGO / relative_path
    spec = importlib.util.spec_from_file_location(module_name, path)
    assert spec is not None
    assert spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    sys.modules[module_name] = module
    spec.loader.exec_module(module)
    return module


def _load_skill_review_modules():
    module_names = [
        'lazyllm',
        'lazyllm.tools',
        'lazyllm.tools.agent',
        'lazyllm.tools.agent.skill_manager',
        'lazymind',
        'lazymind.chat',
        'lazymind.chat.engine',
        'lazymind.chat.engine.tools',
        'lazymind.chat.engine.tools.infra',
        'lazymind.common',
        'lazymind.common.integrations',
        'lazymind.common.integrations.remote_fs',
        'lazymind.common.skill_document',
        'lazymind.common.skill_remote_store',
        'lazymind.common.skill_storage_key',
        'lazymind.config',
        'lazymind.model_config',
        'lazymind.review',
        'lazymind.review.service',
        'lazymind.review.service.skill_review',
        'lazymind.review.skill_review',
        'lazymind.review.skill_review.cluster',
        'lazymind.review.skill_review.config',
        'lazymind.review.skill_review.db',
        'lazymind.review.skill_review.draft',
        'lazymind.review.skill_review.json_call',
        'lazymind.review.skill_review.miner',
        'lazymind.review.skill_review.prompt',
        'lazymind.review.skill_review.reports',
        'lazymind.review.skill_review.resolution',
        'lazymind.review.skill_review.schemas',
        'lazymind.review.skill_review.trajectory',
    ]
    originals = {name: sys.modules.get(name) for name in module_names}

    log = SimpleNamespace(
        info=lambda *_args, **_kwargs: None,
        warning=lambda *_args, **_kwargs: None,
        error=lambda *_args, **_kwargs: None,
        exception=lambda *_args, **_kwargs: None,
    )
    fake_lazyllm = _module(
        'lazyllm',
        AutoModel=object,
        LOG=log,
        ThreadPoolExecutor=ThreadPoolExecutor,
        globals={},
    )
    fake_modules = {
        'lazyllm': fake_lazyllm,
        'lazyllm.tools': _package('lazyllm.tools'),
        'lazyllm.tools.agent': _package('lazyllm.tools.agent'),
        'lazyllm.tools.agent.skill_manager': _module(
            'lazyllm.tools.agent.skill_manager', SkillManager=object
        ),
        'lazymind': _package('lazymind'),
        'lazymind.chat': _package('lazymind.chat'),
        'lazymind.chat.engine': _package('lazymind.chat.engine'),
        'lazymind.chat.engine.tools': _package('lazymind.chat.engine.tools'),
        'lazymind.chat.engine.tools.infra': _package('lazymind.chat.engine.tools.infra'),
        'lazymind.common': _package('lazymind.common'),
        'lazymind.common.integrations': _package('lazymind.common.integrations'),
        'lazymind.common.integrations.remote_fs': _module(
            'lazymind.common.integrations.remote_fs', RemoteFS=object
        ),
        'lazymind.common.skill_remote_store': _module(
            'lazymind.common.skill_remote_store', SkillRemoteStore=object
        ),
        'lazymind.config': _module(
            'lazymind.config',
            config={'skill_fs_url': 'remote://skills', 'core_api_url': 'http://core'},
        ),
        'lazymind.model_config': _module(
            'lazymind.model_config', inject_model_config=lambda _config: None
        ),
        'lazymind.review': _package('lazymind.review'),
        'lazymind.review.service': _package('lazymind.review.service'),
        'lazymind.review.skill_review': _package('lazymind.review.skill_review'),
        'lazymind.review.skill_review.cluster': _module(
            'lazymind.review.skill_review.cluster', cluster_drafts=lambda *_args, **_kwargs: []
        ),
        'lazymind.review.skill_review.db': _module(
            'lazymind.review.skill_review.db',
            insert_skill_review_run_stats=lambda *_args, **_kwargs: None,
            read_session=lambda *_args, **_kwargs: [],
        ),
        'lazymind.review.skill_review.draft': _module(
            'lazymind.review.skill_review.draft', build_skill_drafts=lambda *_args, **_kwargs: ([], {})
        ),
        'lazymind.review.skill_review.json_call': _module(
            'lazymind.review.skill_review.json_call', call_json=lambda *_args, **_kwargs: {}
        ),
        'lazymind.review.skill_review.resolution': _module(
            'lazymind.review.skill_review.resolution', resolve_skill_actions=lambda *_args, **_kwargs: ([], {})
        ),
        'lazymind.review.skill_review.trajectory': _module(
            'lazymind.review.skill_review.trajectory', build_trajectories=lambda *_args, **_kwargs: ([], {})
        ),
    }

    try:
        sys.modules.update(fake_modules)
        _load_module(
            'lazymind.common.skill_document',
            'lazymind/common/skill_document.py',
        )
        _load_module(
            'lazymind.common.skill_storage_key',
            'lazymind/common/skill_storage_key.py',
        )
        config = _load_module(
            'lazymind.review.skill_review.config',
            'lazymind/review/skill_review/config.py',
        )
        schemas = _load_module(
            'lazymind.review.skill_review.schemas',
            'lazymind/review/skill_review/schemas.py',
        )
        json_call = _load_module(
            'lazymind.review.skill_review.json_call',
            'lazymind/review/skill_review/json_call.py',
        )
        prompt = _load_module(
            'lazymind.review.skill_review.prompt',
            'lazymind/review/skill_review/prompt.py',
        )
        reports = _load_module(
            'lazymind.review.skill_review.reports',
            'lazymind/review/skill_review/reports.py',
        )
        miner = _load_module(
            'lazymind.review.skill_review.miner',
            'lazymind/review/skill_review/miner.py',
        )
        resolution = _load_module(
            'lazymind.review.skill_review.resolution',
            'lazymind/review/skill_review/resolution.py',
        )
        service = _load_module(
            'lazymind.review.service.skill_review',
            'lazymind/review/service/skill_review.py',
        )
        return SimpleNamespace(
            config=config,
            json_call=json_call,
            miner=miner,
            prompt=prompt,
            reports=reports,
            resolution=resolution,
            schemas=schemas,
            service=service,
        )
    finally:
        for name, original in originals.items():
            if original is None:
                sys.modules.pop(name, None)
            else:
                sys.modules[name] = original


class _FakeFS:
    def __init__(self, existing_paths=()):
        self.existing_paths = set(existing_paths)

    def exists(self, path):
        return path in self.existing_paths


class _FakeStore:
    def __init__(self, packages=None):
        self.packages = dict(packages or {})
        self.calls = []
        self.fs = _FakeFS(
            f'remote://skills/{category}/{name}' for category, name in self.packages
        )

    def package_dir(self, category, name):
        return f'remote://skills/{category}/{name}'

    def resolve_existing_identity(self, name):
        self.calls.append(('resolve_existing_identity', name))
        if '/' in name:
            category, skill_name = name.split('/', 1)
            return {'category': category, 'name': skill_name}
        matches = [
            {'category': current_category, 'name': current_name}
            for current_category, current_name in self.packages
            if current_name == name
        ]
        return matches[0] if len(matches) == 1 else {'error': 'not found or ambiguous'}

    def list_files(self, category, name):
        self.calls.append(('list_files', category, name))
        return dict(self.packages[(category, name)])

    def replace_files(self, category, name, before, after):
        self.calls.append(('replace_files', category, name, before, after))
        self.packages[(category, name)] = dict(after)
        return {'written': ['SKILL.md'], 'deleted': []}

    def create(self, category, name, content):
        self.calls.append(('create', category, name, content))
        self.packages[(category, name)] = {'SKILL.md': content}
        return {'action': 'create'}

    def rename(self, old_category, old_name, new_category, new_name, *, skill_content):
        self.calls.append((
            'rename',
            old_category,
            old_name,
            new_category,
            new_name,
            skill_content,
        ))
        old_key = (old_category, old_name)
        new_key = (new_category, new_name)
        if old_key not in self.packages:
            raise FileNotFoundError(f'Skill package {old_category}/{old_name} does not exist.')
        if new_key in self.packages:
            raise FileExistsError(f'Skill package {new_category}/{new_name} already exists.')
        files = self.packages.pop(old_key)
        files['SKILL.md'] = skill_content
        self.packages[new_key] = files
        return {'action': 'rename'}

    def remove(self, category, name):
        self.calls.append(('remove', category, name))
        if (category, name) not in self.packages:
            raise FileNotFoundError(f'Skill package {category}/{name} does not exist.')
        self.packages.pop((category, name))
        return {'action': 'remove'}


def _skill_content(name: str, category_line: str = '') -> str:
    return (
        '---\n'
        f'name: {name}\n'
        f'{category_line}'
        'description: Review generated skill.\n'
        '---\n'
        'Use this skill for review tests.\n'
    )


def test_candidate_schema_normalization_and_prompt_do_not_generate_category():
    modules = _load_skill_review_modules()
    outline = modules.schemas.SkillOutline(
        skill_name='review-generated',
        applicable_scenario='Use for review generation.',
        sop=[{
            'step_name': 'Generate',
            'action_goal': 'Generate a reusable skill.',
            'branch_conditions': [],
        }],
    )
    guidelines = modules.schemas.GuidelineSet()

    normalized = modules.miner._normalize_candidate_payload(
        {
            'skill_name': 'review-generated',
            'category': 'ignored-legacy-value',
            'applicable_scenario': 'Use for review generation.',
            'content': _skill_content('review-generated'),
        },
        outline,
        source_trajectories=['session-1'],
        source_skills={},
    )
    candidate = modules.schemas.CandidateSkill.model_validate(normalized)
    generation_prompt = modules.prompt.candidate_prompt(outline, guidelines)
    merge_prompt = modules.prompt.merge_skill_patch_prompt(
        candidate.model_dump(),
        target_skill_key='internal/existing',
        existing_skill_content=_skill_content('existing', 'category: legacy\n'),
    )

    assert 'category' not in modules.schemas.CandidateSkill.model_fields
    assert 'category' not in modules.schemas.CandidateSkillLLMOutput.model_fields
    assert 'category' not in normalized
    assert 'category' not in candidate.model_dump()
    assert 'category' not in generation_prompt.lower()
    assert 'description/category' not in merge_prompt


def test_candidate_llm_output_retries_until_complete_skill_document():
    modules = _load_skill_review_modules()
    cluster = modules.schemas.TaskCluster(task_scope='Review generation.')
    outline = modules.schemas.SkillOutline(
        skill_name='review-generated',
        applicable_scenario='Use for review generation.',
    )
    valid_content = _skill_content('review-generated')
    responses = iter([
        {
            'skill_name': 'review-generated',
            'applicable_scenario': 'Use for review generation.',
            'content': '## Procedure\n\nGenerate a reusable skill.\n',
        },
        {
            'skill_name': 'review-generated',
            'applicable_scenario': 'Use for review generation.',
            'content': valid_content,
        },
    ])
    calls = []

    def llm(prompt, **kwargs):
        calls.append((prompt, kwargs))
        return next(responses)

    result = modules.miner.build_candidate_skill(cluster, outline, llm)

    assert len(calls) == 2
    assert result.content == valid_content


def test_candidate_llm_output_rejects_frontmatter_name_mismatch():
    modules = _load_skill_review_modules()

    with pytest.raises(ValueError, match='frontmatter name.*skill_name'):
        modules.schemas.CandidateSkillLLMOutput(
            skill_name='review-generated',
            applicable_scenario='Use for review generation.',
            content=_skill_content('different-name'),
        )


def test_candidate_model_rejects_incomplete_document():
    modules = _load_skill_review_modules()
    outline = modules.schemas.SkillOutline(
        skill_name='review-generated',
        applicable_scenario='Use for review generation.',
    )

    with pytest.raises(ValueError, match='YAML frontmatter'):
        modules.schemas.CandidateSkill(
            skill_name='review-generated',
            applicable_scenario='Use for review generation.',
            content='## Procedure\n\nGenerate a reusable skill.\n',
            outline=outline,
        )


def test_candidate_llm_output_stops_after_invalid_retries():
    modules = _load_skill_review_modules()
    cluster = modules.schemas.TaskCluster(task_scope='Review generation.')
    outline = modules.schemas.SkillOutline(
        skill_name='review-generated',
        applicable_scenario='Use for review generation.',
    )
    calls = []

    def llm(prompt, **kwargs):
        calls.append((prompt, kwargs))
        return {
            'skill_name': 'review-generated',
            'applicable_scenario': 'Use for review generation.',
            'content': '## Procedure\n\nGenerate a reusable skill.\n',
        }

    with pytest.raises(ValueError, match='failed after 3 attempts') as exc_info:
        modules.miner.build_candidate_skill(cluster, outline, llm)

    assert len(calls) == 3
    assert 'YAML frontmatter' in str(exc_info.value)


def test_review_resolution_selects_exact_skill_key_when_storage_names_match():
    modules = _load_skill_review_modules()
    outline = modules.schemas.SkillOutline(
        skill_name='shared',
        applicable_scenario='Use for shared review generation.',
    )
    candidate = modules.schemas.CandidateSkill(
        skill_name='shared',
        applicable_scenario='Use for shared review generation.',
        content=_skill_content('shared'),
        outline=outline,
    )
    contents = {
        'internal/shared': _skill_content('shared'),
        'external/shared': _skill_content('shared'),
    }

    class _SkillManager:
        def __init__(self):
            self.read_keys = []

        def get_skill(self, key):
            self.read_keys.append(key)
            return {'status': 'ok', 'content': contents[key]}

    manager = _SkillManager()
    responses = iter([
        {
            'type': 'patch',
            'patch_skill_key': 'external/shared',
            'reason': 'The external skill is the exact workflow match.',
        },
        {
            'summary': 'Clarified the workflow boundary.',
            'skill_name': 'renamed-shared',
            'skill_content': _skill_content('renamed-shared'),
        },
    ])
    modules.resolution.call_json = lambda *_args, **_kwargs: next(responses)

    result = modules.resolution.resolve_skill_action(
        candidate,
        object(),
        skill_manager=manager,
        skill_summaries=(
            '- **internal/shared**\n'
            '  - Name: shared\n'
            '- **external/shared**\n'
            '  - Name: shared\n'
        ),
    )

    assert manager.read_keys == ['external/shared']
    assert result.target_skill_key == 'external/shared'
    assert result.skill_name == 'renamed-shared'


def test_review_resolution_rejects_patch_key_when_summaries_have_no_storage_keys():
    modules = _load_skill_review_modules()
    outline = modules.schemas.SkillOutline(
        skill_name='shared',
        applicable_scenario='Use for shared review generation.',
    )
    candidate = modules.schemas.CandidateSkill(
        skill_name='shared',
        applicable_scenario='Use for shared review generation.',
        content=_skill_content('shared'),
        outline=outline,
    )

    class _SkillManager:
        def __init__(self):
            self.read_keys = []

        def get_skill(self, key):
            self.read_keys.append(key)
            raise AssertionError('patch target must be rejected before reading')

    manager = _SkillManager()
    modules.resolution.call_json = lambda *_args, **_kwargs: {
        'type': 'patch',
        'patch_skill_key': 'external/shared',
        'reason': 'Patch the external workflow.',
    }

    with pytest.raises(ValueError, match='not in global skill summaries'):
        modules.resolution.resolve_skill_action(
            candidate,
            object(),
            skill_manager=manager,
            skill_summaries='# Skills\n\n- (none)',
        )

    assert manager.read_keys == []


def test_review_resolution_validates_patch_storage_key_before_reading():
    modules = _load_skill_review_modules()
    outline = modules.schemas.SkillOutline(
        skill_name='shared',
        applicable_scenario='Use for shared review generation.',
    )
    candidate = modules.schemas.CandidateSkill(
        skill_name='shared',
        applicable_scenario='Use for shared review generation.',
        content=_skill_content('shared'),
        outline=outline,
    )

    class _SkillManager:
        def __init__(self):
            self.read_keys = []

        def get_skill(self, key):
            self.read_keys.append(key)
            raise AssertionError('invalid storage key must be rejected before reading')

    manager = _SkillManager()
    modules.resolution.call_json = lambda *_args, **_kwargs: {
        'type': 'patch',
        'patch_skill_key': 'research/shared',
        'reason': 'Patch the listed workflow.',
    }

    with pytest.raises(ValueError, match='Skill storage category must be'):
        modules.resolution.resolve_skill_action(
            candidate,
            object(),
            skill_manager=manager,
            skill_summaries='- **research/shared**\n  - Name: shared',
        )

    assert manager.read_keys == []


def test_review_new_skill_always_uses_internal_and_preserves_content():
    modules = _load_skill_review_modules()
    store = _FakeStore()
    content_with_category = _skill_content('generated', 'category: accidental-value\n')
    record_with_category = modules.schemas.SkillReviewResolution(
        id='new-1',
        skill_name='generated',
        type='new',
        skill_content=content_with_category,
    )
    content_without_category = _skill_content('category-free')
    record_without_category = modules.schemas.SkillReviewResolution(
        id='new-2',
        skill_name='category-free',
        type='new',
        skill_content=content_without_category,
    )

    result_with_category = modules.service._apply_skill_review_record(record_with_category, store)
    result_without_category = modules.service._apply_skill_review_record(record_without_category, store)

    assert result_with_category['category'] == 'internal'
    assert result_without_category['category'] == 'internal'
    assert ('create', 'internal', 'generated', content_with_category) in store.calls
    assert ('create', 'internal', 'category-free', content_without_category) in store.calls


def test_new_review_resolution_rejects_target_skill_key():
    modules = _load_skill_review_modules()

    with pytest.raises(ValueError, match='target_skill_key must be empty'):
        modules.schemas.SkillReviewResolution(
            id='new-with-target',
            skill_name='generated',
            target_skill_key='internal/existing',
            type='new',
            skill_content=_skill_content('generated'),
        )


def test_patch_review_resolution_requires_target_skill_key():
    modules = _load_skill_review_modules()

    with pytest.raises(ValueError, match='target_skill_key is required'):
        modules.schemas.SkillReviewResolution(
            id='patch-without-target',
            skill_name='existing',
            type='patch',
            skill_content=_skill_content('existing'),
        )


def test_patch_review_resolution_rejects_non_storage_category_key():
    modules = _load_skill_review_modules()

    with pytest.raises(ValueError, match='Skill storage category must be'):
        modules.schemas.SkillReviewResolution(
            id='patch-with-invalid-target',
            skill_name='existing',
            target_skill_key='research/existing',
            type='patch',
            skill_content=_skill_content('existing'),
        )


def test_review_patch_ignores_frontmatter_category_and_keeps_storage_category():
    modules = _load_skill_review_modules()
    old_content = _skill_content('existing', 'category: original-content-value\n')
    store = _FakeStore({
        ('internal', 'existing'): {'SKILL.md': old_content},
        ('external', 'existing'): {
            'SKILL.md': old_content,
            'references/example.md': 'supporting file',
        },
    })
    patched_content = _skill_content('renamed', 'category: changed-content-value\n')
    record = modules.schemas.SkillReviewResolution(
        id='patch-1',
        skill_name='renamed',
        target_skill_key='external/existing',
        type='patch',
        skill_content=patched_content,
    )

    result = modules.service._apply_skill_review_record(record, store)

    assert result['old_category'] == 'external'
    assert result['category'] == 'external'
    assert ('resolve_existing_identity', 'external/existing') in store.calls
    assert ('rename', 'external', 'existing', 'external', 'renamed', patched_content) in store.calls
    assert ('internal', 'existing') in store.packages
    assert store.packages[('external', 'renamed')]['references/example.md'] == 'supporting file'
    assert not any(call[0] == 'create' and call[1] == 'changed-content-value' for call in store.calls)


def test_review_patch_rename_does_not_create_target_when_source_disappears_before_apply():
    modules = _load_skill_review_modules()
    store = _FakeStore()
    record = modules.schemas.SkillReviewResolution(
        id='patch-missing-source',
        skill_name='renamed',
        target_skill_key='external/missing',
        type='patch',
        skill_content=_skill_content('renamed'),
    )

    with pytest.raises(FileNotFoundError, match='external/missing does not exist'):
        modules.service._apply_skill_review_record(record, store)

    assert store.packages == {}
    assert not any(call[0] == 'create' for call in store.calls)


def test_review_patch_same_name_replaces_in_original_category_without_category_frontmatter():
    modules = _load_skill_review_modules()
    old_content = _skill_content('existing', 'category: legacy\n')
    store = _FakeStore({('internal', 'existing'): {'SKILL.md': old_content}})
    patched_content = _skill_content('existing')
    record = modules.schemas.SkillReviewResolution(
        id='patch-2',
        skill_name='existing',
        target_skill_key='internal/existing',
        type='patch',
        skill_content=patched_content,
    )

    result = modules.service._apply_skill_review_record(record, store)

    assert result['category'] == 'internal'
    assert any(call[0] == 'replace_files' and call[1:3] == ('internal', 'existing') for call in store.calls)
    assert not any(call[0] in {'create', 'remove'} for call in store.calls)
