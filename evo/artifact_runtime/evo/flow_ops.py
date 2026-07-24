from __future__ import annotations

from ..kernel import (
    ArtifactInput,
    ArtifactOutput,
    FixedOp,
    StaticPartitions,
    all_to_unpartitioned,
    unpartitioned_to_all,
)

from . import catalog as C


def default_evo_ops(cases: tuple[str, ...]) -> tuple[type[FixedOp], ...]:
    partitions = StaticPartitions(cases)
    case_inputs = {
        'config': ArtifactInput(C.RUN_CONFIG, partition_mapping=unpartitioned_to_all()),
        'snapshot': ArtifactInput(C.CORPUS_SNAPSHOT, partition_mapping=unpartitioned_to_all()),
    }

    class LoadCorpus(FixedOp):
        op_id = 'dataset.load_corpus'
        inputs = {'source_config': ArtifactInput(C.CORPUS_SOURCE_CONFIG)}
        outputs = {'report': ArtifactOutput(C.CORPUS_REPORT)}

    class BuildCorpusSnapshot(FixedOp):
        op_id = 'dataset.build_corpus_snapshot'
        inputs = {
            'report': ArtifactInput(C.CORPUS_REPORT),
            'source_config': ArtifactInput(C.CORPUS_SOURCE_CONFIG),
        }
        outputs = {'snapshot': ArtifactOutput(C.CORPUS_SNAPSHOT)}

    class PrepareCase(FixedOp):
        op_id = 'dataset.prepare_case'
        inputs = case_inputs
        outputs = {'preparation': ArtifactOutput(C.EVAL_CASE_PREPARATION, partitions)}

    class GenerateCase(FixedOp):
        op_id = 'dataset.generate_case'
        inputs = {**case_inputs, 'preparation': ArtifactInput(C.EVAL_CASE_PREPARATION, partition_spec=partitions)}
        outputs = {'case': ArtifactOutput(C.EVAL_CASE, partitions)}

    class AssembleDataset(FixedOp):
        op_id = 'dataset.assemble'
        inputs = {
            'cases': ArtifactInput(
                C.EVAL_CASE,
                partition_spec=partitions,
                partition_mapping=all_to_unpartitioned(),
            )
        }
        outputs = {'dataset': ArtifactOutput(C.ROOTS['dataset'])}

    class EvalAnswer(FixedOp):
        op_id = 'eval.answer'
        inputs = {
            'case': ArtifactInput(C.EVAL_CASE, partition_spec=partitions),
            'dataset': ArtifactInput(C.ROOTS['dataset'], partition_mapping=unpartitioned_to_all()),
            'target_config': ArtifactInput(C.EVAL_TARGET_CONFIG, partition_mapping=unpartitioned_to_all()),
        }
        outputs = {'answer': ArtifactOutput(C.EVAL_RAG_ANSWER, partitions)}

    class EvalJudge(FixedOp):
        op_id = 'eval.judge'
        inputs = {
            'case': ArtifactInput(C.EVAL_CASE, partition_spec=partitions),
            'answer': ArtifactInput(C.EVAL_RAG_ANSWER, partition_spec=partitions),
            'policy': ArtifactInput(C.EVAL_POLICY, partition_mapping=unpartitioned_to_all()),
        }
        outputs = {'judge': ArtifactOutput(C.EVAL_JUDGE_RESULT, partitions)}

    class EvalSummary(FixedOp):
        op_id = 'eval.summary'
        inputs = {
            'judges': ArtifactInput(
                C.EVAL_JUDGE_RESULT,
                partition_spec=partitions,
                partition_mapping=all_to_unpartitioned(),
            )
        }
        outputs = {'summary': ArtifactOutput(C.ROOTS['eval'])}

    class TraceSummary(FixedOp):
        op_id = 'analysis.trace_summary'
        inputs = {
            'case': ArtifactInput(C.EVAL_CASE, partition_spec=partitions),
            'answer': ArtifactInput(C.EVAL_RAG_ANSWER, partition_spec=partitions),
            'eval_summary': ArtifactInput(C.ROOTS['eval'], partition_mapping=unpartitioned_to_all()),
        }
        outputs = {'summary': ArtifactOutput(C.ANALYSIS_TRACE_SUMMARY, partitions)}

    class ClassifyCase(FixedOp):
        op_id = 'analysis.classify_case'
        inputs = {
            'case': ArtifactInput(C.EVAL_CASE, partition_spec=partitions),
            'answer': ArtifactInput(C.EVAL_RAG_ANSWER, partition_spec=partitions),
            'judge': ArtifactInput(C.EVAL_JUDGE_RESULT, partition_spec=partitions),
            'trace': ArtifactInput(C.ANALYSIS_TRACE_SUMMARY, partition_spec=partitions),
        }
        outputs = {'classification': ArtifactOutput(C.ANALYSIS_CASE_CLASSIFICATION, partitions)}

    class TraceClusters(FixedOp):
        op_id = 'analysis.trace_clusters'
        inputs = {
            'classifications': ArtifactInput(
                C.ANALYSIS_CASE_CLASSIFICATION,
                partition_spec=partitions,
                partition_mapping=all_to_unpartitioned(),
            )
        }
        outputs = {'clusters': ArtifactOutput(C.ANALYSIS_TRACE_CLUSTERS)}

    class AnalysisSummary(FixedOp):
        op_id = 'analysis.summary'
        inputs = {
            'classifications': ArtifactInput(
                C.ANALYSIS_CASE_CLASSIFICATION,
                partition_spec=partitions,
                partition_mapping=all_to_unpartitioned(),
            ),
            'clusters': ArtifactInput(C.ANALYSIS_TRACE_CLUSTERS),
        }
        outputs = {'summary': ArtifactOutput(C.ROOTS['analysis'])}

    class BuildRepairPlan(FixedOp):
        op_id = 'repair.plan'
        inputs = {
            'analysis_summary': ArtifactInput(C.ROOTS['analysis']),
            'classifications': ArtifactInput(
                C.ANALYSIS_CASE_CLASSIFICATION,
                partition_spec=partitions,
                partition_mapping=all_to_unpartitioned(),
            ),
            'clusters': ArtifactInput(C.ANALYSIS_TRACE_CLUSTERS),
            'policy': ArtifactInput(C.REPAIR_POLICY),
        }
        outputs = {'plan': ArtifactOutput(C.REPAIR_PLAN)}

    class PrepareWorkspace(FixedOp):
        op_id = 'repair.candidate_workspace'
        inputs = {
            'plan': ArtifactInput(C.REPAIR_PLAN),
            'policy': ArtifactInput(C.REPAIR_POLICY),
        }
        outputs = {'workspace': ArtifactOutput(C.REPAIR_CANDIDATE_WORKSPACE)}

    class RepairLoop(FixedOp):
        op_id = 'repair.loop_result'
        inputs = {
            'plan': ArtifactInput(C.REPAIR_PLAN),
            'workspace': ArtifactInput(C.REPAIR_CANDIDATE_WORKSPACE),
            'cases': ArtifactInput(
                C.EVAL_CASE,
                partition_spec=partitions,
                partition_mapping=all_to_unpartitioned(),
            ),
            'baseline_judges': ArtifactInput(
                C.EVAL_JUDGE_RESULT,
                partition_spec=partitions,
                partition_mapping=all_to_unpartitioned(),
            ),
            'eval_policy': ArtifactInput(C.EVAL_POLICY),
            'candidate_config': ArtifactInput(C.ABTEST_CANDIDATE_CONFIG),
            'policy': ArtifactInput(C.REPAIR_POLICY),
        }
        outputs = {'result': ArtifactOutput(C.REPAIR_LOOP_RESULT)}

    class VerifyRepair(FixedOp):
        op_id = 'repair.verified_patch'
        inputs = {'loop': ArtifactInput(C.REPAIR_LOOP_RESULT)}
        outputs = {'patch': ArtifactOutput(C.ROOTS['repair'])}

    class CandidateService(FixedOp):
        op_id = 'abtest.candidate_service'
        inputs = {
            'config': ArtifactInput(C.ABTEST_CANDIDATE_CONFIG),
            'patch': ArtifactInput(C.ROOTS['repair']),
            'workspace': ArtifactInput(C.REPAIR_CANDIDATE_WORKSPACE),
        }
        outputs = {'service': ArtifactOutput(C.ABTEST_CANDIDATE_SERVICE)}

    class CandidateRagAnswer(FixedOp):
        op_id = 'abtest.candidate_rag_answer'
        inputs = {
            'case': ArtifactInput(C.EVAL_CASE, partition_spec=partitions),
            'service': ArtifactInput(C.ABTEST_CANDIDATE_SERVICE, partition_mapping=unpartitioned_to_all()),
        }
        outputs = {'answer': ArtifactOutput(C.ABTEST_CANDIDATE_RAG_ANSWER, partitions)}

    class CandidateJudge(FixedOp):
        op_id = 'abtest.candidate_judge'
        inputs = {
            'case': ArtifactInput(C.EVAL_CASE, partition_spec=partitions),
            'answer': ArtifactInput(C.ABTEST_CANDIDATE_RAG_ANSWER, partition_spec=partitions),
            'policy': ArtifactInput(C.EVAL_POLICY, partition_mapping=unpartitioned_to_all()),
        }
        outputs = {'judge': ArtifactOutput(C.ABTEST_CANDIDATE_JUDGE_RESULT, partitions)}

    class CandidateSummary(FixedOp):
        op_id = 'abtest.candidate_eval_summary'
        inputs = {
            'judges': ArtifactInput(
                C.ABTEST_CANDIDATE_JUDGE_RESULT,
                partition_spec=partitions,
                partition_mapping=all_to_unpartitioned(),
            )
        }
        outputs = {'summary': ArtifactOutput(C.ABTEST_CANDIDATE_EVAL_SUMMARY)}

    class CompareABTest(FixedOp):
        op_id = 'abtest.compare'
        inputs = {
            'baseline': ArtifactInput(C.ROOTS['eval']),
            'candidate': ArtifactInput(C.ABTEST_CANDIDATE_EVAL_SUMMARY),
            'service': ArtifactInput(C.ABTEST_CANDIDATE_SERVICE),
        }
        outputs = {'comparison': ArtifactOutput(C.ROOTS['abtest'])}

    return (
        LoadCorpus,
        BuildCorpusSnapshot,
        PrepareCase,
        GenerateCase,
        AssembleDataset,
        EvalAnswer,
        EvalJudge,
        EvalSummary,
        TraceSummary,
        ClassifyCase,
        TraceClusters,
        AnalysisSummary,
        BuildRepairPlan,
        PrepareWorkspace,
        RepairLoop,
        VerifyRepair,
        CandidateService,
        CandidateRagAnswer,
        CandidateJudge,
        CandidateSummary,
        CompareABTest,
    )


