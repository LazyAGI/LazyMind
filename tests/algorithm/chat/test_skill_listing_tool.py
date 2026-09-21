from __future__ import annotations

import json

from lazymind.chat.engine.tools.skill_listing import (
    build_list_skills_tool,
    build_search_skills_tool,
    compose_prompt_skills,
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


def test_list_skills_returns_injected_catalog_only() -> None:
    tool = build_list_skills_tool(['personal/a', ' personal/a ', '', 'shared/b'])
    assert tool() == {
        'status': 'ok',
        'count': 2,
        'skills': ['personal/a', 'shared/b'],
    }


def test_search_skills_requires_query(monkeypatch) -> None:
    tool = build_search_skills_tool(
        searchable_skills=['lab/a'],
        injected_skills=['lab/a'],
    )
    assert tool('')['status'] == 'error'


def test_search_skills_returns_core_hits(monkeypatch) -> None:
    captured = {}

    def fake_post(path, payload):
        captured['path'] = path
        captured['payload'] = payload
        return {
            'response': {
                'skills': [
                    {'skill_key': 'lab/special', 'name': 'special', 'description': 'narrow case'},
                    {'skill_key': 'lab/injected', 'name': 'injected', 'description': 'already listed'},
                ],
            },
        }

    monkeypatch.setattr(
        'lazymind.chat.engine.tools.infra.core_api_client.post_core_api',
        fake_post,
    )
    tool = build_search_skills_tool(
        searchable_skills=['lab/special', 'lab/injected'],
        injected_skills=['lab/injected'],
    )
    result = tool('invoice', limit=2)
    assert result['status'] == 'ok'
    assert captured['path'] == '/internal/skills:search'
    assert captured['payload']['exclude'] == []
    assert captured['payload']['allowed_skill_keys'] == ['lab/injected', 'lab/special']
    assert result['skills'] == [{
        'skill_key': 'lab/special',
        'name': 'special',
        'description': 'narrow case',
    }, {'skill_key': 'lab/injected', 'name': 'injected', 'description': 'already listed'}]


def test_empty_search_scope_cannot_expose_core_hits(monkeypatch):
    monkeypatch.setattr(
        'lazymind.chat.engine.tools.infra.core_api_client.post_core_api',
        lambda *args: {'response': {'skills': [{'skill_key': 'lab/private'}]}},
    )
    tool = build_search_skills_tool(searchable_skills=[], injected_skills=[])
    assert tool('anything')['skills'] == []


def test_structured_discovery_returns_l1_without_search_metadata(monkeypatch):
    from lazymind.chat.engine.tools.skill_listing import build_discover_skill_by_field_tool
    requests = []
    def post(path, body):
        requests.append(body)
        return {'response': {'skills': [
            {'skill_key': 'external/paper', 'name': 'paper', 'description': 'Write papers',
             'field': 'writing', 'aliases': ['private-index-word']},
        ]}}
    monkeypatch.setattr('lazymind.chat.engine.tools.infra.core_api_client.post_core_api', post)
    tool = build_discover_skill_by_field_tool(searchable_skills=['external/paper'])
    result = tool('tags', ['academic'])
    assert requests[0]['field'] == 'tags'
    assert requests[0]['value'] == ['academic']
    assert result['skills'] == [{'skill_key': 'external/paper', 'name': 'paper', 'description': 'Write papers'}]
    assert tool('category', 'external')['status'] == 'error'


def test_direct_l2_enters_history_and_filters_denied_skills():
    from lazymind.chat.engine.tools.skill_listing import append_loaded_skill_invocations
    content = '---\nname: paper\ntags: [academic]\n---\nFollow the writing steps.\n'
    loaded = [{'skill_key': 'external/paper', 'revision_id': 'rev1', 'content': content}]
    messages = append_loaded_skill_invocations([], loaded)
    payload = json.loads(messages[1]['content'])
    assert payload['content'] == content
    assert append_loaded_skill_invocations([], loaded, excluded=['external/paper']) == []
    assert append_loaded_skill_invocations([], [{'skill_key': 'external/paper', 'content': ''}]) == []
