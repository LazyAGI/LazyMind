from __future__ import annotations

from collections.abc import Mapping
from typing import Any

from evo.artifact_runtime import (
    ArtifactInput,
    ArtifactOutput,
    ArtifactPayload,
    DAGGraph,
    FixedOp,
    StaticPartitions,
    all_to_unpartitioned,
    unpartitioned_to_all,
)
from evo.operations import abtest, analysis, dataset, repair
from evo.operations import eval as eval_ops
from evo.operations.common import OperationServices


def _payloads(items: Mapping[str, ArtifactPayload]) -> dict[str, Any]:
    return {name: item.payload for name, item in items.items()}


def _collection(items: Mapping[str, ArtifactPayload]) -> dict[str, Any]:
    return {partition: item.payload for partition, item in items.items()}


def _result(name: str, schema: str, value: Any) -> dict[str, ArtifactPayload]:
    return {name: ArtifactPayload(schema, value)}


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
            source_config = inputs['source_config'].payload
            loaded = dataset.load_source_documents(source_config)
            return _result(
                'report',
                'CorpusLoadReport',
                dataset.build_corpus_load_report(
                    source_config,
                    loaded.get('documents') or [],
                    load_mode=str(loaded.get('load_mode') or 'unknown'),
                    errors=list(loaded.get('errors') or []),
                ),
            )

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
            data = _payloads(inputs)
            return _result(
                'snapshot',
                'CorpusSnapshot',
                dataset.build_corpus_snapshot(data['report'], data['source_config']),
            )

    class GenerateCase(FixedOp):
        op_id = 'dataset.generate_case'
        inputs = {
            'config': ArtifactInput('run.config', partition_mapping=unpartitioned_to_all()),
            'snapshot': ArtifactInput('corpus.snapshot', partition_mapping=unpartitioned_to_all()),
        }
        outputs = {
            'preparation': ArtifactOutput('eval.case_preparation', partition_spec=partitions),
            'case': ArtifactOutput('eval.case', partition_spec=partitions),
        }
        flow, stage = 'dataset', 'generate_case'

        @classmethod
        def execute(cls, inputs, ctx):
            data = _payloads(inputs)
            preparation, case = dataset.prepare_and_generate_case(
                data['config'],
                data['snapshot'],
                ctx.partition,
                OperationServices(ctx),
            )
            return {
                **_result('preparation', 'CasePreparation', preparation),
                **_result('case', 'DatasetCase', case),
            }

    class AssembleDataset(FixedOp):
        op_id = 'dataset.assemble'
        inputs = {'cases': ArtifactInput('eval.case', partition_spec=partitions,
                                         partition_mapping=all_to_unpartitioned())}
        outputs = {'dataset': ArtifactOutput('eval.dataset')}
        flow, stage = 'dataset', 'assemble'

        @classmethod
        def execute(cls, inputs, ctx):
            return _result('dataset', 'EvalDataset', dataset.assemble_dataset(_collection(inputs['cases'])))

    class AnswerAndJudge(FixedOp):
        op_id = 'eval.answer_and_judge'
        inputs = {
            'case': ArtifactInput('eval.case', partition_spec=partitions),
            'target_config': ArtifactInput('eval.target_config', partition_mapping=unpartitioned_to_all()),
            'policy': ArtifactInput('eval.policy', partition_mapping=unpartitioned_to_all()),
        }
        outputs = {
            'answer': ArtifactOutput('eval.rag_answer', partition_spec=partitions),
            'judge': ArtifactOutput('eval.judge_result', partition_spec=partitions),
        }
        flow, stage = 'eval', 'answer_and_judge'

        @classmethod
        def execute(cls, inputs, ctx):
            data = _payloads(inputs)
            answer, judge = eval_ops.answer_and_judge(
                data['case'],
                data['target_config'],
                data['policy'],
                OperationServices(ctx),
            )
            return {
                **_result('answer', 'RagAnswer', answer),
                **_result('judge', 'JudgeResult', judge),
            }

    class EvalSummary(FixedOp):
        op_id = 'eval.summary'
        inputs = {'judges': ArtifactInput('eval.judge_result', partition_spec=partitions,
                                          partition_mapping=all_to_unpartitioned())}
        outputs = {'summary': ArtifactOutput('eval.summary')}
        flow, stage = 'eval', 'summary'

        @classmethod
        def execute(cls, inputs, ctx):
            return _result('summary', 'EvalSummary', eval_ops.eval_summary(_collection(inputs['judges'])))

    class TraceSummary(FixedOp):
        op_id = 'analysis.trace_summary'
        inputs = {
            'case': ArtifactInput('eval.case', partition_spec=partitions),
            'answer': ArtifactInput('eval.rag_answer', partition_spec=partitions),
        }
        outputs = {'summary': ArtifactOutput('analysis.trace_summary', partition_spec=partitions)}
        flow, stage = 'analysis', 'trace_summary'

        @classmethod
        def execute(cls, inputs, ctx):
            data = _payloads(inputs)
            return _result(
                'summary',
                'TraceSummary',
                analysis.trace_summary(data['case'], data['answer'], OperationServices(ctx)),
            )

    class ClassifyCase(FixedOp):
        op_id = 'analysis.classify_case'
        inputs = {
            'case': ArtifactInput('eval.case', partition_spec=partitions),
            'answer': ArtifactInput('eval.rag_answer', partition_spec=partitions),
            'judge': ArtifactInput('eval.judge_result', partition_spec=partitions),
            'trace': ArtifactInput('analysis.trace_summary', partition_spec=partitions),
        }
        outputs = {'classification': ArtifactOutput('analysis.case_classification', partition_spec=partitions)}
        flow, stage = 'analysis', 'classification'

        @classmethod
        def execute(cls, inputs, ctx):
            data = _payloads(inputs)
            return _result(
                'classification',
                'CaseClassification',
                analysis.classify_case(data['case'], data['answer'], data['judge'], data['trace']),
            )

    class TraceClusters(FixedOp):
        op_id = 'analysis.trace_clusters'
        inputs = {
            'classifications': ArtifactInput(
                'analysis.case_classification',
                partition_spec=partitions,
                partition_mapping=all_to_unpartitioned(),
            )
        }
        outputs = {'clusters': ArtifactOutput('analysis.trace_clusters')}
        flow, stage = 'analysis', 'trace_clusters'

        @classmethod
        def execute(cls, inputs, ctx):
            return _result(
                'clusters',
                'TraceClusters',
                analysis.trace_clusters(_collection(inputs['classifications'])),
            )

    class AnalysisSummary(FixedOp):
        op_id = 'analysis.summary'
        inputs = {
            'classifications': ArtifactInput(
                'analysis.case_classification',
                partition_spec=partitions,
                partition_mapping=all_to_unpartitioned(),
            ),
            'clusters': ArtifactInput('analysis.trace_clusters'),
        }
        outputs = {'summary': ArtifactOutput('analysis.summary')}
        flow, stage = 'analysis', 'summary'

        @classmethod
        def execute(cls, inputs, ctx):
            return _result(
                'summary',
                'AnalysisSummary',
                analysis.analysis_summary(
                    _collection(inputs['classifications']),
                    inputs['clusters'].payload,
                ),
            )

    class BuildRepairPlan(FixedOp):
        op_id = 'repair.plan'
        inputs = {'analysis': ArtifactInput('analysis.summary'), 'policy': ArtifactInput('repair.policy')}
        outputs = {'plan': ArtifactOutput('repair.plan')}
        flow, stage = 'repair', 'plan'

        @classmethod
        def execute(cls, inputs, ctx):
            data = _payloads(inputs)
            return _result('plan', 'RepairPlan', repair.repair_plan(data['analysis'], data['policy']))

    class PrepareWorkspace(FixedOp):
        op_id = 'repair.candidate_workspace'
        inputs = {'plan': ArtifactInput('repair.plan')}
        outputs = {'workspace': ArtifactOutput('repair.candidate_workspace')}
        flow, stage = 'repair', 'workspace'

        @classmethod
        def execute(cls, inputs, ctx):
            return _result(
                'workspace',
                'CandidateWorkspace',
                repair.candidate_workspace(inputs['plan'].payload, OperationServices(ctx)),
            )

    class RepairLoop(FixedOp):
        op_id = 'repair.loop_result'
        inputs = {
            'workspace': ArtifactInput('repair.candidate_workspace'),
            'cases': ArtifactInput(
                'eval.case',
                partition_spec=partitions,
                partition_mapping=all_to_unpartitioned(),
            ),
            'baseline_judges': ArtifactInput(
                'eval.judge_result',
                partition_spec=partitions,
                partition_mapping=all_to_unpartitioned(),
            ),
            'eval_policy': ArtifactInput('eval.policy'),
            'candidate_config': ArtifactInput('abtest.candidate_config'),
        }
        outputs = {'result': ArtifactOutput('repair.loop_result')}
        flow, stage = 'repair', 'loop'

        @classmethod
        def execute(cls, inputs, ctx):
            data = _payloads({key: inputs[key] for key in ('workspace', 'eval_policy', 'candidate_config')})
            return _result(
                'result',
                'RepairLoopResult',
                repair.repair_loop(
                    data['workspace'],
                    _collection(inputs['cases']),
                    _collection(inputs['baseline_judges']),
                    data['eval_policy'],
                    data['candidate_config'],
                    OperationServices(ctx),
                ),
            )

    class VerifyRepair(FixedOp):
        op_id = 'repair.verified_patch'
        inputs = {'loop': ArtifactInput('repair.loop_result')}
        outputs = {'patch': ArtifactOutput('repair.verified_patch')}
        flow, stage = 'repair', 'verify'

        @classmethod
        def execute(cls, inputs, ctx):
            return _result('patch', 'VerifiedRepair', repair.verified_patch(inputs['loop'].payload))

    class CandidateService(FixedOp):
        op_id = 'abtest.candidate_service'
        inputs = {'config': ArtifactInput('abtest.candidate_config'), 'patch': ArtifactInput('repair.verified_patch')}
        outputs = {'service': ArtifactOutput('abtest.candidate_service')}
        flow, stage = 'abtest', 'candidate_service'

        @classmethod
        def execute(cls, inputs, ctx):
            data = _payloads(inputs)
            return _result(
                'service',
                'CandidateService',
                abtest.candidate_service(data['config'], data['patch'], OperationServices(ctx)),
            )

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
            data = _payloads(inputs)
            return _result(
                'answer',
                'CandidateRagAnswer',
                abtest.candidate_rag_answer(data['case'], data['service'], OperationServices(ctx)),
            )

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
            data = _payloads(inputs)
            return _result(
                'judge',
                'CandidateJudgeResult',
                abtest.candidate_judge(data['answer'], data['policy'], OperationServices(ctx)),
            )

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
            return _result(
                'summary',
                'CandidateEvalSummary',
                abtest.candidate_summary(_collection(inputs['judges'])),
            )

    class CompareABTest(FixedOp):
        op_id = 'abtest.compare'
        inputs = {'baseline': ArtifactInput('eval.summary'), 'candidate': ArtifactInput(
            'abtest.candidate_eval_summary')}
        outputs = {'comparison': ArtifactOutput('abtest.comparison')}
        flow, stage = 'abtest', 'comparison'

        @classmethod
        def execute(cls, inputs, ctx):
            data = _payloads(inputs)
            return _result(
                'comparison',
                'ABTestComparison',
                abtest.compare_abtest(data['baseline'], data['candidate']),
            )

    return (
        LoadCorpus,
        BuildCorpusSnapshot,
        GenerateCase,
        AssembleDataset,
        AnswerAndJudge,
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
