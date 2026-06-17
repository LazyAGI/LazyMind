"""Evaluation policy: how judge scores translate into quality labels and failure types.

- answer_only: quality from answer metrics only; retrieval recall is informational.
- citation: good answers must cite at least one reference doc/chunk.
- retrieval_diagnostic: citation rules plus zero-recall cases are always bad.
"""
EVALUATION_POLICIES = ('answer_only', 'citation', 'retrieval_diagnostic')
DEFAULT_EVALUATION_POLICY = 'citation'


def validate_evaluation_policy(value: str) -> str:
    policy = str(value or DEFAULT_EVALUATION_POLICY)
    if policy not in EVALUATION_POLICIES:
        raise ValueError(f'unsupported evaluation policy: {policy}')
    return policy


def quality_label(policy, answer_correctness, faithfulness, doc_recall, context_recall) -> str:
    has_recall = doc_recall > 0 or context_recall > 0
    answer_good = answer_correctness >= 0.8 and faithfulness >= 0.8
    answer_bad = answer_correctness < 0.5 or faithfulness < 0.5
    if policy == 'answer_only':
        return 'good' if answer_good else 'bad' if answer_bad else 'partial'
    if answer_good and has_recall:
        return 'good'
    if answer_bad or (policy == 'retrieval_diagnostic' and not has_recall):
        return 'bad'
    return 'bad' if not has_recall else 'partial'


def failure_type(policy, quality, answer_correctness, faithfulness, doc_recall, context_recall) -> str:
    if quality == 'good':
        return 'none'
    if policy != 'answer_only' and doc_recall == 0 and context_recall == 0:
        return 'no_evidence'
    if answer_correctness < 0.5 and (doc_recall == 0 or context_recall == 0):
        return 'retrieval_miss'
    if answer_correctness >= 0.8 and faithfulness < 0.8:
        return 'unsupported_correct_answer'
    return 'faithfulness_issue' if faithfulness < 0.8 and answer_correctness < 0.8 else 'generation_gap'
