from pathlib import Path

import yaml


KIT = Path(__file__).resolve().parents[2] / 'skills/workflow-agent-kit'


def test_skill_references_exist_and_cover_required_lifecycle():
    skill = (KIT / 'SKILL.md').read_text()
    for reference in (
        'references/installation-and-connection.md',
        'references/model-execution-boundary.md',
        'references/lifecycle.md',
        'references/decision-policy.md',
        'references/execution-policy.md',
        'references/artifact-policy.md',
        'references/recovery-policy.md',
        'references/skill-to-workflow.md',
        'references/workflow-format.md',
        'references/tool-contracts.md',
        'references/source-to-policy-mapping.md',
    ):
        assert reference in skill
        assert (KIT / reference).is_file()


def test_host_profiles_cover_contract_capabilities():
    profiles = {
        path.stem: yaml.safe_load(path.read_text())
        for path in (KIT / 'profiles').glob('*.yaml')
    }
    assert set(profiles) == {'default', 'lazymind', 'codex'}
    required = {
        'version', 'profile', 'advance_tools', 'parallel_ready_steps', 'approval',
        'handoff', 'driver', 'synthetic_turn', 'shadow_authority', 'write_tools',
        'workflow_tools',
    }
    for name, profile in profiles.items():
        assert required <= set(profile), name
        assert profile['version'] == 'workflow.v1'
    assert 'advance_step_and_hand_off' in profiles['lazymind']['advance_tools']
    assert profiles['lazymind']['driver'] is True
    assert profiles['codex']['driver'] is False
    assert profiles['codex']['advance_tools'] == ['advance_step']
    assert profiles['codex']['handoff'] is False
    assert 'workflow_connection_status' in profiles['codex']['workflow_tools']
    assert 'advance_step_and_hand_off' not in profiles['codex']['workflow_tools']
    assert all('prepare_workflow' not in profile['workflow_tools'] for profile in profiles.values())
    assert all(profile['shadow_authority'] == 'shared' for profile in profiles.values())
