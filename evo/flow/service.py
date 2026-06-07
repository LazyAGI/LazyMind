from __future__ import annotations

import json
import os
import re
import time
from dataclasses import asdict, dataclass, field
from pathlib import Path
from typing import Any, Callable

from ..artifacts import ArtifactDraft, ArtifactRef
from ..checkpoints import CheckpointManager
from ..intent import (
    CapabilityRegistry,
    IntentHarness,
    IntentOperationFactory,
    IntentRequest,
    LayeredIntentParser,
    layered_intent_prompt,
    parse_next_task,
    step_capabilities,
)
from ..operations import OperationGraph, OperationRunRef, OperationSpec
from ..operations.abtest import CompareABTestOperation, CutoverCandidateAlgorithmOperation
from ..operations.analysis import (
    AssembleClassificationReportOperation,
    CaseCoarseClassificationOperation,
    CaseFineClassificationOperation,
)
from ..operations.dataset import (
    AssembleDatasetOperation,
    BuildCorpusSnapshotOperation,
    GenerateDatasetCaseOperation,
    LoadCorpusOperation,
    PrepareDatasetCaseOperation,
)
from ..operations.eval import EvalAggregateOperation, JudgeAnswerOperation, RagAnswerOperation
from ..operations.intent import (
    IntentParseOperation,
    PatchArtifactOperation,
    ReadArtifactQueryOperation,
    ReadOperationQueryOperation,
    ReadRunStatusQueryOperation,
    RedirectResearchOperation,
    RegenerateDatasetCaseOperation,
    RejudgeCaseOperation,
    RespondToUserOperation,
)
from ..operations.repair import (
    BuildRepairLoopPlanOperation,
    PrepareCandidateWorkspaceOperation,
    RepairLoopAgentOperation,
    StartCandidateServiceOperation,
    StopCandidateServiceOperation,
    candidate_params,
    cleanup_candidate_artifacts,
)
from ..runtime import OperationResult, OperationRuntime, evo_llm, load_core_model_config
from ..store import (
    Event,
    EvoStore,
    CompactStoreCallRecorder,
    StoreOperationRunObserver,
    StoreProgressReporter,
    StoreRunLifecycle,
)


@dataclass(frozen=True)
class FlowMessageResult:
    message_id: str
    raw: dict[str, Any]
    action: str
    operation_refs: list[str] = field(default_factory=list)
    results: list[OperationResult] = field(default_factory=list)
    skipped: bool = False


