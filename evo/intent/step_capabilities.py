from __future__ import annotations

import copy
from typing import Any

from .models import CapabilitySpec

STR = {'type': 'string', 'minLength': 1}
ARR = {'type': 'array', 'items': STR}
INT = {'type': 'integer'}
NUM = {'type': 'number'}
RESUME_POLICY = {'type': 'string', 'enum': ['resume_from_snapshot', 'resume_with_interventions']}
CASE = {'source': 'source_span_mention', 'case_id_format': 'zero4'}
QUERY_ID = {'query_intent_id': STR}
CASE_TYPES = ['single_hop', 'single_doc_multi_hop', 'multi_doc_multi_hop', 'table_list', 'formula']
REGEN_TYPES = CASE_TYPES + ['multi_hop', 'comparison']
DIFFICULTIES = ['easy', 'medium', 'hard']
CASE_TYPE = {'type': 'string', 'enum': CASE_TYPES}
REGEN_TYPE = {'type': 'string', 'enum': REGEN_TYPES}
DIFFICULTY = {'type': 'string', 'enum': DIFFICULTIES}
PLAN_SEMANTIC = {
    'fine_category': {'type': 'string'}, 'target_case_ids': ARR, 'target_mean_delta': NUM,
    'goodcase_guard_ratio': NUM, 'goodcase_regression_ratio_limit': NUM, 'random_seed': STR,
    'primary_metric': {'type': 'string', 'enum': ['answer_correctness', 'faithfulness']},
}
ABTEST_METRICS = ['answer_correctness', 'faithfulness', 'doc_recall', 'context_recall']
ABTEST_SEMANTIC = {
    'target_mean_delta': NUM, 'primary_metric': {'type': 'string', 'enum': ABTEST_METRICS},
    'goodcase_regression_ratio_limit': NUM,
}
CUTOVER_SEMANTIC = {'algorithm_id': STR, 'candidate_weight': {'type': 'integer', 'minimum': 1}}
PATCH_SEMANTIC = {
    'question': STR, 'answer': STR, 'question_type': REGEN_TYPE, 'difficulty': DIFFICULTY, 'grading_guidance': STR,
    'reference_context': ARR, 'reference_doc': ARR, 'reference_doc_ids': ARR, 'reference_chunk_ids': ARR,
}
LOOP_FIELDS = {
    name: STR for name in ('candidate_workdir', 'candidate_service_command', 'candidate_healthcheck_url',
                           'candidate_chat_url', 'dataset_name', 'opencode_container')
}
STEP_ORDER = ('query', 'control', 'dataset', 'eval', 'analysis', 'abtest', 'repair')
READ_ARTIFACT_IDS = [
    'corpus_load_report', 'corpus_snapshot', 'eval_dataset', 'eval_report', 'classification_report',
    'abtest_comparison', 'candidate_algorithm_cutover', 'repair_loop_plan', 'verified_repair',
]
REPAIR_ARTIFACT_SCHEMAS = [
    'RepairLoopPlan', 'RepairEvidencePacket', 'FaultLocalizationReport', 'DiagnosticProbePlan',
    'DiagnosticProbeResult', 'RepairDiagnosis', 'OpenCodeInstruction', 'OpenCodeWorkerReport',
    'PatchCorrectnessAssessment', 'PatchCritique', 'BranchDecision', 'RepairBranchState', 'RepairStateTransition',
    'RepairHypothesis', 'RepairPlan', 'OpenCodeRunTrace', 'CodePatchCandidate', 'CandidateServiceRun',
    'CandidateAlgorithmCutover', 'RepairEvaluation', 'CandidateClassificationReport', 'RepairLoopDecision',
    'RepairLoopMemory', 'RepairLoopState', 'VerifiedRepair',
]
READ_TARGETS = [
    'CorpusLoadReport', 'CorpusSnapshot', 'CasePreparation', 'DatasetCase', 'EvalDataset', 'RagAnswer', 'JudgeResult',
    'EvalReport', 'CaseCoarseClassification', 'CaseFineClassification', 'ClassificationReport', 'ABTestComparison',
    'ResearchRedirect', *REPAIR_ARTIFACT_SCHEMAS,
]


def step_capabilities(*, run_id: str = '', dataset_id: str = '', eval_dataset_ref: str = 'eval_dataset@v1',
                      eval_report_ref: str = 'eval_report@v1', bad_case_ids: list[str] | None = None,
                      classification_report_ref: str = 'classification_report@v1',
                      abtest_baseline_report_ref: str = '',
                      abtest_candidate_report_ref: str = 'candidate_eval_report@v1',
                      abtest_comparison_ref: str = 'abtest_comparison@v1',
                      candidate_workspace_ref: str = 'candidate_workspace@v1',
                      repair_loop_plan_ref: str = 'repair_loop_plan@v1', verified_repair_ref: str = '',
                      target_chat_url: str = '', router_admin_url: str = '', running_operation_id: str = '',
                      loop_system_params: dict[str, Any] | None = None) -> list[CapabilitySpec]:
    ctx = dict(locals())
    ctx.update(bad_case_ids=bad_case_ids or [], loop_system_params=loop_system_params or {},
               abtest_baseline_report_ref=abtest_baseline_report_ref or eval_report_ref)
    return [_cap(item, ctx) for stage in STEP_ORDER for item in _defs()[stage]]


