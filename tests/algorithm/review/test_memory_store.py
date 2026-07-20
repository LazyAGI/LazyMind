from __future__ import annotations

from typing import Any, Dict, List, Optional

from lazymind.review.memory_review.defaults import (
    default_preference_md,
    default_profile_md,
    default_soul_md,
)
from lazymind.review.memory_review.errors import MemoryPathError, MemoryValidationError
from lazymind.review.memory_review.migrate import migrate_legacy_memory
from lazymind.review.memory_review.paths import (
    PREFERENCE_PATH,
    PROFILE_PATH,
    REFERENCE_ROOT,
    SOUL_PATH,
    build_reference_path,
    is_memory_path,
    normalize_memory_path,
    split_reference_ref,
)
from lazymind.review.memory_review.schema import (
    PreferenceItem,
    append_preference_item,
    parse_preference_items,
    validate_preference_index,
    validate_profile_content,
    validate_reference_content,
    validate_soul_content,
)
from lazymind.review.memory_review.store import MemoryStore


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


def test_validate_default_templates():
    assert validate_soul_content(default_soul_md()) is None
    assert validate_profile_content(default_profile_md()) is None
    assert validate_preference_index(default_preference_md()) is None


def test_validate_soul_rejects_body_and_extra_keys():
    bad = default_soul_md() + '\nfree text\n'
    assert 'free-form body' in (validate_soul_content(bad) or '')
    bad_key = default_soul_md().replace('schema_version: 1', 'schema_version: 1\nextra: 1', 1)
    assert 'unsupported keys' in (validate_soul_content(bad_key) or '')


def test_preference_item_parse_and_append():
    content = default_preference_md()
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
        default_preference_md() + f'- name: {item.name}\n  summary: {item.summary}\n  ref: {item.ref}\n'
    )
    assert error and '100 characters' in error


def test_memory_store_roundtrip():
    fs = FakeRemoteFS()
    store = MemoryStore(fs)
    store.ensure_defaults()
    assert SOUL_PATH in fs.files
    assert PROFILE_PATH in fs.files
    assert PREFERENCE_PATH in fs.files

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
        assert False, 'expected MemoryPathError'
    except MemoryPathError:
        pass
    try:
        store.write_soul('---\nschema_version: 1\n---\nbody\n')
        assert False, 'expected MemoryValidationError'
    except MemoryValidationError:
        pass


def test_migrate_legacy_memory():
    fs = FakeRemoteFS({
        'memory/memory.md': 'old working memory note\n',
        'memory/user.md': (
            '---\n'
            'agent_persona: Helper\n'
            'preferred_name: Alice\n'
            'response_style: concise\n'
            '---\n'
            'Prefer bullet points.\n'
        ),
    })
    store = MemoryStore(fs)
    result = migrate_legacy_memory(store)
    assert result.migrated is True
    assert 'Alice' in store.read_profile()
    assert 'Helper' in store.read_soul()
    assert store.exists(build_reference_path('legacy-memory'))
    assert store.exists(build_reference_path('legacy-preferences'))
    assert 'pref.legacy.user_preference' in store.read_preference()

    # Second run should not overwrite.
    second = migrate_legacy_memory(store)
    assert second.migrated is False
    assert any('already initialized' in warning for warning in second.warnings)


def test_validate_reference_content():
    assert validate_reference_content(
        '---\nname: demo\ndescription: demo ref\n---\n\nbody\n'
    ) is None
    assert validate_reference_content('---\nname: demo\n---\n\nbody\n') is not None
