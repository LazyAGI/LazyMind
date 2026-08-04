from pathlib import Path

import yaml


KIT = Path(__file__).parents[2] / 'skills/workflow-agent-kit'


def test_skill_references_exist_and_cover_required_lifecycle():
    skill = (KIT / 'SKILL.md').read_text()
    for reference in (
        'references/decision-policy.md',
        'references/artifact-and-authoring.md',
        'references/source-to-policy-mapping.md',
    ):
        assert reference in skill
        assert (KIT / reference).is_file()
    for clause in ('Discover and prepare', 'Execute', 'Review and recover', 'authoring'):
        assert clause in skill


def test_host_profiles_cover_contract_capabilities():
    profiles = {
        path.stem: yaml.safe_load(path.read_text())
        for path in (KIT / 'profiles').glob('*.yaml')
    }
    assert set(profiles) == {'default', 'lazymind', 'codex'}
    required = {
        'version', 'profile', 'advance_tools', 'parallel_ready_steps', 'approval',
        'handoff', 'driver', 'synthetic_turn', 'shadow_authority', 'write_tools',
    }
    for name, profile in profiles.items():
        assert required <= set(profile), name
        assert profile['version'] == 'workflow.v1'
    assert 'advance_step_and_hand_off' in profiles['lazymind']['advance_tools']
    assert profiles['codex']['advance_tools'] == ['advance_step']
    assert profiles['codex']['handoff'] is False


def test_mapping_ledger_points_to_current_workflow_sources():
    ledger = (KIT / 'references/source-to-policy-mapping.md').read_text()
    assert 'chat/plugin/' not in ledger
    assert '_trigger_plugin_step' not in ledger
    assert 'chat/workflow/workflow_manager.py' in ledger
