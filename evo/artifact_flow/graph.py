from __future__ import annotations

from evo.artifact_runtime import (
    ArtifactInput,
    ArtifactOutput,
    DAGGraph,
    FixedOp,
    StaticPartitions,
    all_to_unpartitioned,
    unpartitioned_to_all,
)
from evo.operations import artifact_business as business


def build_evo_graph(case_ids: tuple[str, ...]) -> DAGGraph:
    partitions = StaticPartitions(case_ids)
    graph = DAGGraph()
    for op in _ops(partitions):
        graph.register(op)
    graph.validate()
    return graph


def _ops(partitions: StaticPartitions) -> tuple[type[FixedOp], ...]:
    class LoadCorpus(FixedOp):
        op_id = 'dataset.load_corpus'
        inputs = {'source_config': ArtifactInput('corpus.source_config')}
        outputs = {'report': ArtifactOutput('corpus.report')}
        flow, stage = 'dataset', 'corpus'

        @classmethod
        def execute(cls, inputs, ctx):
            return {'report': business.load_corpus(inputs['source_config'].payload)}

    class BuildCorpusSnapshot(FixedOp):
        op_id = 'dataset.build_corpus_snapshot'
        inputs = {
            'report': ArtifactInput('corpus.report'),
            'source_config': ArtifactInput('corpus.source_config'),
        }
        outputs = {'snapshot': ArtifactOutput('corpus.snapshot')}
        flow, stage = 'dataset', 'corpus'

        @classmethod
        def execute(cls, inputs, ctx):
            return {
                'snapshot': business.build_corpus_snapshot(
                    inputs['report'].payload,
                    inputs['source_config'].payload,
                )
            }

    class PrepareCase(FixedOp):
        op_id = 'dataset.prepare_case'
        inputs = {
            'config': ArtifactInput('run.config', partition_mapping=unpartitioned_to_all()),
            'snapshot': ArtifactInput('corpus.snapshot', partition_mapping=unpartitioned_to_all()),
        }
        outputs = {'preparation': ArtifactOutput('eval.case_preparation', partition_spec=partitions)}
        flow, stage = 'dataset', 'prepare_case'

        @classmethod
        def execute(cls, inputs, ctx):
            return {
                'preparation': business.prepare_case(
                    inputs['config'].payload,
                    inputs['snapshot'].payload,
                    ctx.output_partition,
                )
            }

    class GenerateCase(FixedOp):
        op_id = 'dataset.generate_case'
        inputs = {'preparation': ArtifactInput('eval.case_preparation', partition_spec=partitions)}
        outputs = {'case': ArtifactOutput('eval.case', partition_spec=partitions)}
        flow, stage = 'dataset', 'generate_case'

        @classmethod
        def execute(cls, inputs, ctx):
            return {'case': business.generate_case(inputs['preparation'].payload)}

    class AssembleDataset(FixedOp):
        op_id = 'dataset.assemble'
        inputs = {'cases': ArtifactInput('eval.case', partition_spec=partitions,
                                         partition_mapping=all_to_unpartitioned())}
        outputs = {'dataset': ArtifactOutput('eval.dataset')}
        flow, stage = 'dataset', 'assemble'

        @classmethod
        def execute(cls, inputs, ctx):
            return {'dataset': business.assemble_dataset(inputs['cases'])}

    class RagAnswer(FixedOp):
        op_id = 'eval.rag_answer'
        inputs = {
            'case': ArtifactInput('eval.case', partition_spec=partitions),
            'target_config': ArtifactInput('eval.target_config', partition_mapping=unpartitioned_to_all()),
        }
        outputs = {'answer': ArtifactOutput('eval.rag_answer', partition_spec=partitions)}
        flow, stage = 'eval', 'rag_answer'

        @classmethod
        def execute(cls, inputs, ctx):
            return {'answer': business.rag_answer(inputs['case'].payload, inputs['target_config'].payload, ctx)}

    class JudgeAnswer(FixedOp):
        op_id = 'eval.judge_answer'
        inputs = {
            'answer': ArtifactInput('eval.rag_answer', partition_spec=partitions),
            'policy': ArtifactInput('eval.policy', partition_mapping=unpartitioned_to_all()),
        }
        outputs = {'judge': ArtifactOutput('eval.judge_result', partition_spec=partitions)}
        flow, stage = 'eval', 'judge_answer'

        @classmethod
        def execute(cls, inputs, ctx):
            return {'judge': business.judge_answer(inputs['answer'].payload, inputs['policy'].payload)}

    class EvalSummary(FixedOp):
        op_id = 'eval.summary'
        inputs = {'judges': ArtifactInput('eval.judge_result', partition_spec=partitions,
                                          partition_mapping=all_to_unpartitioned())}
        outputs = {'summary': ArtifactOutput('eval.summary')}
        flow, stage = 'eval', 'summary'

        @classmethod
        def execute(cls, inputs, ctx):
            return {'summary': business.eval_summary(inputs['judges'])}

    class ClassifyCase(FixedOp):
        op_id = 'analysis.classify_case'
        inputs = {
            'case': ArtifactInput('eval.case', partition_spec=partitions),
            'answer': ArtifactInput('eval.rag_answer', partition_spec=partitions),
            'judge': ArtifactInput('eval.judge_result', partition_spec=partitions),
        }
        outputs = {'classification': ArtifactOutput('analysis.case_classification', partition_spec=partitions)}
        flow, stage = 'analysis', 'classification'

        @classmethod
        def execute(cls, inputs, ctx):
            return {
                'classification': business.classify_case(
                    inputs['case'].payload,
                    inputs['answer'].payload,
                    inputs['judge'].payload,
                )
            }

    class AnalysisSummary(FixedOp):
        op_id = 'analysis.summary'
        inputs = {
            'classifications': ArtifactInput(
                'analysis.case_classification',
                partition_spec=partitions,
                partition_mapping=all_to_unpartitioned(),
            )
        }
        outputs = {'summary': ArtifactOutput('analysis.summary')}
        flow, stage = 'analysis', 'summary'

        @classmethod
        def execute(cls, inputs, ctx):
            return {'summary': business.analysis_summary(inputs['classifications'])}

    class BuildRepairPlan(FixedOp):
        op_id = 'repair.plan'
        inputs = {'analysis': ArtifactInput('analysis.summary'), 'policy': ArtifactInput('repair.policy')}
        outputs = {'plan': ArtifactOutput('repair.plan')}
        flow, stage = 'repair', 'plan'

        @classmethod
        def execute(cls, inputs, ctx):
            return {'plan': business.repair_plan(inputs['analysis'].payload, inputs['policy'].payload)}

    class PrepareWorkspace(FixedOp):
        op_id = 'repair.candidate_workspace'
        inputs = {'plan': ArtifactInput('repair.plan')}
        outputs = {'workspace': ArtifactOutput('repair.candidate_workspace')}
        flow, stage = 'repair', 'workspace'

        @classmethod
        def execute(cls, inputs, ctx):
            return {'workspace': business.candidate_workspace(inputs['plan'].payload, ctx)}

    class RepairLoop(FixedOp):
        op_id = 'repair.loop_result'
        inputs = {'workspace': ArtifactInput('repair.candidate_workspace')}
        outputs = {'result': ArtifactOutput('repair.loop_result')}
        flow, stage = 'repair', 'loop'

        @classmethod
        def execute(cls, inputs, ctx):
            return {'result': business.repair_loop(inputs['workspace'].payload, ctx)}

    class VerifyRepair(FixedOp):
        op_id = 'repair.verified_patch'
        inputs = {'loop': ArtifactInput('repair.loop_result')}
        outputs = {'patch': ArtifactOutput('repair.verified_patch')}
        flow, stage = 'repair', 'verify'

        @classmethod
        def execute(cls, inputs, ctx):
            return {'patch': business.verified_patch(inputs['loop'].payload)}

    class CandidateService(FixedOp):
        op_id = 'abtest.candidate_service'
        inputs = {'config': ArtifactInput('abtest.candidate_config'), 'patch': ArtifactInput('repair.verified_patch')}
        outputs = {'service': ArtifactOutput('abtest.candidate_service')}
        flow, stage = 'abtest', 'candidate_service'

        @classmethod
        def execute(cls, inputs, ctx):
            return {'service': business.candidate_service(inputs['config'].payload, inputs['patch'].payload, ctx)}

    class CandidateRagAnswer(FixedOp):
        op_id = 'abtest.candidate_rag_answer'
        inputs = {
            'case': ArtifactInput('eval.case', partition_spec=partitions),
            'service': ArtifactInput('abtest.candidate_service', partition_mapping=unpartitioned_to_all()),
        }
        outputs = {'answer': ArtifactOutput('abtest.candidate_rag_answer', partition_spec=partitions)}
        flow, stage = 'abtest', 'candidate_eval'

        @classmethod
        def execute(cls, inputs, ctx):
            return {'answer': business.candidate_rag_answer(inputs['case'].payload, inputs['service'].payload, ctx)}

    class CandidateJudge(FixedOp):
        op_id = 'abtest.candidate_judge'
        inputs = {
            'answer': ArtifactInput('abtest.candidate_rag_answer', partition_spec=partitions),
            'policy': ArtifactInput('eval.policy', partition_mapping=unpartitioned_to_all()),
        }
        outputs = {'judge': ArtifactOutput('abtest.candidate_judge_result', partition_spec=partitions)}
        flow, stage = 'abtest', 'candidate_eval'

        @classmethod
        def execute(cls, inputs, ctx):
            return {'judge': business.candidate_judge(inputs['answer'].payload, inputs['policy'].payload)}

    class CandidateSummary(FixedOp):
        op_id = 'abtest.candidate_eval_summary'
        inputs = {
            'judges': ArtifactInput(
                'abtest.candidate_judge_result',
                partition_spec=partitions,
                partition_mapping=all_to_unpartitioned(),
            )
        }
        outputs = {'summary': ArtifactOutput('abtest.candidate_eval_summary')}
        flow, stage = 'abtest', 'candidate_summary'

        @classmethod
        def execute(cls, inputs, ctx):
            return {'summary': business.candidate_summary(inputs['judges'])}

    class CompareABTest(FixedOp):
        op_id = 'abtest.compare'
        inputs = {'baseline': ArtifactInput('eval.summary'), 'candidate': ArtifactInput(
            'abtest.candidate_eval_summary')}
        outputs = {'comparison': ArtifactOutput('abtest.comparison')}
        flow, stage = 'abtest', 'comparison'

        @classmethod
        def execute(cls, inputs, ctx):
            return {'comparison': business.compare_abtest(inputs['baseline'].payload, inputs['candidate'].payload)}

    return (
        LoadCorpus,
        BuildCorpusSnapshot,
        PrepareCase,
        GenerateCase,
        AssembleDataset,
        RagAnswer,
        JudgeAnswer,
        EvalSummary,
        ClassifyCase,
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