class EvoFlowService:
    def __init__(
        self,
        *,
        run_root: Path | str,
        run_id: str = 'run_1',
        dataset_id: str,
        target_chat_url: str,
        candidate_chat_url: str = '',
        case_count: int = 20,
        max_workers: int = 4,
        model_config: dict[str, Any] | None = None,
    ):
        self._setup(
            run_root=run_root,
            run_id=run_id,
            dataset_id=dataset_id,
            target_chat_url=target_chat_url,
            candidate_chat_url=candidate_chat_url,
            case_count=case_count,
            max_workers=max_workers,
            model_config=model_config,
        )

    @classmethod
    def resume(
        cls,
        *,
        run_root: Path | str,
        run_id: str = 'run_1',
        dataset_id: str,
        target_chat_url: str,
        candidate_chat_url: str = '',
        case_count: int = 20,
        max_workers: int = 4,
        model_config: dict[str, Any] | None = None,
    ) -> 'EvoFlowService':
        service = cls.__new__(cls)
        service._setup(
            run_root=run_root,
            run_id=run_id,
            dataset_id=dataset_id,
            target_chat_url=target_chat_url,
            candidate_chat_url=candidate_chat_url,
            case_count=case_count,
            max_workers=max_workers,
            model_config=model_config,
            recover=True,
        )
        return service

    def _setup(
        self,
        *,
        run_root: Path | str,
        run_id: str,
        dataset_id: str,
        target_chat_url: str,
        candidate_chat_url: str,
        case_count: int,
        max_workers: int,
        model_config: dict[str, Any] | None,
        recover: bool = False,
    ) -> None:
        self.run_root = Path(run_root)
        self.run_id, self.dataset_id = run_id, dataset_id
        self.target_chat_url, self.candidate_chat_url = target_chat_url, candidate_chat_url
        self.case_count, self.max_workers = int(case_count), int(max_workers)
        self.model_config = model_config or load_core_model_config()
        self.llm = evo_llm(self.model_config)
        self.store = EvoStore(self.run_root / 'store')
        self.store.recover_run(run_id) if recover else self.store.create_run(run_id)
        self.graph = self.store.restore_operation_graph(run_id) if recover else OperationGraph()
        self.graph.add_observer(StoreOperationRunObserver(self.store, run_id))
        self.checkpoints = CheckpointManager(self.store)
        self.runtime = self._runtime()
        self.completed, self.bad_case_ids, self.loop_system_params = [], [], {}
        self.refresh_context()

    def plan_full_flow(self) -> None:
        self.plan_dataset()

    def delete(self) -> bool:
        cleanup_candidate_artifacts(self.store.run_dir(self.run_id))
        return self.store.delete_run(self.run_id)

    @classmethod
    def delete_run(cls, *, run_root: Path | str, run_id: str = 'run_1') -> bool:
        store = EvoStore(Path(run_root) / 'store')
        cleanup_candidate_artifacts(store.run_dir(run_id))
        return store.delete_run(run_id)

    def run_full_flow(
        self,
        *,
        include_repair_loop: bool = True,
        include_abtest: bool = True,
        start_stage: str = 'dataset',
        loop_system_params: dict[str, Any] | None = None,
        repair_plan_params: dict[str, Any] | None = None,
        after_stage: Callable[[str, dict[str, Any]], None] | None = None,
    ) -> dict[str, list[OperationResult]]:
        def notify(stage: str, **detail: Any) -> None:
            if after_stage:
                after_stage(stage, detail)

        stages = ('dataset', 'eval', 'analysis', 'repair', 'abtest')
        if start_stage not in stages:
            raise ValueError(f'unknown evo start_stage: {start_stage}')
        start = stages.index(start_stage)
        out: dict[str, list[OperationResult]] = {}
        self._flow_progress('full_flow', 'running', 'starting evo full flow')
        if start == 0:
            self.plan_dataset()
            out['dataset_corpus'] = self._dispatch_stage(
                'dataset_corpus', 'msg_flow_dataset_corpus', ['corpus_snapshot']
            )
            self.create_dataset_case_runs()
            out['dataset'] = self._dispatch_stage('dataset', 'msg_flow_dataset', ['eval_dataset'])
        eval_dataset_ref = self.artifacts.latest_ref('eval_dataset')
        if start == 0:
            notify('dataset', eval_dataset_ref=str(eval_dataset_ref))
        if start <= 1 and not _has_latest(self.artifacts, 'eval_report'):
            self.create_eval_runs(eval_dataset_ref)
            out['eval'] = self._dispatch_stage('eval', 'msg_flow_eval', ['eval_report'])
        eval_report_ref = self.artifacts.latest_ref('eval_report')
        if start <= 1:
            notify('eval', eval_report_ref=str(eval_report_ref))
        if start <= 2:
            self.create_analysis_runs(eval_report_ref)
            out['analysis'] = (
                self._dispatch_stage('analysis', 'msg_flow_analysis', ['classification_report'])
                if self.bad_case_ids
                else []
            )
            notify('analysis', classification_report_ref=_latest_or(self.artifacts, 'classification_report'))
        else:
            self.refresh_context()
        if not self.bad_case_ids:
            if include_repair_loop:
                self._flow_progress('repair_loop', 'skipped', 'no badcase; repair loop skipped')
            if include_abtest:
                self._flow_progress('abtest_compare', 'skipped', 'no badcase; abtest skipped')
            self.refresh_context()
            self._flow_progress('full_flow', 'success', 'evo full flow finished')
            return out
        if include_repair_loop and start <= 3:
            if not _has_latest(self.artifacts, 'repair_loop_plan'):
                self.create_repair_plan_run(self.artifacts.latest_ref('classification_report'), repair_plan_params)
                out['repair_plan'] = self._dispatch_stage('repair_plan', 'msg_flow_repair_plan', ['repair_loop_plan'])
            if not _has_latest(self.artifacts, 'candidate_workspace'):
                workspace_ref = self.create_candidate_workspace_run(loop_system_params)
                out['candidate_workspace'] = self._dispatch_stage(
                    'candidate_workspace', 'msg_flow_candidate_workspace', ['candidate_workspace']
                )
            else:
                self.recover_candidate_context()
                workspace_ref = self.graph.active_run_for('repair.candidate_workspace')
            if not self._latest_ref_prefix('verified_repair_'):
                self.create_repair_loop_run(
                    loop_system_params=self.loop_system_params,
                    depends_on=[workspace_ref] if workspace_ref else None,
                    inputs=[self.artifacts.latest_ref('candidate_workspace')],
                )
                out['repair_loop'] = self._dispatch_stage('repair_loop', 'msg_flow_repair_loop', [])
            self._require_repair_candidate()
            notify('repair', verified_repair_ref=str(self._latest_ref_prefix('verified_repair_') or ''))
        if include_abtest and start <= 4:
            self.recover_candidate_context()
            if not self.candidate_chat_url:
                raise RuntimeError('ABTest requires candidate_chat_url or repair loop candidate service params')
            service_ref = None
            try:
                comparison_existed = _has_latest(self.artifacts, 'abtest_comparison')
                if not _has_latest(self.artifacts, 'candidate_eval_report'):
                    service_ref = self.create_candidate_service_run() if include_repair_loop else None
                    out['candidate_service_start'] = self._dispatch_stage(
                        'candidate_service_start', 'msg_flow_candidate_service_start', ['candidate_service']
                    ) if service_ref else []
                    self._create_candidate_eval_run(eval_dataset_ref, depends_on=[service_ref] if service_ref else None)
                    out['candidate_eval'] = self._dispatch_stage(
                        'candidate_eval', 'msg_flow_candidate_eval', ['candidate_eval_report'], max_workers=1
                    )
                if not _has_latest(self.artifacts, 'abtest_comparison'):
                    self.create_abtest_compare_run(eval_report_ref, self.artifacts.latest_ref('candidate_eval_report'))
                    out['abtest_compare'] = self._dispatch_stage(
                        'abtest_compare', 'msg_flow_abtest_compare', ['abtest_comparison']
                    )
                comparison_ref = self.artifacts.latest_ref('abtest_comparison')
                accepted = (self.artifacts.get(comparison_ref).get('decision') or {}).get('status') == 'accept'
                if accepted and not _has_latest(self.artifacts, 'candidate_algorithm_cutover'):
                    if not comparison_existed:
                        notify(
                            'abtest',
                            abtest_comparison_ref=str(comparison_ref),
                            next_stage='abtest',
                            next_op='abtest.candidate_cutover',
                            message='ABTest 对比已完成，候选版本满足切流条件，请确认是否注册候选算法并切换 chat 服务。',
                        )
                    cutover_ref = self.create_candidate_cutover_run()
                    if cutover_ref:
                        out['candidate_cutover'] = self._dispatch_stage(
                            'candidate_cutover', 'msg_flow_candidate_cutover', ['candidate_algorithm_cutover']
                        )
            finally:
                stop_source = service_ref or self.graph.active_run_for('abtest.candidate_service.start')
                if stop_source and _has_latest(self.artifacts, 'candidate_service') \
                        and not _has_latest(self.artifacts, 'candidate_service_stop'):
                    stop_ref = self.create_candidate_service_stop_run(stop_source)
                    out['candidate_service_stop'] = self._dispatch_stop(stop_ref)
                elif service_ref:
                    cleanup_candidate_artifacts(self.store.run_dir(self.run_id))
                    self._flow_progress(
                        'candidate_service_stop', 'success', 'candidate service cleanup finished before startup'
                    )
            notify('abtest', abtest_comparison_ref=_latest_or(self.artifacts, 'abtest_comparison'), terminal=True)
        self.refresh_context()
        self._flow_progress('full_flow', 'success', 'evo full flow finished')
        return out

    def plan_dataset(self) -> None:
        self.graph.register_default_graph(self._dataset_specs())

    def create_dataset_case_runs(self) -> None:
        try:
            self.artifacts.latest_ref('eval_dataset')
            return
        except KeyError:
            pass
        question_types = self._available_question_types()
        snapshot_ref = str(self.artifacts.latest_ref('corpus_snapshot'))
        self._flow_progress('dataset', 'running', 'planning dataset cases', {'question_types': question_types})
        for index in range(1, self.case_count + 1):
            case_id = f'case_{index:04d}'
            question_type = question_types[(index - 1) % len(question_types)]
            self._create_run(
                f'dataset.prepare.{case_id}',
                'PrepareDatasetCaseOperation',
                flow_tag='dataset_gen',
                stage_tag='prepare_case',
                depends_on=['dataset.build_corpus_snapshot'],
                required_artifact_ids=['corpus_snapshot'],
                params={
                    'source_snapshot_ref': snapshot_ref,
                    'output_case_id': case_id,
                    'question_type': question_type,
                    'difficulty': 'hard',
                    'user_instruction': f'生成第 {index} 条评测样本；问题必须独立完整，答案必须来自参考内容。',
                },
            )
            self._create_run(
                f'dataset.generate.{case_id}',
                'GenerateDatasetCaseOperation',
                flow_tag='dataset_gen',
                stage_tag='generate_case',
                depends_on=[f'dataset.prepare.{case_id}'],
                required_artifact_ids=[f'case_preparation_{case_id}'],
                params={'case_preparation_ref': f'case_preparation_{case_id}@v1'},
                tags={'evo_step': 'dataset_gen.generate_case', 'writes_artifact_id': case_id},
            )
        case_ids = [f'case_{index:04d}' for index in range(1, self.case_count + 1)]
        self._create_run(
            'dataset.assemble',
            'AssembleDatasetOperation',
            flow_tag='dataset_gen',
            stage_tag='assemble',
            depends_on=[f'dataset.generate.{case_id}' for case_id in case_ids],
            required_artifact_ids=case_ids,
            params={'dataset_id': 'eval_dataset', 'case_ids': case_ids},
            tags={'writes_artifact_id': 'eval_dataset'},
        )

    def create_eval_runs(self, eval_dataset_ref: ArtifactRef | str | None = None) -> None:
        dataset_ref = eval_dataset_ref or self.artifacts.latest_ref('eval_dataset')
        dataset_ref = _ref(dataset_ref)
        self._create_eval_report_runs('eval', dataset_ref, self.target_chat_url, 'eval_report')

    def create_analysis_runs(self, eval_report_ref: ArtifactRef | str | None = None) -> None:
        report_ref = eval_report_ref or self.artifacts.latest_ref('eval_report')
        report_ref = _ref(report_ref)
        report = self.artifacts.get(report_ref)
        self.bad_case_ids = [str(row['case_id']) for row in report.get('bad_cases') or [] if row.get('case_id')]
        fine_refs = []
        for case_id in self.bad_case_ids:
            self._create_run(
                f'analysis.coarse.{case_id}',
                'CaseCoarseClassificationOperation',
                flow_tag='analysis',
                stage_tag='coarse_classify',
                required_artifact_ids=['eval_report'],
                tags={
                    'evo_step': 'analysis.coarse_classify',
                    'writes_artifact_id': f'case_coarse_classification_{case_id}',
                },
                params={'eval_report_ref': str(report_ref), 'case_id': case_id,
                        'output_id': f'case_coarse_classification_{case_id}'},
                inputs=[report_ref],
            )
            fine_refs.append(f'case_fine_classification_{case_id}@v1')
            self._create_run(
                f'analysis.fine.{case_id}',
                'CaseFineClassificationOperation',
                flow_tag='analysis',
                stage_tag='fine_classify',
                required_artifact_ids=[f'case_coarse_classification_{case_id}'],
                tags={
                    'evo_step': 'analysis.fine_classify',
                    'writes_artifact_id': f'case_fine_classification_{case_id}',
                },
                params={'coarse_classification_ref': f'case_coarse_classification_{case_id}@v1',
                        'output_id': f'case_fine_classification_{case_id}'},
                run_depends_on=[OperationRunRef(f'analysis.coarse.{case_id}')],
            )
        if fine_refs:
            self._create_run(
                'analysis.classification_report',
                'AssembleClassificationReportOperation',
                flow_tag='analysis',
                stage_tag='classification_report',
                required_artifact_ids=[ref.split('@', 1)[0] for ref in fine_refs],
                tags={'evo_step': 'analysis.classification_report', 'writes_artifact_id': 'classification_report'},
                params={'eval_report_ref': str(report_ref), 'fine_classification_refs': fine_refs,
                        'output_id': 'classification_report'},
                inputs=[report_ref],
                run_depends_on=[OperationRunRef(f'analysis.fine.{case_id}') for case_id in self.bad_case_ids],
            )

    def create_repair_plan_run(
        self,
        classification_report_ref: ArtifactRef | str | None = None,
        params: dict[str, Any] | None = None,
    ) -> OperationRunRef:
        report_ref = _ref(classification_report_ref or self.artifacts.latest_ref('classification_report'))
        return self._create_run(
            'repair.plan',
            'BuildRepairLoopPlanOperation',
            flow_tag='repair',
            stage_tag='plan',
            required_artifact_ids=['classification_report'],
            tags={'evo_step': 'repair.plan', 'writes_artifact_id': 'repair_loop_plan'},
            params={'classification_report_ref': str(report_ref), 'output_id': 'repair_loop_plan', **(params or {})},
            inputs=[report_ref],
        )

    def create_repair_loop_run(
        self,
        repair_loop_plan_ref: ArtifactRef | str | None = None,
        *,
        loop_system_params: dict[str, Any] | None = None,
        depends_on: list[OperationRunRef] | None = None,
        inputs: list[ArtifactRef] | None = None,
    ) -> OperationRunRef:
        if loop_system_params is not None:
            self.loop_system_params = dict(loop_system_params)
            self.refresh_context()
        plan_ref = _ref(repair_loop_plan_ref or self.artifacts.latest_ref('repair_loop_plan'))
        params = {'repair_loop_plan_ref': str(plan_ref), 'output_id': 'repair_loop_agent'}
        params.update(self.loop_system_params)
        return self._create_run(
            'repair.loop',
            'RepairLoopAgentOperation',
            flow_tag='repair',
            stage_tag='repair_loop',
            required_artifact_ids=['repair_loop_plan'],
            tags={'evo_step': 'repair.loop', 'writes_artifact_id': 'repair_loop_agent'},
            params=params,
            inputs=[plan_ref, *(inputs or [])],
            run_depends_on=depends_on,
        )

    def create_candidate_workspace_run(self, params: dict[str, Any] | None = None) -> OperationRunRef:
        self.loop_system_params = candidate_params(
            run_root=self.store.run_dir(self.run_id),
            dataset_name=self.dataset_id,
            overrides=params,
        )
        self.candidate_chat_url = str(self.loop_system_params['candidate_chat_url'])
        self.refresh_context()
        return self._create_run(
            'repair.candidate_workspace',
            'PrepareCandidateWorkspaceOperation',
            flow_tag='repair',
            stage_tag='candidate_workspace',
            tags={'evo_step': 'repair.candidate_workspace', 'writes_artifact_id': 'candidate_workspace'},
            params={**self.loop_system_params, 'output_id': 'candidate_workspace'},
        )

    def create_candidate_service_run(self) -> OperationRunRef:
        workspace_ref = self.artifacts.latest_ref('candidate_workspace')
        return self._create_run(
            'abtest.candidate_service.start',
            'StartCandidateServiceOperation',
            flow_tag='abtest',
            stage_tag='candidate_service_start',
            required_artifact_ids=['candidate_workspace'],
            tags={'evo_step': 'abtest.candidate_service.start', 'writes_artifact_id': 'candidate_service'},
            params={
                **self.loop_system_params,
                'candidate_workspace_ref': str(workspace_ref),
                'output_id': 'candidate_service',
            },
            inputs=[workspace_ref],
        )

    def create_candidate_service_stop_run(self, service_ref: OperationRunRef) -> OperationRunRef:
        candidate_service_ref = str(self.artifacts.latest_ref('candidate_service'))
        return self._create_run(
            'abtest.candidate_service.stop',
            'StopCandidateServiceOperation',
            flow_tag='abtest',
            stage_tag='candidate_service_stop',
            required_artifact_ids=['candidate_service'],
            tags={'evo_step': 'abtest.candidate_service.stop', 'writes_artifact_id': 'candidate_service_stop'},
            params={'candidate_service_ref': candidate_service_ref, 'output_id': 'candidate_service_stop'},
            inputs=[ArtifactRef.parse(candidate_service_ref)],
            run_depends_on=[service_ref],
        )

    def _create_candidate_eval_run(
        self,
        eval_dataset_ref: ArtifactRef | str | None = None,
        *,
        depends_on: list[OperationRunRef] | None = None,
    ) -> OperationRunRef:
        if not self.candidate_chat_url or self.candidate_chat_url == self.target_chat_url:
            raise ValueError('candidate_chat_url must be present and differ from target_chat_url')
        dataset_ref = _ref(eval_dataset_ref or self.artifacts.latest_ref('eval_dataset'))
        candidate_ref, _ = self._create_eval_report_runs(
            'candidate_eval',
            dataset_ref,
            self.candidate_chat_url,
            'candidate_eval_report',
            depends_on=depends_on,
            candidate_service_ref=str(self.artifacts.latest_ref('candidate_service')) if depends_on else '',
        )
        return candidate_ref

    def create_abtest_compare_run(
        self,
        baseline_eval_report_ref: ArtifactRef | str,
        candidate_eval_report_ref: ArtifactRef | str,
    ) -> OperationRunRef:
        baseline_ref = _ref(baseline_eval_report_ref)
        candidate_ref = _ref(candidate_eval_report_ref)
        return self._create_run(
            'abtest.compare',
            'CompareABTestOperation',
            flow_tag='abtest',
            stage_tag='compare',
            required_artifact_ids=[baseline_ref.artifact_id, candidate_ref.artifact_id],
            tags={'evo_step': 'abtest.compare', 'writes_artifact_id': 'abtest_comparison'},
            params={'baseline_eval_report_ref': str(baseline_ref), 'candidate_eval_report_ref': str(candidate_ref),
                    'output_id': 'abtest_comparison'},
            inputs=[baseline_ref, candidate_ref],
        )

    def create_candidate_cutover_run(self) -> OperationRunRef | None:
        comparison_ref = self.artifacts.latest_ref('abtest_comparison')
        comparison = self.artifacts.get(comparison_ref)
        if (comparison.get('decision') or {}).get('status') != 'accept':
            self._flow_progress('candidate_cutover', 'skipped', 'abtest rejected candidate; cutover skipped')
            return None
        workspace_ref = self.artifacts.latest_ref('candidate_workspace')
        algorithm_id = f'evo_{self.run_id}_{int(time.time())}'
        return self._create_run(
            'abtest.candidate_cutover',
            'CutoverCandidateAlgorithmOperation',
            flow_tag='abtest',
            stage_tag='candidate_cutover',
            required_artifact_ids=['abtest_comparison', 'candidate_workspace'],
            tags={'evo_step': 'abtest.candidate_cutover', 'writes_artifact_id': 'candidate_algorithm_cutover'},
            params={'abtest_comparison_ref': str(comparison_ref), 'candidate_workspace_ref': str(workspace_ref),
                    'target_chat_url': self.target_chat_url, 'algorithm_id': algorithm_id,
                    'output_id': 'candidate_algorithm_cutover'},
            inputs=[comparison_ref, workspace_ref],
        )

    def recover_candidate_context(self) -> None:
        if self.candidate_chat_url and self.loop_system_params:
            return
        ref = self.graph.active_run_for('repair.candidate_workspace')
        if not ref:
            return
        params = dict(self.graph.get_run(ref).spec.params or {})
        if params.get('candidate_chat_url'):
            self.loop_system_params = params
            self.candidate_chat_url = str(params['candidate_chat_url'])
            self.refresh_context()

    def send_message(
        self,
        message_id: str,
        message: str,
        *,
        allowed_capabilities: list[str] | None = None,
        dispatch: bool = True,
        max_dispatch: int | None = 1,
    ) -> FlowMessageResult:
        self.refresh_context()
        if _is_resume_message(message) and self.graph.run_refs({'checkpointed'}):
            outputs = self.resume_checkpointed()
            self._remember({'capability_id': 'resume_checkpointed', 'result_summary': {'status': 'ended'}})
            return FlowMessageResult(message_id, {'next_task': {'type': 'runtime_control'}}, 'resume_checkpointed',
                                     results=outputs, skipped=True)
        allowed = allowed_capabilities or self.registry.capability_ids()
        StoreRunLifecycle(self.store, self.run_id).open_dispatch(message_id=message_id)
        checkpoint = self.checkpoints.create_checkpoint(self.run_id, None, message, allowed_capabilities=allowed)
        message_ref = self.artifacts.commit_artifact(ArtifactDraft(
            f'user_message_{message_id}', 'UserMessage', {'message_id': message_id, 'message': message}, 'user',
            role='external_input',
        ))
        capabilities = self.registry.planning_context(self.store, self.run_id, checkpoint)
        raw = _forced_intervention_task(message, allowed, capabilities)
        if raw:
            parse_artifact_ref = self.artifacts.commit_artifact(ArtifactDraft(
                f'intent_parse_{message_id}',
                'IntentParse',
                {
                    'message_id': message_id,
                    'message': message,
                    'checkpoint_id': checkpoint.checkpoint_id,
                    'capabilities': capabilities,
                    'raw_response': raw,
                    'call_id': '',
                },
                'intent_parser:deterministic',
                input_refs=[message_ref],
            ))
        else:
            parse_ref = self._create_run(
                f'intent.parse.{message_id}',
                'IntentParseOperation',
                category='intent',
                params={
                    'message_id': message_id,
                    'message': message,
                    'checkpoint_id': checkpoint.checkpoint_id,
                    'capabilities': capabilities,
                    'prompt': layered_intent_prompt(message, capabilities, completed_tasks=self.completed),
                },
                inputs=[message_ref],
            )
            parse_result = self._run_single(parse_ref)
            if _has_error(parse_result):
                raise RuntimeError(f'intent parse failed: {parse_result}')
            parse_artifact_ref = self.artifacts.latest_ref(f'intent_parse_{message_id}')
            raw = _normalize_readonly_query(message, self.artifacts.get(parse_artifact_ref)['raw_response'],
                                            self.artifacts)
        result = IntentHarness(
            self.store,
            self.run_id,
            checkpoint,
            LayeredIntentParser(raw),
            self.registry,
            self.factory,
        ).handle(IntentRequest(message_id, message, checkpoint.checkpoint_id, message_ref, parse_artifact_ref))
        operation_refs = [str(proposal.operation_ref) for proposal in result.proposals]
        if result.action != 'propose_operations':
            self._remember(_completed(message_id, result, []))
            return FlowMessageResult(message_id, {'next_task': parse_next_task(raw)}, result.action, operation_refs)
        outputs, skipped = self._apply_control(result, message_id)
        if dispatch and not skipped:
            old_limit, old_workers = self.runtime.max_dispatch, self.runtime.max_workers
            self.runtime.max_dispatch = max_dispatch
            if max_dispatch == 1:
                self.runtime.max_workers = 1
            try:
                outputs = []
                for proposal in result.proposals:
                    StoreRunLifecycle(self.store, self.run_id).open_dispatch(message_id=message_id)
                    outputs.append(self.runtime.run(proposal.operation_ref))
            finally:
                self.runtime.max_dispatch, self.runtime.max_workers = old_limit, old_workers
        self.refresh_context()
        self._remember(_completed(message_id, result, outputs))
        return FlowMessageResult(
            message_id,
            {'next_task': parse_next_task(raw)},
            result.intents[0].action,
            operation_refs,
            outputs,
            skipped,
        )

    def dispatch(self, operation_ref: OperationRunRef | None = None, *, message_id: str = 'msg_dispatch',
                 max_dispatch: int | None = None) -> list[OperationResult]:
        StoreRunLifecycle(self.store, self.run_id).open_dispatch(message_id=message_id)
        old_limit = self.runtime.max_dispatch
        if max_dispatch is not None:
            self.runtime.max_dispatch = max_dispatch
        try:
            return self.runtime.dispatch(operation_ref)
        finally:
            self.runtime.max_dispatch = old_limit

    def _run_single(self, operation_ref: OperationRunRef) -> OperationResult:
        old_limit, old_workers = self.runtime.max_dispatch, self.runtime.max_workers
        self.runtime.max_dispatch, self.runtime.max_workers = 1, 1
        try:
            return self.runtime.run(operation_ref)
        finally:
            self.runtime.max_dispatch, self.runtime.max_workers = old_limit, old_workers

    def pause(self, operation_ref: OperationRunRef | str) -> OperationResult:
        operation_ref = operation_ref if isinstance(operation_ref, OperationRunRef) else OperationRunRef(operation_ref)
        self.runtime.request_interrupt(operation_ref)
        return self.runtime.settle_running(operation_ref)

    def resume_checkpointed(self) -> list[OperationResult]:
        for ref in self.graph.run_refs({'checkpointed'}):
            self.graph.reset_run(ref)
        return self.dispatch(message_id='msg_resume')

    def refresh_context(self) -> None:
        self.bad_case_ids = self._bad_cases()
        self.registry = self._registry()
        self.factory = self._factory()

    @property
    def artifacts(self):
        return self.store.artifact_graph(self.run_id)

    def _create_run(self, operation_id: str, operation_type: str, *, inputs=None, run_depends_on=None, **spec: Any):
        return self.graph.create_run(
            OperationSpec(operation_id, operation_type, **spec), inputs=inputs or [], depends_on=run_depends_on
        )

    def progress_events(self, limit: int | None = None) -> list[dict[str, Any]]:
        events = [
            event.payload for event in self.store.read_events(self.run_id)
            if event.event_type == 'operation.progress'
        ]
        return events[-limit:] if limit else events

    def flow_events(self, limit: int | None = None) -> list[dict[str, Any]]:
        events = [
            event.payload for event in self.store.read_events(self.run_id)
            if event.event_type == 'evo_flow.progress'
        ]
        return events[-limit:] if limit else events

    def _dispatch_stage(
        self,
        stage: str,
        message_id: str,
        required_artifact_ids: list[str],
        *,
        max_workers: int | None = None,
    ) -> list[OperationResult]:
        self._flow_progress(stage, 'running', f'{stage} started')
        old_workers = self.runtime.max_workers
        if max_workers is not None:
            self.runtime.max_workers = max(1, int(max_workers))
        results: list[OperationResult] = []
        try:
            while True:
                batch = self.dispatch(message_id=message_id)
                results.extend(batch)
                failed = [str(ref) for ref in self._latest_failed_operation_refs()]
                missing = [
                    artifact_id for artifact_id in required_artifact_ids
                    if not _has_latest(self.artifacts, artifact_id)
                ]
                if failed or not missing:
                    break
                if not batch or not self.graph.schedule_state().ready:
                    break
                StoreRunLifecycle(self.store, self.run_id).open_dispatch(message_id=message_id)
        finally:
            self.runtime.max_workers = old_workers
        failed = [str(ref) for ref in self._latest_failed_operation_refs()]
        missing = [artifact_id for artifact_id in required_artifact_ids if not _has_latest(self.artifacts, artifact_id)]
        if failed or missing:
            detail = {'failed_operations': failed, 'missing_artifacts': missing}
            self._flow_progress(stage, 'failed', f'{stage} failed', detail)
            raise RuntimeError(f'{stage} failed: {detail}')
        self.refresh_context()
        self._flow_progress(stage, 'success', f'{stage} finished', {'result_count': len(results)})
        return results

    def _dispatch_stop(self, stop_ref: OperationRunRef) -> list[OperationResult]:
        self._flow_progress('candidate_service_stop', 'running', 'candidate_service_stop started')
        StoreRunLifecycle(self.store, self.run_id).open_dispatch(message_id='msg_flow_candidate_service_stop')
        result = self.runtime.run(stop_ref)
        if _has_error(result) or not _has_latest(self.artifacts, 'candidate_service_stop'):
            detail = {'operation_ref': result.operation_run_id, 'output_refs': [str(ref) for ref in result.output_refs]}
            self._flow_progress('candidate_service_stop', 'failed', 'candidate_service_stop failed', detail)
            raise RuntimeError(f'candidate_service_stop failed: {detail}')
        self._flow_progress('candidate_service_stop', 'success', 'candidate_service_stop finished')
        return [result]

    def _flow_progress(
        self,
        stage: str,
        status: str,
        message: str,
        detail: dict[str, Any] | None = None,
    ) -> None:
        self.store.append_event(Event('evo_flow.progress', self.run_id, {
            'stage': stage,
            'status': status,
            'message': message,
            'detail': detail or {},
            'timestamp': time.time(),
        }))

    def _require_repair_candidate(self) -> None:
        verified_ref = self._latest_ref_prefix('verified_repair_')
        if not verified_ref:
            self._flow_progress('repair_loop', 'failed', 'repair loop produced no verified repair')
            raise RuntimeError('repair loop produced no verified repair')
        verified = self.artifacts.get(verified_ref)
        if verified.get('status') != 'ready_for_review':
            detail = {'verified_ref': str(verified_ref), 'status': verified.get('status')}
            self._flow_progress('repair_loop', 'failed', 'verified repair is not ready', detail)
            raise RuntimeError(f'verified repair is not ready: {detail}')
        self._flow_progress(
            'repair_loop', 'success', 'verified repair ready for final ABTest', {'verified_ref': str(verified_ref)}
        )

    def _latest_ref_prefix(self, prefix: str) -> ArtifactRef | None:
        refs = []
        for manifest in self.artifacts.manifest_dir.glob(f'{prefix}*.json'):
            try:
                refs.append(self.artifacts.latest_ref(manifest.stem))
            except KeyError:
                pass
        return sorted(refs, key=lambda ref: ref.artifact_id)[-1] if refs else None

    def _runtime(self) -> OperationRuntime:
        executors = {
            'LoadCorpusOperation': LoadCorpusOperation(),
            'BuildCorpusSnapshotOperation': BuildCorpusSnapshotOperation(),
            'PrepareDatasetCaseOperation': PrepareDatasetCaseOperation(self.llm),
            'GenerateDatasetCaseOperation': GenerateDatasetCaseOperation(self.llm),
            'AssembleDatasetOperation': AssembleDatasetOperation(),
            'RagAnswerOperation': RagAnswerOperation(self.model_config),
            'JudgeAnswerOperation': JudgeAnswerOperation(self.llm),
            'EvalAggregateOperation': EvalAggregateOperation(),
            'CaseCoarseClassificationOperation': CaseCoarseClassificationOperation(),
            'CaseFineClassificationOperation': CaseFineClassificationOperation(self.llm),
            'AssembleClassificationReportOperation': AssembleClassificationReportOperation(),
            'BuildRepairLoopPlanOperation': BuildRepairLoopPlanOperation(),
            'PrepareCandidateWorkspaceOperation': PrepareCandidateWorkspaceOperation(),
            'RepairLoopAgentOperation': RepairLoopAgentOperation(self.llm, self.model_config),
            'StartCandidateServiceOperation': StartCandidateServiceOperation(),
            'StopCandidateServiceOperation': StopCandidateServiceOperation(),
            'CompareABTestOperation': CompareABTestOperation(),
            'CutoverCandidateAlgorithmOperation': CutoverCandidateAlgorithmOperation(),
            'IntentParseOperation': IntentParseOperation(self.llm),
            'ReadArtifactQueryOperation': ReadArtifactQueryOperation(),
            'ReadOperationQueryOperation': ReadOperationQueryOperation(self.store),
            'ReadRunStatusQueryOperation': ReadRunStatusQueryOperation(self.store),
            'PatchArtifactOperation': PatchArtifactOperation(),
            'RegenerateDatasetCaseOperation': RegenerateDatasetCaseOperation(),
            'RejudgeCaseOperation': RejudgeCaseOperation(),
            'RedirectResearchOperation': RedirectResearchOperation(),
            'RespondToUserOperation': RespondToUserOperation(),
        }
        return OperationRuntime(
            run_id=self.run_id,
            operation_graph=self.graph,
            artifact_graph=self.artifacts,
            executors=executors,
            draft_root=self.store.run_dir(self.run_id) / 'tmp' / 'drafts',
            progress_reporter=StoreProgressReporter(self.store, self.run_id),
            call_recorder_factory=lambda op_id: CompactStoreCallRecorder(self.store, self.run_id, op_id),
            run_lifecycle=StoreRunLifecycle(self.store, self.run_id),
            max_dispatch=500,
            max_workers=self.max_workers,
        )

    def _registry(self) -> CapabilityRegistry:
        baseline_ref = _latest_or(self.artifacts, 'eval_report')
        try:
            baseline_ref = str(self.artifacts.latest_ref('baseline_eval_report'))
        except KeyError:
            pass
        candidate_ref = 'candidate_eval_report@v1'
        try:
            candidate_ref = str(self.artifacts.latest_ref('candidate_eval_report'))
        except KeyError:
            pass
        return CapabilityRegistry(step_capabilities(
            run_id=self.run_id,
            dataset_id=self.dataset_id,
            eval_dataset_ref=_latest_or(self.artifacts, 'eval_dataset'),
            eval_report_ref=_latest_or(self.artifacts, 'eval_report'),
            classification_report_ref=_latest_or(self.artifacts, 'classification_report'),
            abtest_baseline_report_ref=baseline_ref,
            abtest_candidate_report_ref=candidate_ref,
            abtest_comparison_ref=_latest_or(self.artifacts, 'abtest_comparison'),
            candidate_workspace_ref=_latest_or(self.artifacts, 'candidate_workspace'),
            bad_case_ids=self.bad_case_ids,
            target_chat_url=self.target_chat_url,
            running_operation_id=_running_operation_id(self.graph),
            loop_system_params=self.loop_system_params,
        ))

    def _factory(self) -> IntentOperationFactory:
        return IntentOperationFactory(
            store=self.store,
            operation_graph=self.graph,
            capability_registry=self.registry,
            checkpoint_manager=self.checkpoints,
        )

    def _dataset_specs(self) -> list[OperationSpec]:
        return [
            OperationSpec(
                'dataset.load_corpus',
                'LoadCorpusOperation',
                flow_tag='dataset_gen',
                stage_tag='load_corpus',
                params={'sources': [{
                    'type': 'kb',
                    'source_id': self.dataset_id,
                    'dataset_id': self.dataset_id,
                    'max_docs': int(os.getenv('EVO_FLOW_MAX_DOCS', '8')),
                    'doc_page_size': int(os.getenv('EVO_FLOW_DOC_PAGE_SIZE', '1000')),
                }]},
            ),
            OperationSpec(
                'dataset.build_corpus_snapshot',
                'BuildCorpusSnapshotOperation',
                flow_tag='dataset_gen',
                stage_tag='build_corpus_snapshot',
                depends_on=['dataset.load_corpus'],
                required_artifact_ids=['corpus_load_report'],
                params={
                    'source_report_ref': 'corpus_load_report@v1',
                    'segment_page_size': int(os.getenv('EVO_FLOW_SEGMENT_PAGE_SIZE', '1000')),
                    'min_segment_chars': int(os.getenv('EVO_FLOW_MIN_SEGMENT_CHARS', '80')),
                    'segment_groups': ['block', 'line'],
                },
            ),
        ]

    def _available_question_types(self) -> list[str]:
        snapshot = self.artifacts.get(self.artifacts.latest_ref('corpus_snapshot'))
        stats, doc_counts = snapshot.get('stats', {}), self._snapshot_doc_unit_counts(snapshot)
        counts = stats.get('unit_type_counts', {})
        if int(counts.get('paragraph') or 0) < 1:
            raise RuntimeError('corpus_snapshot has no paragraph source units for dataset generation')
        types = ['single_hop']
        if any(count >= 2 for count in doc_counts.values()):
            types.append('single_doc_multi_hop')
        if int(stats.get('document_with_units_count') or 0) >= 2:
            types.append('multi_doc_multi_hop')
        if int(counts.get('table') or 0) + int(counts.get('list') or 0) + int(counts.get('mixed') or 0):
            types.append('table_list')
        if int(counts.get('formula') or 0) + int(counts.get('mixed') or 0):
            types.append('formula')
        return types

    def _snapshot_doc_unit_counts(self, snapshot: dict[str, Any]) -> dict[str, int]:
        counts: dict[str, int] = {}
        for ref in snapshot.get('source_unit_page_refs') or []:
            for unit in self.artifacts.get(ArtifactRef.parse(str(ref))).get('source_units', []):
                if str(unit.get('unit_type') or 'paragraph') == 'paragraph':
                    doc_id = str(unit.get('doc_id') or '')
                    counts[doc_id] = counts.get(doc_id, 0) + 1
        return counts

    def _create_eval_report_runs(
        self,
        prefix: str,
        dataset_ref: ArtifactRef,
        chat_url: str,
        report_id: str,
        *,
        depends_on: list[OperationRunRef] | None = None,
        candidate_service_ref: str = '',
    ) -> tuple[OperationRunRef, dict[str, tuple[OperationRunRef, OperationRunRef]]]:
        case_ids = list(self.artifacts.get(dataset_ref)['case_ids'])
        case_runs = {
            case_id: self._create_eval_case_runs(
                prefix,
                dataset_ref,
                case_id,
                chat_url,
                depends_on=depends_on,
                candidate_service_ref=candidate_service_ref,
            )
            for case_id in case_ids
        }
        aggregate = self._create_run(
            f'{prefix}.aggregate',
            'EvalAggregateOperation',
            flow_tag='eval',
            stage_tag='aggregate',
            required_artifact_ids=[
                dataset_ref.artifact_id,
                *[f'judge_result_{case_id}' for case_id in case_ids],
            ],
            tags={'evo_step': 'eval.aggregate', 'writes_artifact_id': report_id},
            params={'eval_dataset_ref': str(dataset_ref), 'report_id': report_id},
            inputs=[dataset_ref],
            run_depends_on=[case_runs[case_id][1] for case_id in case_ids],
        )
        return aggregate, case_runs

    def _create_eval_case_runs(
        self,
        prefix: str,
        dataset_ref: ArtifactRef,
        case_id: str,
        chat_url: str,
        *,
        depends_on: list[OperationRunRef] | None,
        candidate_service_ref: str = '',
    ) -> tuple[OperationRunRef, OperationRunRef]:
        common = {'eval_dataset_ref': str(dataset_ref), 'case_id': case_id}
        params = {
            **common,
            'target_chat_url': chat_url,
            'dataset_name': self.dataset_id,
            'require_trace': True,
        }
        if candidate_service_ref:
            params['candidate_service_ref'] = candidate_service_ref
        rag = self._create_run(
            f'{prefix}.rag.{case_id}',
            'RagAnswerOperation',
            flow_tag='eval',
            stage_tag='rag_answer',
            required_artifact_ids=[
                dataset_ref.artifact_id,
                *(['candidate_service'] if candidate_service_ref else []),
            ],
            tags={'evo_step': 'eval.rag_answer', 'writes_artifact_id': f'rag_answer_{case_id}'},
            params=params,
            inputs=[dataset_ref],
            run_depends_on=depends_on,
        )
        judge = self._create_run(
            f'{prefix}.judge.{case_id}',
            'JudgeAnswerOperation',
            flow_tag='eval',
            stage_tag='judge_answer',
            required_artifact_ids=[dataset_ref.artifact_id, f'rag_answer_{case_id}'],
            tags={'evo_step': 'eval.judge_answer', 'writes_artifact_id': f'judge_result_{case_id}'},
            params={**common, 'rag_answer_ref': f'rag_answer_{case_id}'},
            inputs=[dataset_ref],
            run_depends_on=[rag],
        )
        return rag, judge

    def _bad_cases(self) -> list[str]:
        try:
            report = self.artifacts.get(self.artifacts.latest_ref('eval_report'))
        except KeyError:
            return []
        return [str(row['case_id']) for row in report.get('bad_cases') or [] if row.get('case_id')]

    def _apply_control(self, result, message_id: str) -> tuple[list[OperationResult], bool]:
        if not result.intents:
            return [], False
        intent = result.intents[0]
        if intent.action not in {'retry_operation', 'cancel_operation', 'cancel_running_operation'}:
            return [], False
        for proposal in result.proposals:
            self.graph.start_run(proposal.operation_ref)
            self.graph.end_run(proposal.operation_ref, [], outcome='success')
        if intent.action == 'retry_operation':
            refs = self.graph.retry_with_downstream(
                OperationRunRef(str(intent.params['operation_run_id'])),
                source_message_id=message_id,
            )
            return [OperationResult(str(ref), [], 'pending') for ref in refs], True
        ref = OperationRunRef(str(intent.params['operation_run_id']))
        run = self.graph.get_run(ref)
        if run.status != 'running':
            self.store.append_event(Event('control.noop', self.run_id, {
                'message_id': message_id,
                'operation_run_id': str(ref),
                'action': intent.action,
                'reason': f'operation is {run.status}',
            }))
            return [self.runtime.settle(ref)], True
        self.runtime.request_interrupt(ref)
        return [self.runtime.settle_running(ref)], True

    def _failed_operation_refs(self) -> list[OperationRunRef]:
        return [
            OperationRunRef(blocker.operation_run_id)
            for blocker in self.graph.schedule_state().blockers
            if blocker.reason == 'failed_operation'
        ]

    def _latest_failed_operation_refs(self) -> list[OperationRunRef]:
        latest: dict[str, tuple[int, OperationRunRef]] = {}
        for ref in self.graph.run_refs():
            run = self.graph.get_run(ref)
            current = latest.get(run.spec.operation_id)
            if current is None or run.attempt > current[0]:
                latest[run.spec.operation_id] = (run.attempt, ref)
        return [
            ref for _, ref in latest.values()
            if self.graph.get_run(ref).status == 'ended' and self.graph.get_run(ref).outcome == 'failed'
        ]

    def _remember(self, item: dict[str, Any]) -> None:
        self.completed = [*self.completed, item][-20:]