def capabilities_for_stage(stage: str, **kwargs: Any) -> list[CapabilitySpec]:
    if stage not in STEP_ORDER: raise ValueError(f'unknown capability stage: {stage}')
    stage_ids = {item['id'] for item in _defs().get(stage, [])}
    return [capability for capability in step_capabilities(**kwargs) if capability.capability_id in stage_ids]


def _defs() -> dict[str, list[dict[str, Any]]]:
    return {
        'query': [
            {'id': 'read_artifact_query', 'op': 'ReadArtifactQueryOperation', 'targets': list(READ_TARGETS),
             'writes': 'IntentAnswer', 'title': '读取 artifact', 'desc': '读取用户指定 artifact 当前版本或精确版本。',
             'use': ['用户要求查看某个产物、case、报告、分类结果或修复结果'], 'avoid': ['用户要求执行、修改、重试或取消 operation'],
             'system': {'artifact_ref': {'source': 'message_artifact_ref'},
                        'artifact_id': {**CASE, 'source': 'message_artifact_id', 'ids': READ_ARTIFACT_IDS}},
             'params': QUERY_ID, 'required': ['query_intent_id'], 'effects': ['read_artifact']},
            {'id': 'read_operation_query', 'op': 'ReadOperationQueryOperation', 'writes': 'IntentAnswer',
             'title': '读取 operation 状态', 'desc': '读取用户指定 operation_run_id 的状态。',
             'use': ['用户询问某个 operation 是否完成、失败原因、当前状态'], 'avoid': ['用户要求重试、取消或修改 operation'],
             'system': {'operation_run_id': {'source': 'message_operation_id'}},
             'params': QUERY_ID | {'operation_run_id': STR}, 'required': ['query_intent_id', 'operation_run_id'],
             'effects': ['read_operation_status']},
            {'id': 'read_run_status_query', 'op': 'ReadRunStatusQueryOperation', 'writes': 'IntentAnswer',
             'title': '查看当前流程进度', 'desc': '读取当前 run 的整体状态和进度。',
             'use': ['用户询问当前进度、现在执行到哪里、到哪一步、整体流程状态，包含重复查询'], 'avoid': ['用户询问单个 operation 或 artifact 内容'],
             'system': {'run_id': {'source': 'ctx', 'key': 'run_id'}}, 'params': QUERY_ID | {'run_id': STR},
             'required': ['query_intent_id', 'run_id'], 'effects': ['read_run_status']},
            _case_read('read_coarse_artifact_query', '查看粗分结果', 'CaseCoarseClassification',
                       'case_coarse_classification_{case_id}@v1'),
            _case_read('read_fine_artifact_query', '查看细分结果', 'CaseFineClassification',
                       'case_fine_classification_{case_id}@v1'),
            {'id': 'respond_to_user', 'op': 'RespondToUserOperation', 'writes': 'IntentAnswer', 'title': '回复用户',
             'desc': '在无需创建业务操作时写入可审计回答。', 'use': ['用户只需要解释、确认、条件分支已经有查询结果后需要回复'],
             'avoid': ['用户要求创建、修改、重试、取消或查询业务产物'], 'semantic': {'answer': STR},
             'params': QUERY_ID | {'answer': STR}, 'required': ['query_intent_id', 'answer'],
             'effects': ['write_intent_answer'], 'task_type': 'chat_task'},
        ], 'control': [
            _control('retry_operation', '重试 operation', '用户要求重试某个 operation 或重新跑失败步骤'),
            _control('cancel_operation', '取消 operation', '用户要求停止、取消或打断当前 operation'),
            _control('cancel_running_operation', '取消当前运行 operation',
                     '用户要求取消当前正在运行的 task/opencode/repair，不给出 operation id', source='current_operation_id'),
            {'id': 'resume_checkpointed', 'title': '继续 checkpoint',
             'desc': '用户明确确认继续当前 checkpoint 后续流程时，恢复 flow gate 或 runtime checkpoint。',
             'use': ['用户明确确认继续当前 checkpoint、继续执行下一阶段、恢复暂停后的后续流程'], 'avoid': ['用户只是询问进度、描述希望稍后继续、或同时提出新的业务修改要求'],
             'semantic': {'input_policy': RESUME_POLICY}, 'params': {'input_policy': RESUME_POLICY},
             'effects': ['resume_checkpoint'], 'task_type': 'control_task', 'risk': 'medium'},
            {'id': 'redirect_research', 'op': 'RedirectResearchOperation', 'writes': 'ResearchRedirect',
             'title': '调整研究方向', 'desc': '把用户给出的研究员 id 和新指令写成 ResearchRedirect artifact。',
             'use': ['用户要求把研究、分析或搜索方向交给某个 researcher 调整'], 'avoid': ['用户要求修改数据集、评测或 repair 代码'],
             'semantic': {'researcher_id': STR, 'instructions': STR},
             'params': {'researcher_id': STR, 'instructions': STR}, 'required': ['researcher_id', 'instructions'],
             'effects': ['write_research_redirect'], 'flow': 'intent', 'stage': 'redirect_research', 'risk': 'medium'},
        ], 'dataset': [
            {'id': 'load_corpus', 'op': 'LoadCorpusOperation', 'writes': 'CorpusLoadReport', 'title': '加载知识库语料',
             'desc': '从真实 LazyMind 知识库加载文档元数据。dataset_id/source_id 由系统绑定。',
             'use': ['用户要求加载或重新加载知识库语料'], 'avoid': ['用户要求生成题目或执行评测'],
             'semantic': {'max_docs': {'type': 'integer', 'minimum': 1}},
             'system': {'operation_id': _const('dataset.load_corpus'), 'sources': {'source': 'load_sources'}},
             'params': {'sources': {'type': 'array'}}, 'required': ['sources'], 'stage': 'load_corpus',
             'effects': ['write_corpus_load_report']},
            {'id': 'build_corpus_snapshot', 'op': 'BuildCorpusSnapshotOperation', 'target': 'CorpusLoadReport',
             'writes': 'CorpusSnapshot', 'title': '构建语料快照',
             'desc': '基于 CorpusLoadReport 读取分段并产出 CorpusSnapshot。source_report_ref 由系统绑定 latest。',
             'use': ['用户要求基于加载结果生成语料快照'], 'avoid': ['用户要求直接生成 dataset case'],
             'semantic': {'min_segment_chars': INT, 'segment_groups': ARR},
             'system': {'operation_id': _const('dataset.build_corpus_snapshot'),
                        'source_report_ref': _const('corpus_load_report@v1')},
             'params': {'source_report_ref': STR, 'segment_page_size': INT, 'min_segment_chars': INT,
                        'segment_groups': ARR},
             'required': ['source_report_ref'], 'stage': 'build_corpus_snapshot', 'requires': ['corpus_load_report'],
             'effects': ['write_corpus_snapshot']},
            {'id': 'prepare_dataset_case', 'op': 'PrepareDatasetCaseOperation', 'target': 'CorpusSnapshot',
             'writes': 'CasePreparation', 'title': '准备评测样本',
             'desc': '根据用户要求为指定 case 生成 CasePreparation。source_snapshot_ref/output_case_id 由系统绑定。',
             'use': ['用户要求准备、修改、重新设计某条数据集样本计划'], 'avoid': ['用户已经给出完整 question/answer 并要求直接覆盖样本'],
             'semantic': {'question_type': CASE_TYPE, 'difficulty': DIFFICULTY, 'user_instruction': STR,
                          'doc_ids': ARR, 'chunk_ids': ARR, 'preview_chars': INT},
             'system': {'operation_id': _case_template('dataset.prepare.{case_id}'),
                        'source_snapshot_ref': _const('corpus_snapshot@v1'),
                        'output_case_id': _case_template('{case_id}')},
             'params': {'source_snapshot_ref': STR, 'output_case_id': STR, 'question_type': CASE_TYPE,
                        'difficulty': DIFFICULTY, 'doc_ids': ARR, 'chunk_ids': ARR, 'preview_chars': INT,
                        'user_instruction': STR},
             'required': ['source_snapshot_ref', 'output_case_id', 'question_type', 'difficulty'],
             'stage': 'prepare_case', 'requires': ['corpus_snapshot'], 'effects': ['write_case_preparation']},
            {'id': 'generate_dataset_case', 'op': 'GenerateDatasetCaseOperation', 'target': 'CasePreparation',
             'writes': 'DatasetCase', 'title': '生成评测样本',
             'desc': '基于指定 CasePreparation 生成 DatasetCase。case_preparation_ref 由系统绑定。',
             'use': ['用户要求生成某条已准备好的 DatasetCase'], 'avoid': ['用户要求重新设计题型或证据，应先 prepare'],
             'system': {'operation_id': _case_template('dataset.generate.{case_id}'),
                        'case_preparation_ref': _case_template('case_preparation_{case_id}@v1')},
             'params': {'case_preparation_ref': STR}, 'required': ['case_preparation_ref'],
             'stage': 'generate_case', 'effects': ['write_dataset_case']},
            {'id': 'regenerate_dataset_case', 'op': 'RegenerateDatasetCaseOperation', 'target': 'DatasetCase',
             'writes': 'DatasetCase', 'title': '直接重写数据集样本',
             'desc': '按用户给出的语义内容重写指定 DatasetCase。case_id 由 source_spans 绑定。',
             'use': ['用户直接给出新 question/answer/question_type，要求重写某条数据集样本'],
             'avoid': ['用户只给题型或证据偏好但没有完整 question/answer，应走 prepare/generate'],
             'semantic': {'question': STR, 'answer': STR, 'question_type': REGEN_TYPE}, 'system': {'case_id': CASE},
             'params': {'case_id': STR, 'question': STR, 'answer': STR, 'question_type': REGEN_TYPE},
             'required': ['case_id', 'question', 'answer', 'question_type'],
             'effects': ['write_dataset_case', 'invalidates_downstream_eval'],
             'cross_stage_policy': 'allowed_with_runtime_confirmation'},
            {'id': 'patch_dataset_case', 'op': 'PatchArtifactOperation', 'target': 'DatasetCase',
             'writes': 'DatasetCase', 'title': '直接修补数据集样本', 'desc': '按白名单字段修补 DatasetCase，保留原样本的证据、评分指导等上下文字段。',
             'use': ['用户明确给出 DatasetCase 的 question、answer、grading_guidance 或参考证据字段修改'],
             'avoid': ['用户只是给出题型、方向或自然语言偏好，应走 prepare/regenerate'],
             'semantic': dict(PATCH_SEMANTIC), 'system': {'artifact_id': CASE}, 'params': dict(PATCH_SEMANTIC),
             'required': [], 'effects': ['write_dataset_case'], 'flow': 'intent', 'stage': 'patch_dataset_case',
             'risk': 'medium', 'cross_stage_policy': 'allowed_with_runtime_confirmation'},
            {'id': 'assemble_dataset', 'op': 'AssembleDatasetOperation', 'writes': 'EvalDataset', 'title': '组装评测集',
             'desc': '把用户明确给出的 case_ids 组装成 EvalDataset。case 版本由 runtime 绑定 latest。',
             'use': ['用户要求组装、重排、排除或加回评测集 case'], 'avoid': ['用户要求生成或修改单条 case'], 'semantic': {'case_ids': ARR},
             'system': {'operation_id': _const('dataset.assemble'), 'dataset_id': _const('eval_dataset')},
             'params': {'dataset_id': STR, 'case_ids': {'type': 'array', 'items': STR, 'minItems': 1}},
             'required': ['dataset_id', 'case_ids'], 'stage': 'assemble', 'effects': ['write_eval_dataset'],
             'batch_policy': 'case_ids_must_be_explicit'},
        ], 'eval': [
            {'id': 'rag_answer_case', 'op': 'RagAnswerOperation', 'target': 'EvalDataset', 'writes': 'RagAnswer',
             'title': '执行单条 RAG 评测', 'desc': '对 EvalDataset 中指定 case 调用固定 LazyMind chat endpoint。',
             'use': ['用户要求对某条 case 执行 RAG 回答'], 'avoid': ['用户要求切换 endpoint、修改数据集或修改 case 内容'],
             'system': {'operation_id': _case_template('eval.rag.{case_id}'),
                        'eval_dataset_ref': {'source': 'ctx', 'key': 'eval_dataset_ref'},
                        'dataset_name': {'source': 'ctx', 'key': 'dataset_id'},
                        'target_chat_url': {'source': 'ctx', 'key': 'target_chat_url'},
                        'require_trace': _const(True), 'case_id': CASE},
             'params': {'eval_dataset_ref': STR, 'case_id': STR, 'target_chat_url': STR, 'dataset_name': STR,
                        'require_trace': {'type': 'boolean'}},
             'required': ['eval_dataset_ref', 'case_id', 'target_chat_url', 'dataset_name', 'require_trace'],
             'flow': 'eval', 'stage': 'rag_answer', 'requires': ['eval_dataset'], 'effects': ['write_rag_answer']},
            {'id': 'judge_answer_case', 'op': 'JudgeAnswerOperation', 'target': 'RagAnswer', 'writes': 'JudgeResult',
             'title': '评判单条 RAG answer', 'desc': '对指定 RagAnswer 执行 judge。eval_dataset_ref、rag_answer_ref 由系统绑定。',
             'use': ['用户要求评判某条 RAG 回答或生成 JudgeResult'], 'avoid': ['用户要求修改标准答案、RAG 输出或 endpoint'],
             'system': {'operation_id': _case_template('eval.judge.{case_id}'),
                        'eval_dataset_ref': {'source': 'ctx', 'key': 'eval_dataset_ref'}, 'case_id': CASE,
                        'rag_answer_ref': _case_template('rag_answer_{case_id}@v1')},
             'params': {'eval_dataset_ref': STR, 'case_id': STR, 'rag_answer_ref': STR},
             'required': ['eval_dataset_ref', 'case_id', 'rag_answer_ref'], 'flow': 'eval', 'stage': 'judge_answer',
             'effects': ['write_judge_result']},
            {'id': 'rejudge_case', 'op': 'RejudgeCaseOperation', 'target': 'DatasetCase',
             'title': '拒绝无 RagAnswer 的重新评分',
             'desc': 'DatasetCase 不能直接产生合法 JudgeResult；需要先绑定 RagAnswer，再走 judge_answer_case。',
             'use': ['用户要求对 DatasetCase 直接打分但没有提供或绑定 RagAnswer'], 'avoid': ['用户要求评判真实 RagAnswer，应走 judge_answer_case'],
             'semantic': {'score': NUM, 'rationale': {'type': 'string'}}, 'system': {'artifact_id': CASE},
             'params': {'score': NUM, 'rationale': {'type': 'string'}}, 'required': [], 'effects': [],
             'flow': 'intent', 'stage': 'rejudge_case', 'risk': 'medium'},
            {'id': 'aggregate_eval_report', 'op': 'EvalAggregateOperation', 'target': 'EvalDataset',
             'writes': 'EvalReport', 'title': '聚合 eval report',
             'desc': '基于当前 EvalDataset 聚合 EvalReport。eval_dataset_ref 由系统绑定。',
             'use': ['用户要求汇总评测结果、生成 eval report'], 'avoid': ['用户要求修改分数、排除 case 或切换 endpoint'],
             'system': {'operation_id': _const('eval.aggregate'),
                        'eval_dataset_ref': {'source': 'ctx', 'key': 'eval_dataset_ref'},
                        'report_id': _const('eval_report')},
             'params': {'eval_dataset_ref': STR, 'report_id': STR}, 'required': ['eval_dataset_ref', 'report_id'],
             'flow': 'eval', 'stage': 'aggregate', 'requires': ['eval_dataset'], 'effects': ['write_eval_report']},
        ], 'analysis': [
            {'id': 'coarse_classify_case', 'op': 'CaseCoarseClassificationOperation', 'target': 'EvalReport',
             'writes': 'CaseCoarseClassification', 'title': '粗分 badcase',
             'desc': '基于 EvalReport 对指定 badcase 做大类分类。eval_report_ref/output_id 由系统绑定。',
             'use': ['用户要求对某个 badcase 做粗分归因'], 'avoid': ['用户要求细分、汇总报告或修复代码'],
             'system': {'operation_id': _case_template('analysis.coarse.{case_id}'),
                        'eval_report_ref': {'source': 'ctx', 'key': 'eval_report_ref'}, 'case_id': CASE,
                        'output_id': _case_template('case_coarse_classification_{case_id}')},
             'params': {'eval_report_ref': STR, 'case_id': STR, 'output_id': STR},
             'required': ['eval_report_ref', 'case_id', 'output_id'], 'flow': 'analysis',
             'stage': 'coarse_classify', 'requires': ['eval_report'], 'effects': ['write_case_coarse_classification']},
            {'id': 'fine_classify_case', 'op': 'CaseFineClassificationOperation', 'target': 'CaseCoarseClassification',
             'writes': 'CaseFineClassification', 'title': '细分 badcase',
             'desc': '对用户指定 case 的 CaseCoarseClassification 做小类归因。',
             'use': ['用户要求对某个 badcase、粗分结果或数据集条目做细分归因'], 'avoid': ['用户要求粗分、汇总报告或修复代码'],
             'system': {'operation_id': _case_template('analysis.fine.{case_id}'),
                        'coarse_classification_ref': _case_template('case_coarse_classification_{case_id}@v1'),
                        'output_id': _case_template('case_fine_classification_{case_id}')},
             'params': {'coarse_classification_ref': STR, 'output_id': STR},
             'required': ['coarse_classification_ref', 'output_id'], 'flow': 'analysis', 'stage': 'fine_classify',
             'effects': ['write_case_fine_classification']},
            {'id': 'assemble_classification_report', 'op': 'AssembleClassificationReportOperation',
             'writes': 'ClassificationReport', 'title': '汇总分类报告',
             'desc': '汇总本轮所有 badcase 的 coarse/fine 分类结果。eval_report_ref 和 fine refs 由系统绑定。',
             'use': ['用户要汇总本轮分类结果、整理分类报告、重新生成分类报告'], 'avoid': ['用户只想查看已有分类报告或单条 case 的分类结果'],
             'system': {'operation_id': _const('analysis.classification_report'),
                        'eval_report_ref': {'source': 'ctx', 'key': 'eval_report_ref'},
                        'fine_classification_refs': {'source': 'fine_refs'},
                        'output_id': _const('classification_report')},
             'params': {'eval_report_ref': STR, 'fine_classification_refs': ARR, 'output_id': STR},
             'required': ['eval_report_ref', 'fine_classification_refs', 'output_id'], 'flow': 'analysis',
             'stage': 'classification_report', 'requires': {'source': 'classification_report_requires'},
             'effects': ['write_classification_report']},
        ], 'abtest': [
            {'id': 'compare_abtest_result', 'op': 'CompareABTestOperation', 'targets': ['EvalReport'],
             'writes': 'ABTestComparison', 'title': '对比 ABTest 结果',
             'desc': '比较 baseline 和 candidate 两个 EvalReport，确定性计算指标差异和 decision。',
             'use': ['用户要求对比 ABTest 结果、查看 candidate 相比 baseline 提升多少或是否通过'],
             'avoid': ['candidate eval 还没完成，或用户要求启动评测，应先走现有 eval 能力'], 'semantic': dict(ABTEST_SEMANTIC),
             'system': {'baseline_eval_report_ref': {'source': 'ctx', 'key': 'abtest_baseline_report_ref'},
                        'candidate_eval_report_ref': {'source': 'ctx', 'key': 'abtest_candidate_report_ref'},
                        'output_id': _const('abtest_comparison')},
             'params': {**ABTEST_SEMANTIC, 'baseline_eval_report_ref': STR, 'candidate_eval_report_ref': STR,
                        'output_id': STR},
             'required': ['baseline_eval_report_ref', 'candidate_eval_report_ref', 'output_id'], 'flow': 'abtest',
             'stage': 'compare', 'requires': {'source': 'abtest_compare_requires'},
             'effects': ['write_abtest_comparison'], 'risk': 'low'},
            {'id': 'cutover_candidate_algorithm', 'op': 'CutoverCandidateAlgorithmOperation',
             'targets': ['ABTestComparison', 'CandidateWorkspace'], 'writes': 'CandidateAlgorithmCutover',
             'title': '注册并切流 candidate',
             'desc': '仅在 ABTestComparison 为 accept 后，调用 LazyMind router 注册 candidate 并切换 chat 流量。',
             'use': ['用户要求确认 candidate 通过后注册算法、切换 chat 服务、把流量切到新版本'],
             'avoid': ['ABTest 尚未完成或 decision 不是 accept'], 'semantic': dict(CUTOVER_SEMANTIC),
             'system': {'abtest_comparison_ref': {'source': 'ctx', 'key': 'abtest_comparison_ref'},
                        'candidate_workspace_ref': {'source': 'ctx', 'key': 'candidate_workspace_ref'},
                        'router_admin_url': {'source': 'ctx', 'key': 'router_admin_url'},
                        'output_id': _const('candidate_algorithm_cutover')},
             'params': CUTOVER_SEMANTIC | {'abtest_comparison_ref': STR, 'candidate_workspace_ref': STR,
                                           'router_admin_url': STR, 'output_id': STR},
             'required': ['abtest_comparison_ref', 'candidate_workspace_ref', 'router_admin_url', 'output_id'],
             'flow': 'abtest', 'stage': 'candidate_cutover', 'requires': {'source': 'candidate_cutover_requires'},
             'effects': ['register_candidate_algorithm', 'switch_chat_traffic'], 'risk': 'medium',
             'confirmation': 'required'},
        ], 'repair': [
            {'id': 'build_repair_loop_plan', 'op': 'BuildRepairLoopPlanOperation', 'target': 'ClassificationReport',
             'writes': 'RepairLoopPlan', 'title': '构建修复循环目标',
             'desc': '基于分类报告确定 Step4 repair loop 的 badcase 目标和 goodcase guard。',
             'use': ['用户要求基于分类报告开始修复、优化 badcase、进入 step4', '用户要求在已验证修复基础上继续提升'],
             'avoid': ['用户要求实际修改代码，应先构建 plan 后启动 repair loop'], 'semantic': PLAN_SEMANTIC,
             'system': {'classification_report_ref': {'source': 'ctx', 'key': 'classification_report_ref'},
                        'verified_repair_ref': {'source': 'ctx', 'key': 'verified_repair_ref'},
                        'output_id': _const('repair_loop_plan')},
             'params': {**PLAN_SEMANTIC, 'classification_report_ref': STR, 'verified_repair_ref': STR,
                        'output_id': STR},
             'required': ['classification_report_ref', 'output_id'], 'flow': 'repair', 'stage': 'plan',
             'requires': {'source': 'strip_ref', 'key': 'classification_report_ref'},
             'effects': ['write_repair_loop_plan'], 'risk': 'medium'},
            _repair_loop('start_repair_loop', '启动修复循环', '用户要求开始执行修复循环、让 opencode 修改并验证'),
            _repair_loop('continue_repair_loop', '继续修复循环', '用户要求基于当前 repair loop state 继续下一轮分析、修改和验证'),
            {'id': 'read_repair_artifact', 'op': 'ReadArtifactQueryOperation', 'targets': REPAIR_ARTIFACT_SCHEMAS,
             'writes': 'IntentAnswer', 'title': '读取 repair 产物',
             'desc': '读取 Step4 repair loop 的分析、opencode、patch、评估、memory、state 或 verified 产物。',
             'use': ['用户要求查看 Step4 修复过程、opencode 执行、patch、评估、memory、state 或 verified repair'],
             'avoid': ['用户要求继续执行或取消 repair loop'],
             'system': {'artifact_ref': {'source': 'message_artifact_ref'},
                        'artifact_id': {'source': 'message_artifact_id', 'ids': _repair_artifact_ids()}},
             'params': QUERY_ID, 'required': ['query_intent_id'], 'effects': ['read_artifact']},
        ],
    }


