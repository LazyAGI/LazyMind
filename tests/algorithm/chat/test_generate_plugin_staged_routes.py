import json

import pytest

from lazymind.chat.api import generate_plugin_staged_routes as staged


def test_skeleton_response_prefers_structured_plugin_object():
    plugin = {
        'id': 'deep-research',
        'name': 'Deep Research',
        'description': 'Research workflow',
        'when_to_use': 'ONLY call this tool for deep research.',
        'slots': [],
        'steps': [{'id': 'internal_kb', 'label': 'Phase 1: Internal KB Retrieval'}],
        'ui': {'tabs': []},
    }

    assert staged._plugin_dict_from_skeleton_response({'plugin': plugin}, 'system') == plugin


def test_skeleton_response_repairs_invalid_legacy_yaml(monkeypatch):
    invalid_yaml = 'steps:\n  - id: internal_kb\n    label: Phase 1: Internal KB Retrieval\n'
    repaired_plugin = {
        'steps': [{'id': 'internal_kb', 'label': 'Phase 1: Internal KB Retrieval'}],
    }
    monkeypatch.setattr(
        staged,
        '_call_llm',
        lambda _prompt: json.dumps({'plugin': repaired_plugin}),
    )

    result = staged._plugin_dict_from_skeleton_response(
        {'plugin_yaml': invalid_yaml},
        'system',
    )

    assert result == repaired_plugin


@pytest.mark.asyncio
async def test_unresolved_ancillary_coverage_does_not_require_confirmation(monkeypatch):
    analysis = {
        'verdict': 'generatable',
        'verdict_code': 'workflow_complete',
        'message': 'One coherent workflow.',
        'candidates': [{
            'id': 'deep_research',
            'name': 'Deep Research Workflow',
            'evidence_paths': ['SKILL.md'],
        }],
        'coverage': {'files': {'SKILL.md': 'workflow_evidence'}},
        'tool_mappings': {},
        'scripts': {},
    }
    monkeypatch.setattr(staged, 'inject_model_config', lambda _config: None)
    monkeypatch.setattr(
        staged,
        '_hierarchical_evidence',
        lambda _package: ('workflow evidence', ['references/large-notes.md']),
    )
    monkeypatch.setattr(staged, '_script_inventory', lambda _package: {})
    monkeypatch.setattr(staged, '_call_llm', lambda _prompt: json.dumps(analysis))
    monkeypatch.setattr(
        'lazymind.chat.service.component.tool_registry.get_all_tool_groups',
        lambda: [],
    )

    response = await staged.analyze_skill(staged.AnalyzeSkillRequest(
        name='deep-research',
        skill_package={
            'files': [
                {'path': 'SKILL.md', 'content': 'workflow'},
                {'path': 'references/large-notes.md', 'content': 'notes'},
            ],
        },
    ))

    assert response.verdict == 'generatable'
    assert response.coverage['files']['references/large-notes.md'] == 'unresolved'
