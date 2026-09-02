from __future__ import annotations

from pathlib import Path

import pytest

from lazymind.common.memory import (
    load_memory_context,
    truncate_preference_index,
)
from lazymind.common.memory.paths import PREFERENCE_PATH, PROFILE_PATH, SOUL_PATH
from lazymind.common.memory.preference_projection import build_preference_projection
from lazymind.common.memory.store import MemoryStore
from lazymind.common.memory.validation import PreferenceItem, append_preference_item
from lazymind.common.memory.validation.preference import parse_preference_items
from lazymind.config import config as _cfg

SAMPLE_PREFERENCE = 'preferences: []\n'
TIMESTAMP = '2026-07-20T09:30:00+08:00'
PROJECTION_FIXTURES = Path(__file__).parents[2] / 'fixtures' / 'preference_projection'


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


def test_truncate_preference_index_uses_configured_item_and_character_limits():
    content = SAMPLE_PREFERENCE
    for idx in range(4):
        content = append_preference_item(
            content,
            PreferenceItem(
                name=f'pref.configured.{idx}',
                summary='x' * 100,
                ref=f'references/topic.md#item-{idx}',
                created_at=TIMESTAMP,
                updated_at=TIMESTAMP,
            ),
        )

    with _cfg.temp('preference_index_max_items', 2):
        with _cfg.temp('preference_context_max_chars', 5000):
            truncated = truncate_preference_index(content)

    assert truncated.count('- summary:') == 2
    assert 'name:' not in truncated
    assert 'created_at:' not in truncated
    assert 'updated_at:' not in truncated
    assert 'references/topic.md#item-0' in truncated
    assert 'references/topic.md#item-1' in truncated
    assert 'references/topic.md#item-2' not in truncated
    assert len(truncated) <= 5000


def test_projection_matches_shared_golden_fixture_and_character_counts():
    full = (PROJECTION_FIXTURES / 'full.yaml').read_text()
    expected = (PROJECTION_FIXTURES / 'compact.yaml').read_text()
    expected_first_two = (PROJECTION_FIXTURES / 'compact-first-two.yaml').read_text()
    items = parse_preference_items(full)

    complete = build_preference_projection(items, max_items=100, max_chars=5000)
    truncated = build_preference_projection(items, max_items=2, max_chars=5000)

    assert complete.content == expected
    assert complete.full_projection_chars == len(expected)
    assert complete.projected_chars == len(expected)
    assert not complete.projection_truncated
    assert truncated.content == expected_first_two
    assert truncated.projected_chars == len(expected_first_two)
    assert truncated.projected_items == 2
    assert truncated.projection_truncated


def test_load_memory_context_reads_store_without_references():
    fs = FakeRemoteFS({
        SOUL_PATH: (
            'schema_version: 2\n'
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
        ),
        PROFILE_PATH: (
            'schema_version: 2\n'
            'identity:\n'
            '  preferred_name: "Alice"\n'
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
        ),
        PREFERENCE_PATH: (
            'preferences:\n'
            '- name: pref.response.detail\n'
            '  summary: Prefer concise answers.\n'
            '  ref: references/response.md\n'
            f'  created_at: "{TIMESTAMP}"\n'
            f'  updated_at: "{TIMESTAMP}"\n'
        ),
        'memory/users/references/response.md': (
            '---\n'
            'name: response\n'
            'description: detail\n'
            '---\n'
            'long detail body\n'
        ),
    })
    ctx = load_memory_context(MemoryStore(fs))
    assert 'LazyMind' in ctx.soul
    assert 'Alice' in ctx.profile
    assert ctx.preference == (
        'preferences:\n'
        '- summary: Prefer concise answers.\n'
        '  ref: references/response.md\n'
    )
    assert 'long detail body' not in ctx.preference
    assert 'name:' not in ctx.preference
    assert 'created_at:' not in ctx.preference
    assert 'updated_at:' not in ctx.preference

    full_ctx = load_memory_context(
        MemoryStore(fs),
        project_preference=False,
    )
    assert 'created_at:' in full_ctx.preference
    assert full_ctx.preference == fs.files[PREFERENCE_PATH]


def test_load_memory_context_propagates_store_errors():
    class BrokenStore(MemoryStore):
        def read_soul(self):
            raise RuntimeError('backend down')

        def read_profile(self):
            raise RuntimeError('backend down')

        def read_preference(self):
            raise RuntimeError('backend down')

    with pytest.raises(RuntimeError, match='backend down'):
        load_memory_context(BrokenStore(FakeRemoteFS()))
