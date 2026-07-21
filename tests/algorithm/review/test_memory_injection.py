from __future__ import annotations

from lazymind.review.memory_review.defaults import default_preference_md
from lazymind.review.memory_review.injection import (
    load_chat_memory_context,
    profile_languages,
    truncate_preference_index,
)
from lazymind.review.memory_review.schema import PreferenceItem, append_preference_item
from lazymind.review.memory_review.store import MemoryStore


class FakeRemoteFS:
    def __init__(self, files=None):
        self.files = dict(files or {})
        self.dirs = set()

    def exists(self, path: str) -> bool:
        return path.strip('/') in self.files or path.strip('/') in self.dirs

    def write(self, path: str, content: str, content_type: str = 'text/plain; charset=utf-8') -> None:
        normalized = path.strip('/')
        self.files[normalized] = content
        self.dirs.add(normalized.rsplit('/', 1)[0])

    def open(self, path: str, mode: str = 'rb', **kwargs):
        normalized = path.strip('/')
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

    def ls(self, path: str, detail: bool = True):
        return []

    def makedirs(self, path: str, exist_ok: bool = True) -> None:
        self.dirs.add(path.strip('/'))


def test_truncate_preference_index_keeps_frontmatter_and_cap():
    content = default_preference_md()
    for idx in range(105):
        content = append_preference_item(
            content,
            PreferenceItem(
                name=f'pref.item.{idx}',
                summary=f'summary {idx}',
                ref=f'references/topic.md#item-{idx}',
            ),
        )
    truncated = truncate_preference_index(content, max_items=100)
    assert truncated.count('- name:') == 100
    assert 'schema_version: 1' in truncated
    assert 'pref.item.0' in truncated
    assert 'pref.item.99' in truncated
    assert 'pref.item.100' not in truncated


def test_profile_languages():
    profile = (
        '---\n'
        'schema_version: 1\n'
        'locale:\n'
        '  languages: ["zh-CN", "en-US"]\n'
        '---\n'
    )
    assert profile_languages(profile) == ['zh-CN', 'en-US']


def test_load_chat_memory_context_reads_store_without_references():
    fs = FakeRemoteFS({
        'memory/agents/soul.md': (
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
        ),
        'memory/users/profile.md': (
            '---\n'
            'schema_version: 1\n'
            'identity:\n'
            '  preferred_name: "Alice"\n'
            '---\n'
        ),
        'memory/users/preference.md': (
            '---\n'
            'schema_version: 1\n'
            'updated_at: 2026-07-20\n'
            '---\n'
            '# Preference Index\n'
            '- name: pref.response.detail\n'
            '  summary: Prefer concise answers.\n'
            '  ref: references/response.md\n'
        ),
        'memory/users/references/response.md': (
            '---\n'
            'name: response\n'
            'description: detail\n'
            '---\n'
            'long detail body\n'
        ),
    })
    ctx = load_chat_memory_context(MemoryStore(fs))
    assert 'LazyMind' in ctx.soul
    assert 'Alice' in ctx.profile
    assert 'pref.response.detail' in ctx.preference
    assert 'long detail body' not in ctx.preference


def test_load_chat_memory_context_falls_back_on_store_errors():
    class BrokenStore(MemoryStore):
        def read_soul(self):
            raise RuntimeError('backend down')

        def read_profile(self):
            raise RuntimeError('backend down')

        def read_preference(self):
            raise RuntimeError('backend down')

    ctx = load_chat_memory_context(BrokenStore(FakeRemoteFS()))
    assert 'schema_version: 1' in ctx.soul
    assert 'schema_version: 1' in ctx.profile
    assert '# Preference Index' in ctx.preference