def _latest_or(artifacts, artifact_id: str) -> str:
    try:
        return str(artifacts.latest_ref(artifact_id))
    except KeyError:
        return f'{artifact_id}@v1'


def _has_latest(artifacts, artifact_id: str) -> bool:
    try:
        artifacts.latest_ref(artifact_id)
        return True
    except KeyError:
        return False


def _ref(value: ArtifactRef | str) -> ArtifactRef:
    return value if isinstance(value, ArtifactRef) else ArtifactRef.parse(str(value))


def _running_operation_id(graph: OperationGraph) -> str:
    refs = graph.run_refs({'running'})
    return str(refs[-1]) if refs else ''


def _has_error(result: OperationResult) -> bool:
    return result.status != 'ended' or any(ref.artifact_id.startswith('error_') for ref in result.output_refs)


def _is_resume_message(message: str) -> bool:
    text = ''.join(message.lower().split()).strip('。.!！')
    return text in {'继续', '恢复', '继续刚才暂停的任务', '恢复刚才暂停的任务', 'resume', 'continue'}


def _normalize_readonly_query(message: str, raw: str, artifacts) -> str:
    artifact_id = _readonly_artifact_id(message)
    if not artifact_id:
        return raw
    try:
        artifacts.latest_ref(artifact_id)
    except KeyError:
        return raw
    task = {
        'next_task': {
            'type': 'execute_task',
            'capability_id': 'read_artifact_query',
            'source_spans': [{'text': message}],
            'semantic_params': {},
        }
    }
    import json
    return json.dumps(task, ensure_ascii=False)