def _case_read(capability_id: str, title: str, schema: str, template: str) -> dict[str, Any]:
    return {'id': capability_id, 'op': 'ReadArtifactQueryOperation', 'target': schema, 'writes': 'IntentAnswer',
            'title': title, 'desc': f'读取用户指定数据集条目的 {schema}。', 'use': [f'用户要查看某条数据集的 {title}'],
            'avoid': ['用户要求重新分类或修改 case'], 'system': {'artifact_ref': _case_template(template)},
            'params': QUERY_ID, 'required': ['query_intent_id'], 'effects': ['read_artifact']}


def _control(capability_id: str, title: str, use: str, *, source: str = 'message_operation_id') -> dict[str, Any]:
    return {'id': capability_id, 'title': title, 'desc': '只确认用户控制意图；真正执行由 runtime/graph 控制层完成。',
            'use': [use], 'avoid': ['用户要求创建业务产物'], 'system': {'operation_run_id': {'source': source}},
            'params': {'operation_run_id': STR}, 'required': ['operation_run_id'],
            'effects': ['control_operation'], 'task_type': 'control_task', 'risk': 'medium'}


def _repair_loop(capability_id: str, title: str, use: str) -> dict[str, Any]:
    return {'id': capability_id, 'op': 'RepairLoopAgentOperation', 'target': 'RepairLoopPlan',
            'writes': 'RepairLoopDecision', 'title': title,
            'desc': '执行 Step4 repair loop。RepairLoopPlan、工作区、服务等系统参数由 runtime 绑定。', 'use': [use],
            'avoid': ['用户只要求构建 repair plan 或查看 repair artifact'], 'semantic': {'repair_instruction': STR},
            'system': {'repair_loop_plan_ref': {'source': 'ctx', 'key': 'repair_loop_plan_ref'},
                       'output_id': _const('repair_loop_agent')},
            'params': {'repair_loop_plan_ref': STR, 'output_id': STR, 'repair_instruction': STR,
                       'repair_scope': {'type': 'object'}, **LOOP_FIELDS},
            'required': ['repair_loop_plan_ref', 'output_id'], 'flow': 'repair', 'stage': capability_id,
            'requires': {'source': 'strip_ref', 'key': 'repair_loop_plan_ref'},
            'effects': ['run_repair_loop', 'write_repair_artifacts'], 'risk': 'high'}


