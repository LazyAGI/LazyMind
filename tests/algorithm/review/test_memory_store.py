from __future__ import annotations

from typing import Any, Dict, List, Optional

import pytest
import yaml

from lazymind.common.memory.exceptions import MemoryPartialApplyError
from lazymind.common.memory.paths import (
    PREFERENCE_PATH,
    PROFILE_PATH,
    SOUL_PATH,
    build_reference_path,
    normalize_memory_path,
)
from lazymind.common.memory.validation import (
    PreferenceItem,
    append_preference_item,
    parse_preference_items,
    validate_preference_index,
)
from lazymind.common.memory.validation.document import validate_stored_memory_content
from lazymind.common.memory.store import MemoryStore
from lazymind.config import config as _cfg

SAMPLE_SOUL = (
    'schema_version: 2\n'
    'identity:\n'
    '  name: "LazyMind"\n'
    '  role: "personal_ai_assistant"\n'
    '  description: "desc"\n'
    'mission:\n'
    '  primary_goal: "g"\n'
    '  success_definition: "s"\n'
    'interaction:\n'
    '  default_relationship_mode: "collaborator"\n'
    '  default_tone: "warm_direct"\n'
    '  default_initiative_level: "proactive"\n'
    '  default_challenge_level: "constructive"\n'
    '  default_decision_mode: "recommend_then_confirm"\n'
    'epistemic:\n'
    '  uncertainty_style: "explicit"\n'
    '  verification_mode: "when_material"\n'
)

SAMPLE_PROFILE = (
    'schema_version: 2\n'
    'identity:\n'
    '  preferred_name: null\n'
    '  aliases: []\n'
    'locale:\n'
    '  languages: ["zh-CN"]\n'
    '  residence: "CN"\n'
    'professional:\n'
    '  occupations: []\n'
    '  organizations: []\n'
    '  industries: []\n'
    '  expertise_domains: []\n'
)

SAMPLE_PREFERENCE = 'preferences: []\n'
TIMESTAMP = '2026-07-20T09:30:00+08:00'


class FakeRemoteFS:
    def __init__(self, files: Optional[Dict[str, str]] = None):
        self.files: Dict[str, str] = dict(files or {})
        self.dirs: set[str] = set()
        self.fail_write_paths: set[str] = set()
        self.fail_rm_paths: set[str] = set()

    def exists(self, path: str) -> bool:
        normalized = normalize_memory_path(path)
        return normalized in self.files or normalized in self.dirs

    def ls(self, path: str, detail: bool = True) -> List[Any]:
        normalized = normalize_memory_path(path)
        prefix = normalized.rstrip('/') + '/'
        items = []
        seen = set()
        for file_path in sorted(self.files):
            if not file_path.startswith(prefix):
                continue
            rest = file_path[len(prefix) :]
            name = rest.split('/', 1)[0]
            full = f'{normalized}/{name}'
            if full in seen:
                continue
            seen.add(full)
            if '/' in rest:
                items.append({'name': full, 'path': full, 'type': 'dir'})
            else:
                items.append(
                    {
                        'name': full,
                        'path': full,
                        'type': 'file',
                        'size': len(self.files[file_path]),
                    }
                )
        return items

    def makedirs(self, path: str, exist_ok: bool = True) -> None:
        self.dirs.add(normalize_memory_path(path))

    def write(self, path: str, content: str, content_type: str = 'text/plain; charset=utf-8') -> None:
        normalized = normalize_memory_path(path)
        if normalized in self.fail_write_paths:
            raise RuntimeError(f'write failed: {normalized}')
        self.files[normalized] = content
        parent = normalized.rsplit('/', 1)[0]
        self.dirs.add(parent)

    def rm(self, path: str) -> None:
        normalized = normalize_memory_path(path)
        if normalized in self.fail_rm_paths:
            raise RuntimeError(f'delete failed: {normalized}')
        self.files.pop(normalized, None)

    def open(self, path: str, mode: str = 'rb', **kwargs):
        normalized = normalize_memory_path(path)
        if normalized not in self.files:
            raise FileNotFoundError(normalized)

        class _Handle:
            def __init__(self, text: str):
                self._text = text

            def read(self):
                return self._text

            def __enter__(self):
                return self

            def __exit__(self, *args):
                return False

        return _Handle(self.files[normalized])