def _forced_intervention_task(message: str, allowed: list[str], capabilities: list[dict]) -> str:
    raw = _forced_artifact_read_query(message, allowed)
    if raw:
        return raw
    if len(allowed) == 1 and _should_force_single_capability(allowed[0], message, capabilities):
        return _forced_read_task(allowed[0], message)
    return ''


def _forced_artifact_read_query(message: str, allowed: list[str]) -> str:
    allowed_case_reads = [
        item for item in allowed
        if item in {'read_coarse_artifact_query', 'read_fine_artifact_query'}
    ]
    if len(allowed_case_reads) == 1 and _has_case_target(message):
        return _forced_read_task(allowed_case_reads[0], message)
    allowed_artifact_reads = [
        item for item in allowed
        if item in {'read_artifact_query', 'read_repair_artifact'}
    ]
    if len(allowed_artifact_reads) != 1 or not _readonly_artifact_id(message):
        return ''
    return _forced_read_task(allowed_artifact_reads[0], message)


def _should_force_single_capability(capability_id: str, message: str, capabilities: list[dict]) -> bool:
    if capability_id in {'respond_to_user', 'read_run_status_query', 'read_artifact_query', 'read_repair_artifact',
                         'read_coarse_artifact_query', 'read_fine_artifact_query', 'read_operation_query'}:
        return False
    if not any(item.get('capability_id') == capability_id for item in capabilities):
        return False
    text = message.lower()
    return any(word in text for word in ('执行', '启动', '继续', '重试', '取消', '构建', '汇总', '整理', '生成',
                                         '评判', '评分', '分类', '细分', '修复', '改', '写成',
                                         'retry', 'start', 'continue'))


