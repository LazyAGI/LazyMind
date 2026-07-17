from __future__ import annotations

from concurrent.futures import ThreadPoolExecutor
import importlib.util
from pathlib import Path
import sys
from types import ModuleType, SimpleNamespace


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
        'lazymind.chat.engine.tools.infra.skill_remote_store',
        'lazymind.chat.engine.tools.infra.skill_validation',
        'lazymind.chat.integrations',
        'lazymind.chat.integrations.remote_fs',
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
        'lazymind.chat.engine.tools.infra.skill_remote_store': _module(
            'lazymind.chat.engine.tools.infra.skill_remote_store', SkillRemoteStore=object
        ),
        'lazymind.chat.integrations': _package('lazymind.chat.integrations'),
        'lazymind.chat.integrations.remote_fs': _module(
            'lazymind.chat.integrations.remote_fs', RemoteFS=object
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
        validation = _load_module(
            'lazymind.chat.engine.tools.infra.skill_validation',
            'lazymind/chat/engine/tools/infra/skill_validation.py',
        )
        config = _load_module(
            'lazymind.review.skill_review.config',
            'lazymind/review/skill_review/config.py',
        )
        schemas = _load_module(
            'lazymind.review.skill_review.schemas',
            'lazymind/review/skill_review/schemas.py',
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
        service = _load_module(
            'lazymind.review.service.skill_review',
            'lazymind/review/service/skill_review.py',
        )
        return SimpleNamespace(
            config=config,
            miner=miner,
            prompt=prompt,
            reports=reports,
            schemas=schemas,
            service=service,
            validation=validation,
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

    def resolve_existing_identity(self, name, category=None):
        self.calls.append(('resolve_existing_identity', name, category))
        matches = [
            {'category': current_category, 'name': current_name}
            for current_category, current_name in self.packages
            if current_name == name and (category is None or current_category == category)
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

    def remove(self, category, name):
        self.calls.append(('remove', category, name))
        self.packages.pop((category, name), None)
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
        patch_skill_name='existing',
        existing_skill_content=_skill_content('existing', 'category: legacy\n'),
    )

    assert 'category' not in modules.schemas.CandidateSkill.model_fields
    assert 'category' not in modules.schemas.CandidateSkillLLMOutput.model_fields
    assert 'category' not in normalized
    assert 'category' not in candidate.model_dump()
    assert 'category' not in generation_prompt.lower()
    assert 'description/category' not in merge_prompt


def test_category_independent_validation_keeps_strict_validator_for_organize():
    modules = _load_skill_review_modules()
    content = _skill_content('category-free')

    assert modules.validation.validate_skill_document(content) is None
    assert modules.validation.validate_skill_content(content) == (
        "Frontmatter must include non-empty 'category'."
    )


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


def test_review_patch_ignores_frontmatter_category_and_keeps_storage_category():
    modules = _load_skill_review_modules()
    old_content = _skill_content('existing', 'category: original-content-value\n')
    store = _FakeStore({('external', 'existing'): {'SKILL.md': old_content}})
    patched_content = _skill_content('renamed', 'category: changed-content-value\n')
    record = modules.schemas.SkillReviewResolution(
        id='patch-1',
        skill_name='existing',
        type='patch',
        skill_content=patched_content,
    )

    result = modules.service._apply_skill_review_record(record, store)

    assert result['old_category'] == 'external'
    assert result['category'] == 'external'
    assert ('create', 'external', 'renamed', patched_content) in store.calls
    assert ('remove', 'external', 'existing') in store.calls
    assert not any(call[0] == 'create' and call[1] == 'changed-content-value' for call in store.calls)


def test_review_patch_same_name_replaces_in_original_category_without_category_frontmatter():
    modules = _load_skill_review_modules()
    old_content = _skill_content('existing', 'category: legacy\n')
    store = _FakeStore({('internal', 'existing'): {'SKILL.md': old_content}})
    patched_content = _skill_content('existing')
    record = modules.schemas.SkillReviewResolution(
        id='patch-2',
        skill_name='existing',
        type='patch',
        skill_content=patched_content,
    )

    result = modules.service._apply_skill_review_record(record, store)

    assert result['category'] == 'internal'
    assert any(call[0] == 'replace_files' and call[1:3] == ('internal', 'existing') for call in store.calls)
    assert not any(call[0] in {'create', 'remove'} for call in store.calls)
