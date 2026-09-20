"""Retrieval identity and complete evaluation evidence must gate acceptance."""

import json
from copy import deepcopy
from unittest.mock import Mock

import pytest

from evo.operations.abtest.comparison import compare_abtest
from evo.operations.dataset.generation import generate_case, prepare_case
from evo.operations.dataset.kb_loader import build_corpus_snapshot
from evo.operations.eval import judge
from evo.operations.public_contracts import build_eval_summary_root
from evo.operations.route.chat_router import ChatStreamState, _normalize


@pytest.fixture
def judge_model(monkeypatch):
    client = Mock(return_value=_scores(1.0))
    monkeypatch.setattr(judge, 'LazyLLMClient', lambda **kwargs: client)
    return client


def _scores(value):
    return json.dumps({
        **dict.fromkeys(judge.SCORE_KEYS, value), 'format_compliance': 1.0,
        'failure_type': 'none', 'reason': 'Test judgment.', 'defect': '',
    })


def _case(case_id='case-1'):
    return {
        'id': case_id, 'question': 'When does the test library open?', 'answer': '09:00',
        'question_type': 'single_hop', 'reference_context': ['The test library opens at 09:00.'],
        'reference_chunk_ids': ['kb-a:doc-a:chunk-1'], 'reference_doc_ids': ['kb-a:doc-a'],
    }


def _answer(case):
    return {
        'status': 'ok', 'answer': case['answer'], 'contexts': case['reference_context'],
        'chunk_ids': case['reference_chunk_ids'], 'doc_ids': case['reference_doc_ids'],
    }


def _judge(case, answer):
    return judge.judge_case(case, answer, {}, {'evo_llm': {'model': 'test-only'}})


@pytest.mark.parametrize('doc, chunk', [
    ('kb-a:doc-b', 'kb-a:doc-b:chunk-1'),
    ('kb-b:doc-a', 'kb-b:doc-a:chunk-1'),
])
def test_same_chunk_suffix_in_another_scope_is_not_a_hit(judge_model, doc, chunk):
    case = _case()
    result = _judge(case, _answer(case) | {'doc_ids': [doc], 'chunk_ids': [chunk]})
    assert result['chunk_recall'] == result['doc_recall'] == 0.0
    assert result['retrieval_failure_type'] == 'retrieval_miss'
    assert result['is_correct'] is False


def test_same_chunk_suffix_in_two_reference_documents_keeps_both_references(judge_model):
    case = _case()
    answer = _answer(case)
    case = case | {
        'reference_chunk_ids': ['kb-a:doc-a:chunk-1', 'kb-a:doc-b:chunk-1'],
        'reference_doc_ids': ['kb-a:doc-a', 'kb-a:doc-b'],
    }
    result = _judge(case, answer)
    assert result['chunk_recall'] == result['doc_recall'] == 0.5
    assert result['retrieval_failure_type'] == 'retrieval_partial'
    assert result['is_correct'] is False


def test_missing_chunks_cannot_fall_back_to_matching_document(judge_model):
    case = _case()
    result = _judge(case, _answer(case) | {'chunk_ids': []})
    assert result['doc_recall'] == 1.0
    assert result['chunk_recall'] == result['context_recall'] == 0.0
    assert result['retrieval_failure_type'] == 'retrieval_miss'
    assert result['is_correct'] is False


def test_document_only_reference_still_requires_matching_knowledge_base(judge_model):
    case = _case() | {'reference_chunk_ids': []}
    result = _judge(case, _answer(case) | {'doc_ids': ['kb-b:doc-a']})
    assert result['doc_recall'] == 0.0
    assert result['retrieval_failure_type'] == 'retrieval_miss'
    assert result['is_correct'] is False


@pytest.mark.parametrize('references, retrieval', [
    ({}, 'none'),
    ({'reference_chunk_ids': [], 'reference_doc_ids': ['kb-a:doc-a']}, 'none'),
    ({'reference_chunk_ids': ['kb-a:doc-a:chunk-1'], 'reference_doc_ids': []}, 'none'),
    ({'reference_chunk_ids': ['fixture-chunk'], 'reference_doc_ids': ['fixture-doc']}, 'none'),
    ({'reference_chunk_ids': [], 'reference_doc_ids': []}, 'not_applicable'),
])
def test_complete_matching_evidence_still_passes(judge_model, references, retrieval):
    case = _case() | references
    result = _judge(case, _answer(case))
    assert result['retrieval_failure_type'] == retrieval
    assert result['is_correct'] is True