def _forced_read_task(capability_id: str, message: str) -> str:
    task = {'next_task': {
        'type': 'execute_task',
        'capability_id': capability_id,
        'source_spans': [{'text': message}],
        'semantic_params': _forced_semantic_params(capability_id, message),
    }}
    return json.dumps(task, ensure_ascii=False)


def _forced_semantic_params(capability_id: str, message: str) -> dict[str, Any]:
    if capability_id == 'patch_dataset_case':
        payload = _json_object_in_text(message)
        return {'payload': payload} if payload else {}
    if capability_id == 'redirect_research':
        researcher = re.search(r'\bresearcher_[A-Za-z0-9_-]+\b', message)
        instruction = re.search(r'(?:改成|调整为|指令(?:是|为)?)([^。.!！]+)', message)
        params = {}
        if researcher:
            params['researcher_id'] = researcher.group(0)
        if instruction:
            params['instructions'] = instruction.group(1).strip()
        return params
    if capability_id == 'rejudge_case':
        score = re.search(r'(?:重新)?评分(?:为|是|:|：)?\s*([0-9]+(?:\.[0-9]+)?)', message)
        score = score or re.search(r'\b([0-9]+(?:\.[0-9]+)?)\b', message)
        rationale = re.search(r'(?:理由|原因)(?:是|为|:|：)?([^。.!！]+)', message)
        params = {'score': float(score.group(1))} if score else {}
        if rationale:
            params['rationale'] = rationale.group(1).strip()
        return params
    return {}


