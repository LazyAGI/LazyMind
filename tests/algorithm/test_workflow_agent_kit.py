from pathlib import Path

import yaml


KIT = Path(__file__).resolve().parents[2] / 'skills/workflow-agent-kit'


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
