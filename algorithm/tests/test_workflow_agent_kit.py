from pathlib import Path

import yaml


KIT = Path(__file__).parents[2] / 'skills/workflow-agent-kit'


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
    for clause in ('Discover a Workflow', 'Convert a Skill', 'Start a Workflow',
                   'Advance steps', 'Inspect inputs and Artifacts', 'controller/UI-only'):
        assert clause in skill


def test_skill_is_the_complete_model_free_workflow_operating_procedure():
    skill = (KIT / 'SKILL.md').read_text()
    for tool in (
        'list_workflows', 'get_workflow', 'get_skill_conversion_context',
        'create_workflow_draft', 'trigger_<workflow>_workflow',
        'get_ready_steps', 'advance_step', 'list_workflow_inputs',
        'list_artifacts', 'read_artifact', 'patch_artifact',
    ):
        assert tool in skill
    assert 'Infrastructure tools must never call a model' in skill
    assert 'explicit SubAgent' in skill


def test_skill_skips_catalog_listing_when_host_exposes_bound_trigger():
    skill = (KIT / 'SKILL.md').read_text()
    assert 'Call the matching trigger directly instead of' in skill
    assert 'creates the Session when inputs are sufficient' in skill
    assert 'It never advances a step' in skill
    assert 'prepare_workflow' not in skill


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


def test_skill_to_workflow_covers_complete_authoring_tool_chain():
    policy = (KIT / 'references' / 'skill-to-workflow.md').read_text()
    for tool in (
        'get_skill_conversion_context',
        'create_workflow_draft',
        'update_workflow_draft_file',
        'validate_workflow_draft',
        'get_workflow_diagnostics',
        'publish_workflow',
    ):
        assert tool in policy
    boundary = (KIT / 'references' / 'model-execution-boundary.md').read_text()
    assert 'Only execution of a Workflow step may invoke another model' in boundary
    assert 'No other tool may hide' in boundary
    format_policy = (KIT / 'references' / 'workflow-format.md').read_text()
    for path in ('workflow.yaml', 'scenario/state.yml', 'scenario/scenario.md'):
        assert path in format_policy


def test_host_adapters_keep_runtime_rules_out_of_host_capabilities():
    for host in ('lazymind', 'codex'):
        adapter = KIT / 'adapters' / f'{host}.md'
        assert adapter.is_file()
        content = adapter.read_text()
        assert 'Supervisor' in content


def test_mapping_ledger_points_to_current_workflow_sources():
    ledger = (KIT / 'references/source-to-policy-mapping.md').read_text()
    assert 'chat/plugin/' not in ledger
    assert '_trigger_workflow_step' not in ledger
    assert 'chat/workflow/workflow_manager.py' in ledger
