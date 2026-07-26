from __future__ import annotations

from typing import Any, Dict, List, Optional

import pytest
import yaml

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
    validate_preference_index,
    validate_profile_content,
    validate_soul_content,
)
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
            rest = file_path[len(prefix):]
            name = rest.split('/', 1)[0]
            full = f'{normalized}/{name}'
            if full in seen:
                continue
            seen.add(full)
            if '/' in rest:
                items.append({'name': full, 'path': full, 'type': 'dir'})
            else:
                items.append({
                    'name': full,
                    'path': full,
                    'type': 'file',
                    'size': len(self.files[file_path]),
                })
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
    assert validate_soul_content(SAMPLE_SOUL) is None
    assert validate_profile_content(SAMPLE_PROFILE) is None
    assert validate_preference_index(SAMPLE_PREFERENCE) is None


def test_memory_store_roundtrip():
    fs = FakeRemoteFS({
        SOUL_PATH: SAMPLE_SOUL,
        PROFILE_PATH: SAMPLE_PROFILE,
        PREFERENCE_PATH: SAMPLE_PREFERENCE,
    })
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


def test_memory_store_migrates_legacy_documents_and_hides_version():
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
    fs = FakeRemoteFS({
        SOUL_PATH: legacy_soul,
        PROFILE_PATH: legacy_profile,
    })
    store = MemoryStore(fs)

    visible_soul = store.read_soul()
    visible_profile = store.read_profile()

    assert 'schema_version' not in visible_soul
    assert 'schema_version' not in visible_profile
    assert fs.files[SOUL_PATH].startswith('schema_version: 2\n')
    assert fs.files[PROFILE_PATH].startswith('schema_version: 2\n')
    profile = yaml.safe_load(visible_profile)
    assert profile['locale']['residence'] == '上海'
    assert profile['professional']['occupations'] == ['产品经理']
    assert profile['professional']['organizations'] == ['LazyMind']
    assert profile['professional']['industries'] == ['人工智能']


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


def test_preference_add_rejects_capacity_before_writing_reference():
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

    with _cfg.temp('preference_index_max_items', 2):
        result = MemoryStore(fs).add_preference_with_reference(
            name='pref.response.concise',
            summary='回答要简洁',
            scenario='日常问答',
            details='先给结论，再按需补充背景。',
            reason='用户明确要求',
            source_kind='memory_review',
            conversation_id='conversation-1',
        )

    assert result['ok'] is False
    assert result['type'] == 'capacity_exceeded'
    assert result['used_items'] == 3
    assert result['max_items'] == 2
    assert build_reference_path('response-concise') not in fs.files
