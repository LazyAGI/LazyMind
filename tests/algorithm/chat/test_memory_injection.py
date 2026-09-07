from __future__ import annotations

from pathlib import Path

import pytest

from lazymind.common.memory import (
    load_memory_context,
    truncate_preference_index,
)
from lazymind.common.memory.paths import PREFERENCE_PATH, PROFILE_PATH, SOUL_PATH
from lazymind.common.memory.preference_projection import build_preference_projection, render_preference_projection
from lazymind.common.memory.store import MemoryStore
from lazymind.common.memory.validation import PreferenceItem, append_preference_item
from lazymind.common.memory.validation.preference import parse_preference_items
from lazymind.config import config as _cfg

SAMPLE_PREFERENCE = 'preferences: []\n'
TIMESTAMP = '2026-07-20T09:30:00+08:00'
PROJECTION_FIXTURES = Path(__file__).parents[2] / 'fixtures' / 'preference_projection'


def test_whitespace_only_preference_does_not_exceed_budget():
    assert truncate_preference_index(' ' * 100, max_chars=1) == ''


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


def test_truncate_preference_index_uses_configured_character_budget():
    content = SAMPLE_PREFERENCE
    for idx in range(4):
        content = append_preference_item(
            content,
            PreferenceItem(
                name=f'pref.configured.{idx}',
                summary='x' * 100,
                ref=f'references/topic-{idx}.md',
                created_at=TIMESTAMP,
                updated_at=TIMESTAMP,
            ),
        )

    with _cfg.temp('preference_context_max_chars', 330):
        truncated = truncate_preference_index(content)

    assert truncated.count('- summary:') == 2
    assert 'name:' not in truncated
    assert 'created_at:' not in truncated
    assert 'updated_at:' not in truncated
    assert 'references/topic-0.md' in truncated
    assert 'references/topic-1.md' in truncated
    assert 'references/topic-2.md' not in truncated
    assert len(truncated) <= 330


@pytest.mark.parametrize('variant', ['', 'edge-'])
def test_projection_matches_shared_golden_fixture_and_character_counts(variant):
    full = (PROJECTION_FIXTURES / f'{variant}full.yaml').read_text()
    expected = (PROJECTION_FIXTURES / f'{variant}compact.yaml').read_text()
    expected_first_two = (PROJECTION_FIXTURES / f'{variant}compact-first-two.yaml').read_text()
    items = parse_preference_items(full)

    complete = build_preference_projection(items, max_chars=5000)
    truncated = build_preference_projection(items, max_chars=len(expected_first_two))

    assert complete.content == expected
    assert complete.full_projection_chars == len(expected)
    assert complete.projected_chars == len(expected)
    assert not complete.projection_truncated
    assert truncated.content == expected_first_two
    assert truncated.projected_chars == len(expected_first_two)
    assert truncated.projected_items == 2
    assert truncated.projection_truncated


def test_projection_has_no_item_limit_and_keeps_a_whole_prefix():
    item = parse_preference_items((PROJECTION_FIXTURES / 'full.yaml').read_text())[0]
    items = [item] * 101
    full = render_preference_projection(items)
    complete = build_preference_projection(items, max_chars=len(full))
    assert complete.projected_items == 101
    assert complete.content == full
    assert not complete.projection_truncated
    truncated = build_preference_projection(items, max_chars=len(full) - 1)
    assert truncated.projected_items == 100
    assert truncated.content == render_preference_projection(items[:100])


@pytest.mark.parametrize('budget', [1, 14, 15, 16])
@pytest.mark.parametrize('nonempty', [False, True])
def test_projection_empty_envelope_obeys_even_tiny_budgets(budget, nonempty):
    items = parse_preference_items((PROJECTION_FIXTURES / 'full.yaml').read_text()) if nonempty else []
    projection = build_preference_projection(items, max_chars=budget)
    assert projection.projected_items == 0
    assert projection.projected_chars == len(projection.content) <= budget
    assert projection.content == ('preferences: []\n' if budget >= 16 else '')
    assert projection.projection_truncated == nonempty


def test_projection_does_not_skip_an_oversized_first_item():
    items = parse_preference_items((PROJECTION_FIXTURES / 'edge-full.yaml').read_text())
    items.sort(key=lambda item: len(item.summary), reverse=True)
    budget = len(render_preference_projection(items[-1:]))
    assert build_preference_projection(items, max_chars=budget).projected_items == 0


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
        '- summary: "Prefer concise answers."\n'
        '  ref: "references/response.md"\n'
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
