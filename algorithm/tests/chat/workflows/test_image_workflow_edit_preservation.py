from __future__ import annotations

from pathlib import Path

import yaml


def _load_state() -> dict:
    repo_root = Path(__file__).resolve().parents[4]
    path = repo_root / 'workflows' / 'image-workflow' / 'scenario' / 'state.yml'
    return yaml.safe_load(path.read_text(encoding='utf-8'))


def test_optimize_prompt_requires_a_narrow_scoped_edit_instruction():
    prompt = _load_state()['steps']['optimize_prompt']['prompt']

    assert 'The original user request is the source of truth' in prompt
    assert 'Use the smallest possible edit scope' in prompt
    assert 'modify only that target region' in prompt
    assert 'instead of inventing extra changes' in prompt
    for clause in ('Requested edit:', 'Edit scope:', 'Preserve:', 'Do not:'):
        assert clause in prompt


def test_enhance_prompt_protects_unrequested_image_content():
    enhance = _load_state()['steps']['enhance_image']
    prompt = enhance['prompt']

    assert 'Treat the original user request as authoritative' in prompt
    assert 'Never regenerate or reinterpret the entire image for a local edit' in prompt
    assert 'If any detail is ambiguous, preserve the original rather than guessing' in prompt
    assert 'Pass exactly one resolved source image URL' in prompt
    assert 'Call image_editor (NOT image_generator) exactly once' in prompt
    for protected_property in (
        'identity/facial details', 'background', 'composition', 'lighting',
        'existing text/logos', 'resolution',
    ):
        assert protected_property in prompt
    for clause in ('Requested edit:', 'Edit scope:', 'Preserve:', 'Do not:'):
        assert clause in prompt


def test_enhance_acceptance_criteria_requires_edit_scope_and_preservation():
    criteria = _load_state()['steps']['enhance_image']['acceptance_criteria']

    assert 'smallest sufficient edit scope' in criteria
    assert 'protect all unrequested' in criteria


def test_image_workflow_fails_closed_on_missing_route_or_edit_source():
    steps = _load_state()['steps']
    collect = steps['collect_materials']['prompt']
    optimize = steps['optimize_prompt']['prompt']
    enhance = steps['enhance_image']['prompt']

    assert 'ROUTING INPUT GUARD' in collect
    assert 'not save material_summary' in collect.lower()
    assert 'never turn a failure report into a successful output' in collect
    assert 'Never infer a meme route from stale examples or prior tasks' in optimize
    assert 'If raw_source_image is absent' in optimize
    assert 'Never save a BLOCKED/failure message into enhanced_image_output' in enhance
