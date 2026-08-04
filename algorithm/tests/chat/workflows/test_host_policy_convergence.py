from __future__ import annotations

from unittest.mock import patch


def test_shared_skill_is_default_authority_and_legacy_is_explicit_rollback():
    from lazymind.chat.workflow import decision_policy
    from lazymind.chat.workflow import workflow_manager

    with patch.dict('os.environ', {}, clear=True):
        prompt = workflow_manager._build_mode_guidance('dynamic')
    assert 'Shared Workflow Decision Policy [AUTHORITATIVE]' in prompt
    assert 'Apply the first matching rule' in prompt
    assert 'Rule 0 — Intent capture' not in prompt

    before = decision_policy.legacy_policy_hits
    with patch.dict('os.environ', {decision_policy.POLICY_FLAG: '0'}, clear=True):
        rollback = workflow_manager._build_mode_guidance('dynamic')
    assert 'Current Workflow Execution Policy [AUTHORITATIVE]' in rollback
    assert decision_policy.legacy_policy_hits == before + 1


def test_driver_profile_changes_host_tools_not_runtime_projection_or_lineage():
    from lazymind.chat.workflow import workflow_manager

    projection = {
        'state_version': 7,
        'ready': ['draft'],
        'artifact_lineage': [{'artifact_id': 'a1', 'revision': 3, 'attempt_id': 'at1'}],
    }
    auto = workflow_manager.build_cold_advance_tools('auto')
    dynamic = workflow_manager.build_cold_advance_tools('dynamic')

    assert 'advance_step' not in {tool.__name__ for tool in auto}
    assert 'advance_step' in {tool.__name__ for tool in dynamic}
    assert projection['state_version'] == 7
    assert projection['artifact_lineage'] == [
        {'artifact_id': 'a1', 'revision': 3, 'attempt_id': 'at1'},
    ]


def test_handoff_builder_keeps_established_public_spelling():
    from lazymind.chat.workflow import workflow_manager

    tool = workflow_manager.build_advance_step_and_hand_off_tool('wf', 'step')
    assert tool.__name__ == 'advance_step_and_hand_off'