def test_generated_dataset_and_router_sources_share_full_retrieval_identity(judge_model):
    source = {'kb_id': 'kb-a', 'doc_id': 'doc-a', 'chunk_id': 'chunk-1',
              'content': 'The test library opens at 09:00.'}
    snapshot = build_corpus_snapshot({'source_units': [source]}, {'kb_id': 'kb-a'})
    config = {'question_type': 'single_hop', 'difficulty': 'easy'}
    row = {'question': 'When does the test library open?', 'answer': '09:00',
           'grading_guidance': 'The opening time must be 09:00.',
           'reasoning_steps': ['Read the opening time.'],
           'difficulty_rationale': 'The time is explicit.', 'type_rationale': 'One source is sufficient.'}
    case = generate_case(config, snapshot, prepare_case(config, snapshot, 'case_0001'),
                         llm_complete=lambda prompt: json.dumps(row))
    answer = _normalize({'kb_id': 'kb-a'}, ChatStreamState(
        frames=[], answer_parts=['09:00'], sources=[source], finished=True,
    ))
    result = _judge(case, answer)
    assert case['reference_chunk_ids'] == answer['chunk_ids'] == ['kb-a:doc-a:chunk-1']
    assert case['reference_doc_ids'] == answer['doc_ids'] == ['kb-a:doc-a']
    assert result['chunk_recall'] == result['doc_recall'] == 1.0
    assert result['is_correct'] is True


@pytest.fixture
def evaluations(judge_model):
    summaries = []
    for score in (0.75, 1.0):
        judge_model.return_value = _scores(score)
        cases = [_case('case-1'), _case('case-2')]
        results = [_judge(case, _answer(case)) for case in cases]
        summaries.append((results, build_eval_summary_root('test-run', results)))
    return summaries


@pytest.mark.parametrize('side', ['baseline', 'candidate'])
@pytest.mark.parametrize('failure', ['infra_failure', 'dataset_contract_error', 'judge_contract_error'])
def test_abtest_rejects_execution_or_contract_failure_on_either_side(evaluations, side, failure):
    results, _ = evaluations[0 if side == 'baseline' else 1]
    case = _case('case-2')
    if failure == 'judge_contract_error':
        failed = judge.judge_contract_error(case, _answer(case), {}, 'Test judge failure.')
    else:
        failed = _judge(case, {'status': 'failed', 'chat_error': {'type': failure, 'message': 'Test failure.'}})
    incomplete = build_eval_summary_root('test-run', [results[0], failed])
    baseline, candidate = evaluations[0][1], evaluations[1][1]
    if side == 'baseline':
        baseline = incomplete
    else:
        candidate = incomplete

    assert incomplete['case_num'] == 2 and incomplete['scored_case_num'] == 1
    result = compare_abtest('test-run', baseline, candidate, {'status': 'ready', 'algorithm_id': 'candidate'})
    assert result['status'] == 'failed'
    assert result['verdict'] == 'reject'
    assert any(side in reason for reason in result['reasons'])


@pytest.mark.parametrize('side', ['baseline', 'candidate'])
@pytest.mark.parametrize('field', ['case_num', 'scored_case_num'])
def test_abtest_rejects_incomplete_summary_counts(evaluations, side, field):
    baseline, candidate = deepcopy(evaluations[0][1]), deepcopy(evaluations[1][1])
    (baseline if side == 'baseline' else candidate)[field] = 1
    result = compare_abtest('test-run', baseline, candidate, {'status': 'ready', 'algorithm_id': 'candidate'})
    assert result['status'] == 'failed'
    assert result['verdict'] == 'reject'


@pytest.mark.parametrize('field, value', [
    ('failure_type', 'judge_contract_error'), ('quality_label', 'infra_failure'),
])
def test_abtest_does_not_trust_complete_counts_when_a_case_failed(evaluations, field, value):
    baseline, candidate = deepcopy(evaluations[0][1]), deepcopy(evaluations[1][1])
    candidate['cases'][1][field] = value
    result = compare_abtest('test-run', baseline, candidate, {'status': 'ready', 'algorithm_id': 'candidate'})
    assert result['status'] == 'failed'
    assert result['verdict'] == 'reject'


def test_abtest_accepts_complete_improvement(evaluations):
    result = compare_abtest('test-run', evaluations[0][1], evaluations[1][1],
                            {'status': 'ready', 'algorithm_id': 'candidate'})
    assert result['status'] == 'completed'
    assert result['verdict'] == 'accept'
    assert result['delta']['overall'] == 0.2


def test_abtest_rejects_complete_evaluation_without_improvement(evaluations):
    result = compare_abtest('test-run', evaluations[1][1], evaluations[0][1],
                            {'status': 'ready', 'algorithm_id': 'candidate'})
    assert result['status'] == 'completed'
    assert result['verdict'] == 'reject'
