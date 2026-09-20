"""Validation must fail closed; provider errors must never enter public evidence."""

import json
from unittest.mock import Mock

import pytest

from evo import llm, validation
from evo.operations.eval import judge
from test_evolution_contracts import model_config


@pytest.fixture
def reference_case(monkeypatch):
    response = json.dumps({
        'question': 'When does the Aurora test library open?',
        'answer': '09:00',
        'grading_guidance': 'The opening time must be 09:00.',
        'reasoning_steps': ['Read the opening time in the supplied source.'],
        'difficulty_rationale': 'The time is explicitly stated.',
        'type_rationale': 'Only one source is needed.',
    })
    monkeypatch.setattr(llm, 'LazyLLMClient', lambda **kwargs: Mock(return_value=response))
    return validation._dataset(model_config())


@pytest.fixture
def judge_model(monkeypatch):
    client = Mock(return_value=json.dumps({
        **dict.fromkeys(judge.SCORE_KEYS, 1.0),
        'failure_type': 'none', 'reason': 'The answer matches the source.', 'defect': '',
    }))
    monkeypatch.setattr(judge, 'LazyLLMClient', lambda **kwargs: client)
    return client


@pytest.mark.parametrize('omitted_reference', [None, 'reference_chunk_ids', 'reference_doc_ids'])
def test_reference_answer_passes_evaluation_with_retrieval_evidence(reference_case, judge_model, omitted_reference):
    assert reference_case['reference_chunk_ids']
    assert reference_case['reference_doc_ids']
    if omitted_reference:
        reference_case.pop(omitted_reference)

    validation._evaluate(model_config(), reference_case)

    prompt = judge_model.call_args.args[0]
    payload = json.loads(prompt.split('case_json: ', 1)[1])
    assert payload['retrieved_contexts'] == reference_case['reference_context'] == [validation.SOURCE]


@pytest.mark.parametrize('response, error', [
    (json.dumps({**dict.fromkeys(judge.SCORE_KEYS, 0.0), 'failure_type': 'wrong_answer',
                 'reason': 'The answer is incorrect.', 'defect': 'Incorrect answer.'}), 'reference_answer_rejected'),
    ('{}', 'evaluation_contract'),
])
def test_reference_evaluation_still_rejects_bad_or_invalid_judgments(reference_case, judge_model, response, error):
    judge_model.return_value = response
    with pytest.raises(ValueError, match=error):
        validation._evaluate(model_config(), reference_case)


def test_real_evaluation_still_rejects_missing_retrieval_evidence(reference_case, judge_model):
    result = judge.judge_case(reference_case, {
        'case_id': reference_case['id'], 'status': 'ok', 'answer': reference_case['answer'],
        'sources': [], 'trace_id': '',
    }, {}, model_config())
    assert result['retrieval_failure_type'] == 'retrieval_miss'
    assert result['quality_label'] == 'partial'
    assert result['is_correct'] is False


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