def dataset_evo_ops(cases: tuple[str, ...]) -> tuple[type[FixedOp], ...]:
    return default_evo_ops(cases)[:5]


def eval_evo_ops(cases: tuple[str, ...]) -> tuple[type[FixedOp], ...]:
    return default_evo_ops(cases)[:8]


def analysis_evo_ops(cases: tuple[str, ...]) -> tuple[type[FixedOp], ...]:
    op_ids = (
        *[op.op_id for op in eval_evo_ops(cases)],
        'analysis.trace_summary',
        'analysis.classify_case',
        'analysis.trace_clusters',
        'analysis.summary',
    )
    ops = {op.op_id: op for op in default_evo_ops(cases)}
    return tuple(ops[op_id] for op_id in op_ids)


def repair_evo_ops(cases: tuple[str, ...]) -> tuple[type[FixedOp], ...]:
    op_ids = (
        *[op.op_id for op in analysis_evo_ops(cases)],
        'repair.plan',
        'repair.candidate_workspace',
        'repair.loop_result',
        'repair.verified_patch',
    )
    ops = {op.op_id: op for op in default_evo_ops(cases)}
    return tuple(ops[op_id] for op_id in op_ids)


def abtest_evo_ops(cases: tuple[str, ...]) -> tuple[type[FixedOp], ...]:
    op_ids = (
        'abtest.candidate_service',
        'abtest.candidate_rag_answer',
        'abtest.candidate_judge',
        'abtest.candidate_eval_summary',
        'abtest.compare',
    )
    ops = {op.op_id: op for op in default_evo_ops(cases)}
    return tuple(ops[op_id] for op_id in op_ids)


__all__ = [
    'abtest_evo_ops',
    'analysis_evo_ops',
    'dataset_evo_ops',
    'default_evo_ops',
    'eval_evo_ops',
    'repair_evo_ops',
]
