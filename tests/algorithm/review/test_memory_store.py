from __future__ import annotations

from typing import Any, Dict, List, Optional

from lazymind.common.memory.paths import (
    PREFERENCE_PATH,
    PROFILE_PATH,
    REFERENCE_ROOT,
    SOUL_PATH,
    build_reference_path,
    is_memory_path,
    normalize_memory_path,
    split_reference_ref,
)
from lazymind.common.memory.validation import (
    PreferenceItem,
    append_preference_item,
    parse_preference_items,
    validate_preference_index,
    validate_profile_content,
    validate_reference_content,
    validate_soul_content,
)
from lazymind.common.memory.store import MemoryStore

SAMPLE_SOUL = (
    '---\n'
    'schema_version: 1\n'
    'identity:\n'
    '  name: "LazyMind"\n'
    '  role: "personal_ai_assistant"\n'
    '  description: "desc"\n'
    'mission:\n'
    '  primary_goal: "g"\n'
    '  success_definition: "s"\n'
    'interaction:\n'
    '  relationship_mode: "collaborator"\n'
    '  default_tone: "warm_direct"\n'
    '  initiative_level: "proactive"\n'
    '  challenge_level: "constructive"\n'
    '  decision_mode: "recommend_then_confirm"\n'
    'epistemic:\n'
    '  uncertainty_style: "explicit"\n'
    '  verification_mode: "when_material"\n'
    '---\n'
)

SAMPLE_PROFILE = (
    '---\n'
    'schema_version: 1\n'
    'identity:\n'
    '  preferred_name: null\n'
    '  aliases: []\n'
    '  pronouns: null\n'
    'locale:\n'
    '  languages: ["zh-CN"]\n'
    '  timezone: "Asia/Shanghai"\n'
    '  region: "CN"\n'
    'professional:\n'
    '  roles: []\n'
    '  organization: null\n'
    '  industry: null\n'
    '  expertise_domains: []\n'
    'accessibility:\n'
    '  communication_needs: []\n'
    '---\n'
)

SAMPLE_PREFERENCE = (
    '---\n'
    'schema_version: 1\n'
    'updated_at: 2026-07-20\n'
    '---\n'
    '# Preference Index\n'
)


class FakeRemoteFS:
    def __init__(self, files: Optional[Dict[str, str]] = None):
        self.files: Dict[str, str] = dict(files or {})
        self.dirs: set[str] = set()

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
        self.files[normalized] = content
        parent = normalized.rsplit('/', 1)[0]
        self.dirs.add(parent)

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


def test_normalize_and_reference_paths():
    assert normalize_memory_path('/memory/agents/soul.md') == SOUL_PATH
    assert is_memory_path(SOUL_PATH)
    assert build_reference_path('response') == f'{REFERENCE_ROOT}/response.md'
    path, anchor = split_reference_ref('references/response.md#pref-response-technical-detail')
    assert path == f'{REFERENCE_ROOT}/response.md'
    assert anchor == 'pref-response-technical-detail'


def test_validate_sample_documents():
    assert validate_soul_content(SAMPLE_SOUL) is None
    assert validate_profile_content(SAMPLE_PROFILE) is None
    assert validate_preference_index(SAMPLE_PREFERENCE) is None


def test_validate_soul_rejects_body_and_extra_keys():
    bad = SAMPLE_SOUL + '\nfree text\n'
    assert 'free-form body' in (validate_soul_content(bad) or '')
    bad_key = SAMPLE_SOUL.replace('schema_version: 1', 'schema_version: 1\nextra: 1', 1)
    assert 'unsupported keys' in (validate_soul_content(bad_key) or '')


def test_preference_item_parse_and_append():
    content = SAMPLE_PREFERENCE
    item = PreferenceItem(
        name='pref.response.technical_detail',
        summary='Explain tradeoffs for technical questions.',
        ref='references/response.md#pref-response-technical-detail',
    )
    updated = append_preference_item(content, item)
    assert validate_preference_index(updated) is None
    assert parse_preference_items(updated) == [item]


def test_preference_summary_length_limit():
    item = PreferenceItem(
        name='pref.too.long',
        summary='x' * 101,
        ref='references/response.md',
    )
    error = validate_preference_index(
        SAMPLE_PREFERENCE + f'- name: {item.name}\n  summary: {item.summary}\n  ref: {item.ref}\n'
    )
    assert error and '100 characters' in error


def test_memory_store_roundtrip():
    fs = FakeRemoteFS({
        SOUL_PATH: SAMPLE_SOUL,
        PROFILE_PATH: SAMPLE_PROFILE,
        PREFERENCE_PATH: SAMPLE_PREFERENCE,
    })
    store = MemoryStore(fs)
    assert store.read_soul() == SAMPLE_SOUL
    assert store.read_profile() == SAMPLE_PROFILE
    assert store.read_preference() == SAMPLE_PREFERENCE

    store.write_reference(
        'response',
        (
            '---\n'
            'name: response\n'
            'description: Response preferences\n'
            'metadata:\n'
            '  node_type: memory\n'
            '  type: feedback\n'
            '---\n'
            '## Pref Response Technical Detail\n'
            'Explain motivations and tradeoffs.\n'
        ),
    )
    refs = store.list_references()
    assert [item['name'] for item in refs] == ['response.md']
    section = store.read_reference('references/response.md#pref-response-technical-detail')
    assert 'Explain motivations and tradeoffs.' in section


def test_memory_store_rejects_invalid_path_and_content():
    store = MemoryStore(FakeRemoteFS())
    try:
        store.write('memory/agents/../secret.md', 'x')
        assert False, 'expected ValueError'
    except ValueError:
        pass
    try:
        store.write_soul('---\nschema_version: 1\n---\nbody\n')
        assert False, 'expected ValueError'
    except ValueError:
        pass


def test_validate_reference_content():
    assert validate_reference_content(
        '---\nname: demo\ndescription: demo ref\n---\n\nbody\n'
    ) is None
    assert validate_reference_content('---\nname: demo\n---\n\nbody\n') is not None


def test_apply_soul_field_returns_structured_error_for_missing_field():
    store = MemoryStore(FakeRemoteFS({SOUL_PATH: SAMPLE_SOUL}))
    result = store.apply_soul_field('identity.email', 'x@y.com')
    assert result['ok'] is False
    assert result['type'] == 'validation'
    assert 'does not exist in soul' in result['error']