def test_validate_sample_documents():
    assert validate_stored_memory_content(SAMPLE_SOUL, label='soul') is None
    assert validate_stored_memory_content(SAMPLE_PROFILE, label='profile') is None
    assert validate_preference_index(SAMPLE_PREFERENCE) is None


def test_profile_validation_discovers_current_fields_and_rejects_unsupported_types():
    dynamic_profile = 'schema_version: 2\npersonal:\n  nickname: Neo\n  interests: [AI]\n  headline: null\n'

    assert validate_stored_memory_content(dynamic_profile, label='profile') is None
    error = validate_stored_memory_content(
        dynamic_profile.replace('  interests: [AI]\n', '  interests: [AI, 7]\n'),
        label='profile',
    )
    assert error is not None
    assert 'personal.interests' in error
    assert 'list of strings' in error
    for invalid_leaf in ('  score: 7\n', '  active: true\n'):
        error = validate_stored_memory_content(
            dynamic_profile.replace('  headline: null\n', invalid_leaf),
            label='profile',
        )
        assert error is not None
        assert 'unsupported type' in error


def test_memory_store_roundtrip():
    fs = FakeRemoteFS(
        {
            SOUL_PATH: SAMPLE_SOUL,
            PROFILE_PATH: SAMPLE_PROFILE,
            PREFERENCE_PATH: SAMPLE_PREFERENCE,
        }
    )
    store = MemoryStore(fs)
    soul = store.read_soul()
    profile = store.read_profile()
    assert 'schema_version' not in soul
    assert 'schema_version' not in profile
    assert yaml.safe_load(soul)['identity']['name'] == 'LazyMind'
    assert yaml.safe_load(profile)['locale']['residence'] == 'CN'
    assert store.read_preference() == SAMPLE_PREFERENCE

    store.write(
        build_reference_path('response'),
        (
            '---\n'
            'name: pref.response\n'
            'summary: Response preferences\n'
            f'created_at: "{TIMESTAMP}"\n'
            f'updated_at: "{TIMESTAMP}"\n'
            'source:\n'
            '  kind: chat_explicit\n'
            '  conversation_id: conversation-1\n'
            '---\n'
            '## Application Scenarios\n'
            'Technical questions.\n'
            '## Preference Details\n'
            'Explain motivations and tradeoffs.\n'
            '## Reason\n'
            'The user requested it.\n'
        ),
    )
    section = store.read_reference('references/response.md#pref-response-technical-detail')
    assert 'Explain motivations and tradeoffs.' in section


def test_memory_store_does_not_migrate_versionless_documents():
    legacy_soul = (
        SAMPLE_SOUL.removeprefix('schema_version: 2\n')
        .replace('default_relationship_mode:', 'relationship_mode:')
        .replace('default_initiative_level:', 'initiative_level:')
        .replace('default_challenge_level:', 'challenge_level:')
        .replace('default_decision_mode:', 'decision_mode:')
    )
    legacy_profile = (
        'identity:\n'
        '  preferred_name: Alice\n'
        '  aliases: [A]\n'
        '  pronouns: she/her\n'
        'locale:\n'
        '  languages: [中文]\n'
        '  timezone: Asia/Shanghai\n'
        '  region: 上海\n'
        'professional:\n'
        '  roles: [产品经理]\n'
        '  organization: LazyMind\n'
        '  industry: 人工智能\n'
        '  expertise_domains: [Agent Memory]\n'
        'accessibility:\n'
        '  communication_needs: []\n'
    )
    fs = FakeRemoteFS(
        {
            SOUL_PATH: legacy_soul,
            PROFILE_PATH: legacy_profile,
        }
    )
    store = MemoryStore(fs)

    with pytest.raises(ValueError, match='internal version metadata is missing'):
        store.read_soul()
    with pytest.raises(ValueError, match='internal version metadata is missing'):
        store.read_profile()
    assert fs.files[SOUL_PATH] == legacy_soul
    assert fs.files[PROFILE_PATH] == legacy_profile


