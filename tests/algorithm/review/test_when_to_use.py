from __future__ import annotations

from lazymind.review.traj_to_skill.when_to_use import (
    when_to_use_prompt,
    apply_when_to_use_description,
    _normalize_audit,
)


def test_apply_when_to_use_updates_frontmatter_and_section():
    content = (
        '---\n'
        'name: nearby-search\n'
        'description: Search nearby places.\n'
        '---\n'
        '## Steps\n\n- Look around.\n'
    )
    updated = apply_when_to_use_description(
        content,
        'Use when the user wants nearby POI search. Do not use for web research.',
        expected_name='nearby-search',
    )
    assert 'Use when the user wants nearby POI search' in updated
    assert '## When to Use' in updated
    assert 'Do not use for web research' in updated


def test_normalize_audit_keeps_conflict_exclusive_from_patches():
    catalog = {
        'internal/a': {
            'skill_key': 'internal/a',
            'category': 'internal',
            'name': 'a',
            'content': '---\nname: a\ndescription: overlapping mail helper.\n---\nBody.\n',
            'pending': False,
        },
        'internal/b': {
            'skill_key': 'internal/b',
            'category': 'internal',
            'name': 'b',
            'content': '---\nname: b\ndescription: overlapping mail helper.\n---\nBody.\n',
            'pending': False,
        },
    }
    patches, conflicts = _normalize_audit(
        {
            'patches': [
                {
                    'skill_key': 'internal/a',
                    'when_to_use': 'Use for inbox triage only.',
                    'reason': 'narrow a',
                }
            ],
            'conflicts': [
                {
                    'id': 'mail-overlap',
                    'skill_keys': ['internal/a', 'internal/b'],
                    'reason': 'still overlap',
                    'mention_only_when_to_use': {},
                }
            ],
        },
        catalog,
    )
    assert patches[0]['skill_key'] == 'internal/a'
    assert conflicts == []


def test_conflicts_do_not_generate_invocation_policy():
    catalog = {
        'internal/a': {
            'skill_key': 'internal/a',
            'category': 'internal',
            'name': 'a',
            'content': '---\nname: a\ndescription: overlapping mail helper.\n---\nBody.\n',
            'pending': False,
        },
        'internal/b': {
            'skill_key': 'internal/b',
            'category': 'internal',
            'name': 'b',
            'content': '---\nname: b\ndescription: overlapping mail helper.\n---\nBody.\n',
            'pending': False,
        },
    }
    patches, conflicts = _normalize_audit(
        {
            'patches': [],
            'conflicts': [
                {
                    'id': 'mail-overlap',
                    'skill_keys': ['internal/a', 'internal/b'],
                    'reason': 'same inbox intent',
                    'mention_only_when_to_use': {},
                }
            ],
        },
        catalog,
    )
    assert patches == []
    assert len(conflicts) == 1
    assert conflicts[0]['skill_keys'] == ['internal/a', 'internal/b']
    assert 'mention_only_when_to_use' not in conflicts[0]['skills'][0]
    assert 'mention_only' not in when_to_use_prompt([])
    assert '@-mentions' not in when_to_use_prompt([])
