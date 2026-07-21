from __future__ import annotations

import importlib
from typing import Any, Dict, List, Optional
from unittest.mock import patch

from lazymind.review.memory_review.defaults import (
    default_preference_md,
    default_profile_md,
    default_soul_md,
)
from lazymind.review.memory_review.paths import (
    PREFERENCE_PATH,
    PROFILE_PATH,
    SOUL_PATH,
    build_reference_path,
    normalize_memory_path,
)
from lazymind.review.memory_review.store import MemoryStore

soul_editor_mod = importlib.import_module('lazymind.chat.engine.tools.soul_editor')
profile_editor_mod = importlib.import_module('lazymind.chat.engine.tools.profile_editor')
preference_editor_mod = importlib.import_module('lazymind.chat.engine.tools.preference_editor')


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

    def rm(self, path: str) -> None:
        normalized = normalize_memory_path(path)
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


class FakeMemoryRemoteStore:
    def __init__(self, fs: FakeRemoteFS):
        self.fs = fs
        self.store = MemoryStore(fs)


def test_soul_editor_updates_supported_field():
    fs = FakeRemoteFS({
        SOUL_PATH: default_soul_md(),
        PROFILE_PATH: default_profile_md(),
        PREFERENCE_PATH: default_preference_md(),
    })
    remote = FakeMemoryRemoteStore(fs)
    with patch.object(soul_editor_mod, 'MemoryRemoteStore', lambda *args, **kwargs: remote):
        payload = soul_editor_mod.soul_editor('identity.description', '更直接的助手')

    assert payload['success'] is True
    assert payload['result']['status'] == 'applied'
    assert '更直接的助手' in fs.files[SOUL_PATH]


def test_soul_editor_rejects_unsupported_field():
    fs = FakeRemoteFS({
        SOUL_PATH: default_soul_md(),
        PROFILE_PATH: default_profile_md(),
        PREFERENCE_PATH: default_preference_md(),
    })
    remote = FakeMemoryRemoteStore(fs)
    with patch.object(soul_editor_mod, 'MemoryRemoteStore', lambda *args, **kwargs: remote):
        payload = soul_editor_mod.soul_editor('identity.email', 'x@y.com')
    assert payload['success'] is False
    assert 'unsupported soul field' in payload['error']['reason']


def test_profile_editor_updates_list_field():
    fs = FakeRemoteFS({
        SOUL_PATH: default_soul_md(),
        PROFILE_PATH: default_profile_md(),
        PREFERENCE_PATH: default_preference_md(),
    })
    remote = FakeMemoryRemoteStore(fs)
    with patch.object(profile_editor_mod, 'MemoryRemoteStore', lambda *args, **kwargs: remote):
        payload = profile_editor_mod.profile_editor('locale.languages', '["zh-CN","en-US"]')

    assert payload['success'] is True
    assert payload['result']['status'] == 'applied'
    assert 'en-US' in fs.files[PROFILE_PATH]


def test_preference_editor_add_and_delete():
    fs = FakeRemoteFS({
        SOUL_PATH: default_soul_md(),
        PROFILE_PATH: default_profile_md(),
        PREFERENCE_PATH: default_preference_md(),
    })
    remote = FakeMemoryRemoteStore(fs)
    with patch.object(preference_editor_mod, 'MemoryRemoteStore', lambda *args, **kwargs: remote):
        added = preference_editor_mod.preference_editor(
            'add',
            name='pref.response.concise',
            summary='回答要简洁',
            scenario='日常问答',
            reason='用户明确要求简洁回答',
        )
        assert added['success'] is True
        assert added['result']['status'] == 'applied'
        assert 'pref.response.concise' in fs.files[PREFERENCE_PATH]
        assert build_reference_path('response-concise') in fs.files

        deleted = preference_editor_mod.preference_editor('delete', name='pref.response.concise')
        assert deleted['success'] is True
        assert 'pref.response.concise' not in fs.files[PREFERENCE_PATH]
        assert build_reference_path('response-concise') not in fs.files