@pytest.mark.parametrize(
    ('invalid', 'expected_error'),
    (
        (
            SAMPLE_PROFILE.replace('  aliases: []\n', '  aliases: not-a-list\n'),
            'identity.aliases',
        ),
        (
            SAMPLE_PROFILE.replace('  residence: "CN"\n', '  residence: [CN]\n'),
            'locale.residence',
        ),
        (
            SAMPLE_PROFILE.replace('  aliases: []\n', '  aliases: []\n  extra: value\n'),
            'leaf fields',
        ),
        (
            SAMPLE_PROFILE.replace('  aliases: []\n', '  known_as: []\n'),
            'leaf fields',
        ),
        (
            SAMPLE_PROFILE.replace('  aliases: []\n', '  aliases:\n    nested: []\n'),
            'mapping structure',
        ),
    ),
)
def test_memory_store_rejects_profile_type_changes_before_writing(
    monkeypatch,
    invalid,
    expected_error,
):
    fs = FakeRemoteFS({PROFILE_PATH: SAMPLE_PROFILE})
    store = MemoryStore(fs)
    original = fs.files[PROFILE_PATH]

    def fake_apply(_content, operations, *, label):
        assert label == 'profile'
        return {
            'content': invalid.replace('schema_version: 2\n', '', 1),
            'stored_content': invalid,
            'operations': operations,
        }

    monkeypatch.setattr(
        'lazymind.common.memory.store.apply_memory_operations',
        fake_apply,
    )

    with pytest.raises(ValueError, match=expected_error):
        store.apply_profile_operations(
            [
                {'op': 'add', 'path': 'identity.aliases', 'value': 'Neo'},
            ]
        )
    assert fs.files[PROFILE_PATH] == original


def test_memory_store_writes_the_same_stored_profile_it_validates(monkeypatch):
    edited_profile = SAMPLE_PROFILE.replace('  aliases: []\n', '  aliases: [Neo]\n')
    visible_profile = edited_profile.removeprefix('schema_version: 2\n')
    fs = FakeRemoteFS({PROFILE_PATH: SAMPLE_PROFILE})
    store = MemoryStore(fs)

    def fake_apply(_content, operations, *, label):
        assert label == 'profile'
        return {
            'content': visible_profile,
            'stored_content': edited_profile,
            'operations': operations,
        }

    monkeypatch.setattr(
        'lazymind.common.memory.store.apply_memory_operations',
        fake_apply,
    )

    result = store.apply_profile_operations(
        [
            {'op': 'add', 'path': 'identity.aliases', 'value': 'Neo'},
        ]
    )

    assert 'ok' not in result
    assert result['content'] == visible_profile
    assert fs.files[PROFILE_PATH] == edited_profile


def test_memory_store_rejects_invalid_path_and_content():
    store = MemoryStore(FakeRemoteFS())
    with pytest.raises(ValueError):
        store.write('memory/agents/../secret.md', 'x')
    with pytest.raises(ValueError):
        store.write(SOUL_PATH, '- invalid\n')


def test_fixed_memory_file_missing_is_an_error():
    store = MemoryStore(FakeRemoteFS())
    with pytest.raises(FileNotFoundError, match='soul.yaml'):
        store.read_soul()


def test_preference_add_can_exceed_prompt_projection_limit():
    content = SAMPLE_PREFERENCE
    for idx in range(2):
        content = append_preference_item(
            content,
            PreferenceItem(
                name=f'pref.existing.{idx}',
                summary=f'existing {idx}',
                ref=f'references/existing-{idx}.md',
                created_at=TIMESTAMP,
                updated_at=TIMESTAMP,
            ),
        )
    fs = FakeRemoteFS({PREFERENCE_PATH: content})
    with _cfg.temp('preference_context_max_chars', 2):
        MemoryStore(fs).add_preference_with_reference(
            name='pref.response.concise',
            summary='回答要简洁',
            scenario='日常问答',
            details='先给结论，再按需补充背景。',
            reason='用户明确要求',
            source_kind='memory_review',
            conversation_id='conversation-1',
        )

    assert len(parse_preference_items(fs.files[PREFERENCE_PATH])) == 3
    assert build_reference_path('response-concise') in fs.files


