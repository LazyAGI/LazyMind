from __future__ import annotations

from dataclasses import dataclass
from types import MappingProxyType


@dataclass(frozen=True)
class OutputSpec:
    artifact_id: str
    partitioned: bool = False


RUN_CONFIG = 'run.config'
CORPUS_SOURCE_CONFIG = 'corpus.source_config'
EVAL_TARGET_CONFIG = 'eval.target_config'
EVAL_POLICY = 'eval.policy'
REPAIR_POLICY = 'repair.policy'
ABTEST_CANDIDATE_CONFIG = 'abtest.candidate_config'

CORPUS_REPORT = 'corpus.report'
CORPUS_SNAPSHOT = 'corpus.snapshot'
EVAL_CASE_PREPARATION = 'eval.case_preparation'
EVAL_CASE = 'eval.case'
EVAL_DATASET = 'eval.dataset'
EVAL_RAG_ANSWER = 'eval.rag_answer'
EVAL_JUDGE_RESULT = 'eval.judge_result'
EVAL_SUMMARY = 'eval.summary'
ANALYSIS_TRACE_SUMMARY = 'analysis.trace_summary'
ANALYSIS_CASE_CLASSIFICATION = 'analysis.case_classification'
ANALYSIS_TRACE_CLUSTERS = 'analysis.trace_clusters'
ANALYSIS_SUMMARY = 'analysis.summary'
REPAIR_PLAN = 'repair.plan'
REPAIR_CANDIDATE_WORKSPACE = 'repair.candidate_workspace'
REPAIR_LOOP_RESULT = 'repair.loop_result'
REPAIR_VERIFIED_PATCH = 'repair.verified_patch'
ABTEST_CANDIDATE_SERVICE = 'abtest.candidate_service'
ABTEST_CANDIDATE_RAG_ANSWER = 'abtest.candidate_rag_answer'
ABTEST_CANDIDATE_JUDGE_RESULT = 'abtest.candidate_judge_result'
ABTEST_CANDIDATE_EVAL_SUMMARY = 'abtest.candidate_eval_summary'
ABTEST_COMPARISON = 'abtest.comparison'

STEPS = ('dataset', 'eval', 'analysis', 'repair', 'abtest')

SEEDS = (
    RUN_CONFIG,
    CORPUS_SOURCE_CONFIG,
    EVAL_TARGET_CONFIG,
    EVAL_POLICY,
    REPAIR_POLICY,
    ABTEST_CANDIDATE_CONFIG,
)

ROOTS = MappingProxyType({
    'dataset': EVAL_DATASET,
    'eval': EVAL_SUMMARY,
    'analysis': ANALYSIS_SUMMARY,
    'repair': REPAIR_VERIFIED_PATCH,
    'abtest': ABTEST_COMPARISON,
})

OUTPUTS = MappingProxyType({
    'dataset': (
        OutputSpec(CORPUS_REPORT),
        OutputSpec(CORPUS_SNAPSHOT),
        OutputSpec(EVAL_CASE_PREPARATION, True),
        OutputSpec(EVAL_CASE, True),
        OutputSpec(EVAL_DATASET),
    ),
    'eval': (
        OutputSpec(EVAL_RAG_ANSWER, True),
        OutputSpec(EVAL_JUDGE_RESULT, True),
        OutputSpec(EVAL_SUMMARY),
    ),
    'analysis': (
        OutputSpec(ANALYSIS_TRACE_SUMMARY, True),
        OutputSpec(ANALYSIS_CASE_CLASSIFICATION, True),
        OutputSpec(ANALYSIS_TRACE_CLUSTERS),
        OutputSpec(ANALYSIS_SUMMARY),
    ),
    'repair': (
        OutputSpec(REPAIR_PLAN),
        OutputSpec(REPAIR_CANDIDATE_WORKSPACE),
        OutputSpec(REPAIR_LOOP_RESULT),
        OutputSpec(REPAIR_VERIFIED_PATCH),
    ),
    'abtest': (
        OutputSpec(ABTEST_CANDIDATE_SERVICE),
        OutputSpec(ABTEST_CANDIDATE_RAG_ANSWER, True),
        OutputSpec(ABTEST_CANDIDATE_JUDGE_RESULT, True),
        OutputSpec(ABTEST_CANDIDATE_EVAL_SUMMARY),
        OutputSpec(ABTEST_COMPARISON),
    ),
})

READ_CASE = MappingProxyType({
    'dataset_case': EVAL_CASE,
    'eval_answer': EVAL_RAG_ANSWER,
    'eval_judge': EVAL_JUDGE_RESULT,
    'analysis_trace': ANALYSIS_TRACE_SUMMARY,
    'analysis_classification': ANALYSIS_CASE_CLASSIFICATION,
    'abtest_answer': ABTEST_CANDIDATE_RAG_ANSWER,
    'abtest_judge': ABTEST_CANDIDATE_JUDGE_RESULT,
})

RERUN_CASE_STAGE = MappingProxyType({
    'dataset': (EVAL_CASE_PREPARATION, EVAL_CASE),
    'eval': (EVAL_RAG_ANSWER, EVAL_JUDGE_RESULT),
    'analysis': (ANALYSIS_TRACE_SUMMARY, ANALYSIS_CASE_CLASSIFICATION),
    'abtest': (ABTEST_CANDIDATE_RAG_ANSWER, ABTEST_CANDIDATE_JUDGE_RESULT),
})


__all__ = [
    'OUTPUTS',
    'READ_CASE',
    'RERUN_CASE_STAGE',
    'ROOTS',
    'SEEDS',
    'STEPS',
    'OutputSpec',
]