def _cap(item: dict[str, Any], ctx: dict[str, Any]) -> CapabilitySpec:
    system_specs = dict(item.get('system', {}))
    params = copy.deepcopy(item.get('params', {}))
    if item['id'] in {'start_repair_loop', 'continue_repair_loop'}:
        loop = {key: value for key, value in ctx['loop_system_params'].items() if value is not None and value != ''}
        system_specs.update({key: _const(value) for key, value in loop.items()})
        params = {key: value for key, value in params.items() if key not in LOOP_FIELDS or key in loop}
    if item['id'] == 'build_repair_loop_plan' and not ctx.get('verified_repair_ref'):
        system_specs.pop('verified_repair_ref', None)
        params.pop('verified_repair_ref', None)
    return CapabilitySpec(
        item['id'], item.get('op', ''),
        target_artifact_schemas=[item['target']] if item.get('target') else list(item.get('targets', [])),
        writable_artifact_schema=item.get('writes', ''), title=item.get('title', ''),
        description=item.get('desc', ''), use_when=list(item.get('use', [])), avoid_when=list(item.get('avoid', [])),
        task_type=item.get('task_type', 'single_operation_task'), semantic_schema=dict(item.get('semantic', {})),
        system_param_contract={name: _resolve_system(spec, ctx) for name, spec in system_specs.items()},
        effects=list(item.get('effects', [])), batch_policy=item.get('batch_policy', 'single'),
        cross_stage_policy=item.get('cross_stage_policy', 'runtime_bound'),
        params_schema=_schema(item.get('required', []), params),
        examples=[_op(item.get('op', ''), item.get('category', 'pipeline'), item.get('flow', 'dataset_gen'),
                      item.get('stage', item['id']), _resolve_required(item.get('requires', []), ctx))],
        risk_level=item.get('risk', 'low'), confirmation_policy=item.get('confirmation', 'none'),
    )