def test_preference_add_returns_item_without_internal_envelope():
    fs = FakeRemoteFS({PREFERENCE_PATH: SAMPLE_PREFERENCE})

    item = MemoryStore(fs).add_preference_with_reference(
        name='pref.response.concise',
        summary='回答要简洁',
        scenario='日常问答',
        details='先给结论，再按需补充背景。',
        reason='用户明确要求',
        source_kind='memory_review',
        conversation_id='conversation-1',
    )

    assert isinstance(item, PreferenceItem)
    assert item.name == 'pref.response.concise'
    assert 'pref.response.concise' in fs.files[PREFERENCE_PATH]
    assert build_reference_path('response-concise') in fs.files


def test_preference_add_reports_partial_apply_when_cleanup_fails():
    fs = FakeRemoteFS({PREFERENCE_PATH: SAMPLE_PREFERENCE})
    reference_path = build_reference_path('response-concise')
    fs.fail_write_paths.add(PREFERENCE_PATH)
    fs.fail_rm_paths.add(reference_path)

    with pytest.raises(MemoryPartialApplyError) as captured:
        MemoryStore(fs).add_preference_with_reference(
            name='pref.response.concise',
            summary='回答要简洁',
            scenario='日常问答',
            details='先给结论，再按需补充背景。',
            reason='用户明确要求',
            source_kind='memory_review',
            conversation_id='conversation-1',
        )

    assert captured.value.operation == 'add'
    assert captured.value.applied == ('reference',)
    assert captured.value.failed == ('preference_index', 'reference_cleanup')
    assert fs.files[PREFERENCE_PATH] == SAMPLE_PREFERENCE
    assert reference_path in fs.files


@pytest.mark.parametrize('cleanup_fails', [False, True])
def test_shared_merge_persistence_compensates_failed_index_write(cleanup_fails):
    from lazymind.common.memory.editors.preference import add_preference_entry

    edited = add_preference_entry(
        SAMPLE_PREFERENCE, name='pref.merged', summary='Merged', scenario='Always',
        details='Both source rules', reason='Same scope', source_kind='memory_review',
        conversation_id='conversation-1',
    )
    fs = FakeRemoteFS({PREFERENCE_PATH: SAMPLE_PREFERENCE})
    reference_path = build_reference_path(edited['reference_name'])
    fs.fail_write_paths.add(PREFERENCE_PATH)
    if cleanup_fails:
        fs.fail_rm_paths.add(reference_path)
    with pytest.raises(Exception) as captured:
        MemoryStore(fs).write_preference_with_new_reference(
            original=SAMPLE_PREFERENCE, proposed=edited['content'], reference_name=edited['reference_name'],
            reference_content=edited['reference_content'], item=edited['item'], operation='merge',
        )
    assert fs.files[PREFERENCE_PATH] == SAMPLE_PREFERENCE
    assert (reference_path in fs.files) == cleanup_fails
    assert isinstance(captured.value, MemoryPartialApplyError) == cleanup_fails
    if cleanup_fails:
        assert captured.value.operation == 'merge'
        assert captured.value.applied == ('new_reference',)
        assert captured.value.failed == ('preference_index', 'new_reference_cleanup')


@pytest.mark.parametrize('new_name', ['pref.a_b', 'pref.a-b'])
def test_new_preference_name_mapping_collision_is_rejected_before_write(new_name):
    content = append_preference_item(
        SAMPLE_PREFERENCE,
        PreferenceItem(
            name='pref.a.b',
            summary='existing',
            ref='references/a-b.md#section',
            created_at=TIMESTAMP,
            updated_at=TIMESTAMP,
        ),
    )
    fs = FakeRemoteFS({PREFERENCE_PATH: content, build_reference_path('a-b'): 'existing reference'})
    before = dict(fs.files)
    with pytest.raises(ValueError, match='duplicate preference reference|existing reference'):
        MemoryStore(fs).add_preference_with_reference(
            name=new_name,
            summary='new',
            scenario='same scope',
            details='details',
            reason='reason',
            source_kind='memory_review',
            conversation_id='conversation-1',
        )
    assert fs.files == before
