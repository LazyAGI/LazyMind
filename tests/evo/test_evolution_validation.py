"""Validation must fail closed; provider errors must never enter public evidence."""

import pytest

from evo import validation
from test_evolution_contracts import model_config


def test_protocol_probes_without_workflow_cannot_admit_model(tmp_path, monkeypatch):
    monkeypatch.setattr(validation, '_planning', lambda config: None)
    monkeypatch.setattr(validation, '_dataset', lambda config: {'id': 'fixture'})
    monkeypatch.setattr(validation, '_evaluate', lambda config, case: None)
    monkeypatch.setattr(validation, '_code_edit', lambda config, root: None)
    report = validation.validate({'llm_config': model_config(), 'model_ref': 'test-ref', 'nonce': 'test-nonce'}, tmp_path)
    assert report['passed'] is False
    assert report['checks']['workflow'] is False
    assert report['failures']['workflow'] == 'isolated_workflow_inputs_required'


@pytest.mark.parametrize('probe', ['_planning', '_dataset', '_evaluate', '_code_edit'])
def test_probe_failure_never_becomes_pass_or_leaks_provider_errors(tmp_path, monkeypatch, probe):
    monkeypatch.setattr(validation, '_planning', lambda config: None)
    monkeypatch.setattr(validation, '_dataset', lambda config: {'id': 'fixture'})
    monkeypatch.setattr(validation, '_evaluate', lambda config, case: None)
    monkeypatch.setattr(validation, '_code_edit', lambda config, root: None)
    def failure(*args):
        raise RuntimeError('upstream api_key=test-secret-private https://internal.example.test')
    monkeypatch.setattr(validation, probe, failure)
    report = validation.validate({'llm_config': model_config(), 'model_ref': 'test-ref', 'nonce': 'test-nonce'}, tmp_path)
    assert report['passed'] is False
    assert 'test-secret-private' not in str(report)
    assert 'internal.example.test' not in str(report)


def test_semantically_wrong_plan_is_rejected(monkeypatch):
    from evo.message_intent.schemas import TurnPlan
    monkeypatch.setattr(validation, 'plan_next_turn', lambda *args: TurnPlan.model_validate({
        'turn_decision': 'next_action', 'next_action': {'kind': 'flow', 'command': 'cancel'},
    }))
    with pytest.raises(ValueError, match='planning_contract'):
        validation._planning(model_config())


def test_swallowed_judge_failure_is_not_capability_evidence(monkeypatch):
    monkeypatch.setattr(validation, 'judge_case', lambda *args: {'failure_type': 'judge_contract_error'})
    with pytest.raises(ValueError, match='evaluation_contract'):
        validation._evaluate(model_config(), {'id': 'fixture', 'answer': 'fixture answer'})