def _resolve_system(spec: Any, ctx: dict[str, Any]) -> dict[str, Any]:
    if not isinstance(spec, dict): return spec
    source = spec.get('source')
    if source == 'ctx': return _const(ctx.get(spec['key'], ''))
    if source == 'loop_system_param': return _const(ctx['loop_system_params'].get(spec['key'], ''))
    if source == 'load_sources':
        source_doc = {'type': 'kb', 'source_id': ctx['dataset_id'], 'dataset_id': ctx['dataset_id'],
                      'max_docs': 3, 'doc_page_size': 50}
        return _const([source_doc], {'max_docs': '0.max_docs'})
    if source == 'fine_refs':
        return _const([f'case_fine_classification_{case_id}@v1' for case_id in ctx['bad_case_ids']])
    if source == 'current_operation_id': return {'source': source, 'value': ctx.get('running_operation_id', '')}
    if source in {'constant', 'message_artifact_ref', 'message_artifact_id', 'message_operation_id',
                  'source_span_mention'}:
        return spec
    raise ValueError(f'unsupported capability system source: {source}')


def _resolve_required(required: Any, ctx: dict[str, Any]) -> list[str]:
    source = required.get('source') if isinstance(required, dict) else ''
    if source == 'classification_report_requires':
        return ['eval_report', *(f'case_fine_classification_{case_id}' for case_id in ctx['bad_case_ids'])]
    if source == 'strip_ref': return [str(ctx.get(required['key']) or '').rsplit('@v', 1)[0]]
    if source == 'abtest_compare_requires':
        refs = [ctx.get('abtest_baseline_report_ref', ''), ctx.get('abtest_candidate_report_ref', '')]
        return [ref.rsplit('@v', 1)[0] for ref in refs if ref]
    if source == 'candidate_cutover_requires':
        refs = [ctx.get('abtest_comparison_ref', ''), ctx.get('candidate_workspace_ref', '')]
        return [ref.rsplit('@v', 1)[0] for ref in refs if ref]
    return list(required or [])


