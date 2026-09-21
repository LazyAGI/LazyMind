from __future__ import annotations

import json

from lazymind.chat.engine.tools.skill_listing import (
    append_loaded_skill_invocations,
    compose_prompt_skills,
    core_skill_search,
)


def test_compose_prompt_skills_keeps_injected_catalog_and_excludes_denied() -> None:
    prompt, manager = compose_prompt_skills(
        ['lab/new', 'lab/denied'],
        ['lab/new', 'lab/denied', 'lab/old', 'lab/extra'],
        excluded=('lab/denied',),
    )
    assert prompt == ['lab/new']
    assert 'lab/denied' not in manager
    assert 'lab/extra' in manager
    assert 'lab/old' in manager


def test_core_skill_search_requires_backend_and_returns_hits(monkeypatch) -> None:
    captured = {}

    def fake_post(path, payload):
        captured['path'] = path
        captured['payload'] = payload
        return {
            'response': {
                'skills': [
                    {'skill_key': 'lab/special', 'name': 'special', 'description': 'narrow case'},
                ],
            },
        }

    monkeypatch.setattr(
        'lazymind.chat.engine.tools.infra.core_api_client.post_core_api',
        fake_post,
    )
    result = core_skill_search({'query': 'invoice', 'allowed_skill_keys': ['lab/special']})
    assert captured['path'] == '/internal/skills:search'
    assert result['status'] == 'ok'
    assert result['skills'][0]['skill_key'] == 'lab/special'


def test_core_skill_search_wraps_backend_errors(monkeypatch) -> None:
    monkeypatch.setattr(
        'lazymind.chat.engine.tools.infra.core_api_client.post_core_api',
        lambda *args: (_ for _ in ()).throw(RuntimeError('down')),
    )
    result = core_skill_search({'query': 'x'})
    assert result['status'] == 'error'
    assert result['skills'] == []


def test_direct_l2_enters_history_and_filters_denied_skills():
    content = '---\nname: paper\ntags: [academic]\n---\nFollow the writing steps.\n'
    loaded = [{'skill_key': 'external/paper', 'revision_id': 'rev1', 'content': content}]
    messages = append_loaded_skill_invocations([], loaded)
    payload = json.loads(messages[1]['content'])
    assert payload['content'] == content
    assert append_loaded_skill_invocations([], loaded, excluded=['external/paper']) == []
    assert append_loaded_skill_invocations([], [{'skill_key': 'external/paper', 'content': ''}]) == []