def _json_object_in_text(message: str) -> dict:
    decoder = json.JSONDecoder()
    for match in re.finditer(r'\{', message):
        try:
            value, _ = decoder.raw_decode(message[match.start():])
        except json.JSONDecodeError:
            continue
        if isinstance(value, dict):
            return value
    return {}


def _has_case_target(message: str) -> bool:
    pattern = r'case[_ -]?\d{1,4}|第\s*[0-9一二三四五六七八九十百]+\s*(?:条|个)?'
    return bool(re.search(pattern, message, re.I))


def _readonly_artifact_id(message: str) -> str:
    text = message.lower()
    if not any(word in text for word in ('查看', '读取', '查询', 'read', 'show')):
        return ''
    names = ('eval_report', 'classification_report', 'repair_loop_plan', 'abtest_comparison',
             'candidate_eval_report', 'opencode_run_trace_attempt_1')
    return next((name for name in names if name in text), '')


def _completed(message_id: str, result, outputs: list[OperationResult]) -> dict[str, Any]:
    intent = result.intents[0] if result.intents else None
    return {
        'capability_id': intent.action if intent else result.action,
        'result_summary': {
            'message_id': message_id,
            'status': 'ended' if outputs and all(item.status == 'ended' for item in outputs) else result.action,
            'output_refs': [str(ref) for item in outputs for ref in item.output_refs],
            'operation_refs': [str(item.operation_ref) for item in result.proposals],
            'params': dict(getattr(intent, 'params', {}) or {}),
        },
    }


def result_dict(result: FlowMessageResult) -> dict[str, Any]:
    return {
        'message_id': result.message_id,
        'raw': result.raw,
        'action': result.action,
        'skipped': result.skipped,
        'operation_refs': list(result.operation_refs),
        'results': [asdict(item) for item in result.results],
    }