def _op(operation_type: str, category: str, flow: str, stage: str, required: list[str]) -> dict[str, Any]:
    return {'operation_spec': {'operation_type': operation_type, 'category': category, 'flow_tag': flow,
                               'stage_tag': stage, 'required_artifact_ids': required or [],
                               'tags': {'evo_step': f'{flow}.{stage}'}}}


def _schema(required: list[str], props: dict[str, Any]) -> dict[str, Any]:
    return {'type': 'object', 'required': list(required), 'properties': copy.deepcopy(props),
            'additionalProperties': False}


def _const(value: Any, patch_semantic: dict[str, str] | None = None) -> dict[str, Any]:
    return {'source': 'constant', 'value': value, **({'patch_semantic': patch_semantic} if patch_semantic else {})}


def _case_template(template: str) -> dict[str, Any]:
    return {**CASE, 'template': template}


def _repair_artifact_ids() -> list[str]:
    prefixes = (
        'repair_evidence_packet', 'fault_localization', 'diagnostic_probe_plan', 'diagnostic_probe_result',
        'repair_diagnosis', 'opencode_instruction', 'opencode_explore_instruction', 'opencode_patch_instruction',
        'opencode_no_patch_instruction', 'opencode_probe_trace', 'opencode_patch_trace', 'repair_hypothesis',
        'repair_plan', 'opencode_run_trace', 'code_patch_candidate', 'candidate_service', 'candidate_service_run',
        'repair_evaluation', 'patch_correctness_assessment', 'patch_critique', 'branch_decision',
        'repair_branch_state_before', 'repair_branch_state_after', 'repair_state_transition',
        'candidate_classification_report', 'repair_loop_decision', 'repair_loop_memory', 'repair_loop_state',
    )
    ids = [f'{prefix}_attempt_{attempt}' for attempt in range(1, 4) for prefix in prefixes]
    for pattern in ('opencode_worker_report_attempt_{n}_patch', 'opencode_patch_worker_report_attempt_{n}',
                    'opencode_probe_worker_report_attempt_{n}', 'opencode_no_patch_worker_report_attempt_{n}',
                    'verified_repair_repair_loop_agent_attempt_{n}'):
        ids += [pattern.format(n=attempt) for attempt in range(1, 4)]
    return ['repair_loop_plan', 'verified_repair'] + ids
