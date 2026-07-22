from __future__ import annotations

from typing import Any, Dict, List, Optional
from unittest.mock import patch

from lazymind.chat.engine.tools.memory import MemoryTools
from lazymind.common.memory.paths import (
    PREFERENCE_PATH,
    PROFILE_PATH,
    SOUL_PATH,
    build_reference_path,
    normalize_memory_path,
)
from lazymind.common.memory.store import MemoryStore

SAMPLE_SOUL = (
    '---\n'
    'schema_version: 1\n'
    'identity:\n'
    '  name: "LazyMind"\n'
    '  role: "personal_ai_assistant"\n'
    '  description: "面向研究、分析和复杂任务的个人智能助手"\n'
    'mission:\n'
    '  primary_goal: "帮助用户准确、高效地思考并完成工作"\n'
    '  success_definition: "输出可靠、可执行且符合用户真实目标的结果"\n'
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


class FakeMemoryStore(MemoryStore):
    def __init__(self, fs: FakeRemoteFS):
        super().__init__(fs)


def _tools_with_store(fs: FakeRemoteFS):
    store = FakeMemoryStore(fs)
    return MemoryTools(), store


def test_soul_editor_updates_supported_field():
    fs = FakeRemoteFS({
        SOUL_PATH: SAMPLE_SOUL,
        PROFILE_PATH: SAMPLE_PROFILE,
        PREFERENCE_PATH: SAMPLE_PREFERENCE,
    })
    tools, store = _tools_with_store(fs)
    with patch('lazymind.chat.engine.tools.memory.MemoryStore', lambda *args, **kwargs: store):
        payload = tools.soul_editor('identity.description', '更直接的助手')

    assert payload['success'] is True
    assert payload['result']['status'] == 'applied'
    assert '更直接的助手' in fs.files[SOUL_PATH]


def test_soul_editor_rejects_missing_field():
    fs = FakeRemoteFS({
        SOUL_PATH: SAMPLE_SOUL,
        PROFILE_PATH: SAMPLE_PROFILE,
        PREFERENCE_PATH: SAMPLE_PREFERENCE,
    })
    tools, store = _tools_with_store(fs)
    with patch('lazymind.chat.engine.tools.memory.MemoryStore', lambda *args, **kwargs: store):
        payload = tools.soul_editor('identity.email', 'x@y.com')
    assert payload['success'] is False
    assert payload['error']['type'] == 'validation'
    assert 'does not exist in soul' in payload['error']['reason']


def test_profile_editor_rejects_new_key():
    fs = FakeRemoteFS({
        SOUL_PATH: SAMPLE_SOUL,
        PROFILE_PATH: SAMPLE_PROFILE,
        PREFERENCE_PATH: SAMPLE_PREFERENCE,
    })
    tools, store = _tools_with_store(fs)
    with patch('lazymind.chat.engine.tools.memory.MemoryStore', lambda *args, **kwargs: store):
        payload = tools.profile_editor('identity.nickname', 'Neo')
    assert payload['success'] is False
    assert payload['error']['type'] == 'validation'
    assert 'does not exist in profile' in payload['error']['reason']


def test_profile_editor_updates_list_field():
    fs = FakeRemoteFS({
        SOUL_PATH: SAMPLE_SOUL,
        PROFILE_PATH: SAMPLE_PROFILE,
        PREFERENCE_PATH: SAMPLE_PREFERENCE,
    })
    tools, store = _tools_with_store(fs)
    with patch('lazymind.chat.engine.tools.memory.MemoryStore', lambda *args, **kwargs: store):
        payload = tools.profile_editor('locale.languages', '["zh-CN","en-US"]')

    assert payload['success'] is True
    assert payload['result']['status'] == 'applied'
    assert 'en-US' in fs.files[PROFILE_PATH]


def test_preference_editor_add_and_delete():
    fs = FakeRemoteFS({
        SOUL_PATH: SAMPLE_SOUL,
        PROFILE_PATH: SAMPLE_PROFILE,
        PREFERENCE_PATH: SAMPLE_PREFERENCE,
    })
    tools, store = _tools_with_store(fs)
    with patch('lazymind.chat.engine.tools.memory.MemoryStore', lambda *args, **kwargs: store):
        added = tools.preference_editor(
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

        deleted = tools.preference_editor('delete', name='pref.response.concise')
        assert deleted['success'] is True
        assert 'pref.response.concise' not in fs.files[PREFERENCE_PATH]
        assert build_reference_path('response-concise') not in fs.files
