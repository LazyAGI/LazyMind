from __future__ import annotations

import os
import time
from dataclasses import asdict, dataclass, field
from enum import Enum
from pathlib import Path
from typing import Any, Callable

from .. import validate_id
from ..artifacts import ArtifactDraft, ArtifactRef
from ..checkpoints import CheckpointManager, CheckpointRef, CheckpointState
from ..checkpoints.models import RESUME_FROM_SNAPSHOT, RESUME_WITH_INTERVENTIONS, ResumeInputPolicy
from ..intent import (CapabilityRegistry, IntentHarness, IntentOperationFactory, IntentRequest, LayeredIntentParser,
                      layered_intent_prompt, parse_next_task, step_capabilities)
from ..operations import OperationGraph, OperationRunRef, OperationSpec
from ..operations.abtest import CompareABTestOperation, CutoverCandidateAlgorithmOperation
from ..operations.analysis import (AssembleClassificationReportOperation, CaseCoarseClassificationOperation,
                                   CaseFineClassificationOperation)
from ..operations.dataset import (AssembleDatasetOperation, BuildCorpusSnapshotOperation, GenerateDatasetCaseOperation,
                                  LoadCorpusOperation, PrepareDatasetCaseOperation)
from ..operations.eval import EvalAggregateOperation, JudgeAnswerOperation, RagAnswerOperation
from ..operations.intent import (IntentParseOperation, PatchArtifactOperation, ReadArtifactQueryOperation,
                                 ReadOperationQueryOperation, ReadRunStatusQueryOperation, RedirectResearchOperation,
                                 RegenerateDatasetCaseOperation, RejudgeCaseOperation, RespondToUserOperation)
from ..operations.repair import (BuildRepairLoopPlanOperation, PrepareCandidateWorkspaceOperation,
                                 RepairLoopAgentOperation, StartCandidateServiceOperation,
                                 StopCandidateServiceOperation, candidate_params, cleanup_candidate_artifacts)
from ..runtime import (DispatchGate, OperationResult, OperationRuntime, ScopedExecutionMode, evo_llm,
                       load_core_model_config)
from ..store import (Event, EvoStore, CompactStoreCallRecorder, StoreOperationRunObserver, StoreProgressReporter,
                     StoreRunLifecycle)

AfterStage = Callable[[str, dict[str, Any]], None]

# Single source of truth for the improvement threshold: the repair loop must aim for
# the same delta that ABTest compare later requires, otherwise repair "wins" get rejected.
TARGET_MEAN_DELTA = 0.02


@dataclass(frozen=True)
class FlowMessageResult:
    message_id: str
    raw: dict[str, Any]
    action: str
    operation_refs: list[str] = field(default_factory=list)
    results: list[OperationResult] = field(default_factory=list)
    skipped: bool = False
    requires_confirmation: bool = False
    confirmation_checkpoint_id: str = ''


class MessageExecutionScope(Enum):
    ROOT = 'root'
    CHECKPOINT = 'checkpoint'


class EvoFlowService:
    def __init__(self, **kwargs: Any):
        self._setup(**kwargs)

    @classmethod
    def resume(cls, **kwargs: Any) -> 'EvoFlowService':
        service = cls.__new__(cls)
        service._setup(recover=True, **kwargs)
        return service

    def _setup(self, *, run_root: Path | str, run_id: str = 'run_1', dataset_id: str, target_chat_url: str,
               candidate_chat_url: str = '', router_admin_url: str = '', case_count: int = 20, max_workers: int = 2,
               model_config: dict[str, Any] | None = None, dispatch_gate: DispatchGate | None = None,
               recover: bool = False) -> None:
        self.run_root = Path(run_root)
        self.run_id, self.dataset_id = run_id, dataset_id
        self.target_chat_url, self.candidate_chat_url = target_chat_url, candidate_chat_url
        self.router_admin_url = router_admin_url
        self.case_count, self.max_workers = int(case_count), int(max_workers)
        self.dispatch_gate = dispatch_gate
        self.model_config = load_core_model_config() | (model_config or {})
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

    def run_full_flow(self, *, include_repair_loop: bool = True, include_abtest: bool = True,
                      start_stage: str = 'dataset', loop_system_params: dict[str, Any] | None = None,
                      repair_plan_params: dict[str, Any] | None = None,
                      after_stage: AfterStage | None = None) -> dict[str, list[OperationResult]]:
        next_steps = {'dataset': ('eval', 'eval.run'), 'eval': ('analysis', 'analysis.run'),
                      'analysis': ('repair', 'repair.run'), 'repair': ('abtest', 'abtest.compare')}

        def notify(stage: str, **detail: Any) -> None:
            if not after_stage: return
            if not detail.get('terminal') and 'next_stage' not in detail:
                detail['next_stage'], detail['next_op'] = next_steps[stage]
            after_stage(stage, detail)

        stages = ('dataset', 'eval', 'analysis', 'repair', 'abtest')
        if start_stage not in stages: raise ValueError(f'unknown evo start_stage: {start_stage}')
        start = stages.index(start_stage)
        out: dict[str, list[OperationResult]] = {}
        self._flow_progress('full_flow', 'running', 'starting evo full flow')
        leftover = self.graph.run_refs({'checkpointed'})
        if leftover:
            self.checkpoints.resume_operation_runs(
                self.run_id, self.graph, leftover, checkpoint_id='operation_checkpointed',
                input_policy=RESUME_WITH_INTERVENTIONS, old_refs_for=self._operation_input_refs,
            )
        if start == 0:
            self.plan_dataset()
            out['dataset_corpus'] = self._dispatch_stage('dataset_corpus', 'msg_flow_dataset_corpus',
                                                         ['corpus_snapshot'])
            self.create_dataset_case_runs()
            out['dataset'] = self._dispatch_stage('dataset', 'msg_flow_dataset', ['eval_dataset'])
        eval_dataset_ref = self.artifacts.latest_ref('eval_dataset')
        if start == 0:
            notify('dataset', eval_dataset_ref=str(eval_dataset_ref))
            eval_dataset_ref = self.artifacts.latest_ref('eval_dataset')
        if start <= 1 and (not _artifact_field_matches(self.artifacts, 'eval_report', 'eval_dataset_ref',
                                                       eval_dataset_ref) or not self._eval_report_ready()):
            self._flow_progress('eval', 'running', 'eval preparing operation graph')
            self.create_eval_runs(eval_dataset_ref)
            out['eval'] = self._dispatch_stage('eval', 'msg_flow_eval', ['eval_report'])
            self._require_eval_report_ready()
        eval_report_ref = self.artifacts.latest_ref('eval_report')
        if start <= 1:
            notify('eval', eval_report_ref=str(eval_report_ref))
            eval_report_ref = self.artifacts.latest_ref('eval_report')
        if start <= 2:
            if not _artifact_field_matches(self.artifacts, 'classification_report', 'eval_report_ref',
                                           eval_report_ref):
                self.create_analysis_runs(eval_report_ref)
                out['analysis'] = self._dispatch_stage('analysis', 'msg_flow_analysis', ['classification_report']) \
                    if self.bad_case_ids else []
            notify('analysis', classification_report_ref=_latest_or(self.artifacts, 'classification_report'))
        self.refresh_context()
        if not self.bad_case_ids:
            if include_repair_loop: self._flow_progress('repair_loop', 'skipped', 'no badcase; repair loop skipped')
            if include_abtest: self._flow_progress('abtest_compare', 'skipped', 'no badcase; abtest skipped')
            self.refresh_context()
            self._flow_progress('full_flow', 'success', 'evo full flow finished')
            return out
        if include_repair_loop and start <= 3:
            if not _artifact_field_matches(self.artifacts, 'repair_loop_plan', 'classification_report_ref',
                                           self.artifacts.latest_ref('classification_report')):
                self.create_repair_plan_run(self.artifacts.latest_ref('classification_report'), repair_plan_params)
                out['repair_plan'] = self._dispatch_stage('repair_plan', 'msg_flow_repair_plan', ['repair_loop_plan'])
            if not _has_latest(self.artifacts, 'candidate_workspace'):
                workspace_ref = self.create_candidate_workspace_run(loop_system_params)
                out['candidate_workspace'] = self._dispatch_stage('candidate_workspace',
                                                                  'msg_flow_candidate_workspace',
                                                                  ['candidate_workspace'])
            else:
                self.recover_candidate_context()
                workspace_ref = self.graph.active_run_for('repair.candidate_workspace')
            if not self._latest_ref_prefix('verified_repair_'):
                loop_ref = self.graph.active_run_for('repair.loop')
                loop_status = self.graph.get_run(loop_ref).status if loop_ref else ''
                if loop_status == 'checkpointed':
                    # Interrupted loop: resume the existing run so it continues from the
                    # last persisted attempt instead of registering a second versioned writer.
                    out['repair_loop'] = self.resume_checkpointed(input_policy=RESUME_WITH_INTERVENTIONS)
                else:
                    if loop_status != 'pending':
                        self.create_repair_loop_run(loop_system_params=self.loop_system_params,
                                                    depends_on=[workspace_ref] if workspace_ref else None,
                                                    inputs=[self.artifacts.latest_ref('candidate_workspace')])
                    out['repair_loop'] = self._dispatch_stage('repair_loop', 'msg_flow_repair_loop', [])
            self._require_repair_candidate()
            notify('repair', verified_repair_ref=str(self._latest_ref_prefix('verified_repair_') or ''))
            eval_dataset_ref = self.artifacts.latest_ref('eval_dataset')
            eval_report_ref = self.artifacts.latest_ref('eval_report')
        if include_abtest and start <= 4:
            self.recover_candidate_context()
            if not self.candidate_chat_url:
                raise RuntimeError('ABTest requires candidate_chat_url or repair loop candidate service params')
            service_ref = None
            try:
                if not _artifact_field_matches(self.artifacts, 'candidate_eval_report', 'eval_dataset_ref',
                                               eval_dataset_ref):
                    service_ref = self.create_candidate_service_run() if include_repair_loop else None
                    out['candidate_service_start'] = self._dispatch_stage(
                        'candidate_service_start', 'msg_flow_candidate_service_start', ['candidate_service']
                    ) if service_ref else []
                    self._create_candidate_eval_run(eval_dataset_ref,
                                                    depends_on=[service_ref] if service_ref else None)
                    out['candidate_eval'] = self._dispatch_stage('candidate_eval', 'msg_flow_candidate_eval',
                                                                 ['candidate_eval_report'], max_workers=1)
                candidate_eval_ref = self.artifacts.latest_ref('candidate_eval_report')
                if not _abtest_comparison_matches(self.artifacts, eval_report_ref, candidate_eval_ref):
                    self.create_abtest_compare_run(eval_report_ref, candidate_eval_ref)
                    out['abtest_compare'] = self._dispatch_stage('abtest_compare', 'msg_flow_abtest_compare',
                                                                 ['abtest_comparison'])
                comparison_ref = self.artifacts.latest_ref('abtest_comparison')
                accepted = (self.artifacts.get(comparison_ref).get('decision') or {}).get('status') == 'accept'
                if accepted and not _has_latest(self.artifacts, 'candidate_algorithm_cutover'):
                    if not self.manual_cutover_confirmed():
                        notify('abtest', abtest_comparison_ref=str(comparison_ref), next_stage='abtest',
                               next_op='abtest.candidate_cutover', checkpoint_kind='manual_cutover',
                               message='ABTest 对比已完成，候选版本满足切流条件，请确认是否注册候选算法并切换 chat 服务。')
                    if not _has_latest(self.artifacts, 'candidate_algorithm_cutover'):
                        _, out['candidate_cutover'], _ = self.execute_candidate_cutover('msg_flow_candidate_cutover')
            finally:
                stop_source = service_ref or self.graph.active_run_for('abtest.candidate_service.start')
                if stop_source and _has_latest(self.artifacts, 'candidate_service') \
                        and not _has_latest(self.artifacts, 'candidate_service_stop'):
                    stop_ref = self.create_candidate_service_stop_run(stop_source)
                    out['candidate_service_stop'] = self._dispatch_stop(stop_ref)
                elif service_ref:
                    cleanup_candidate_artifacts(self.store.run_dir(self.run_id))
                    self._flow_progress('candidate_service_stop', 'success',
                                        'candidate service cleanup finished before startup')
            notify('abtest', abtest_comparison_ref=_latest_or(self.artifacts, 'abtest_comparison'), terminal=True)
        self.refresh_context()
        self._flow_progress('full_flow', 'success', 'evo full flow finished')
        return out

    def plan_dataset(self) -> None:
        self.graph.register_default_graph(self._dataset_specs())

    def create_dataset_case_runs(self) -> None:
        if _has_latest(self.artifacts, 'eval_dataset'): return
        question_types = self._available_question_types()
        snapshot_ref = str(self.artifacts.latest_ref('corpus_snapshot'))
        self._flow_progress('dataset', 'running', 'planning dataset cases', {'question_types': question_types})
        for index in range(1, self.case_count + 1):
            case_id = f'case_{index:04d}'
            self._create_run(
                f'dataset.prepare.{case_id}', 'PrepareDatasetCaseOperation', flow_tag='dataset_gen',
                stage_tag='prepare_case', depends_on=['dataset.build_corpus_snapshot'],
                required_artifact_ids=['corpus_snapshot'],
                params={'source_snapshot_ref': snapshot_ref, 'output_case_id': case_id,
                        'question_type': question_types[(index - 1) % len(question_types)],
                        'difficulty': _dataset_difficulty(index, self.case_count),
                        'user_instruction': f'生成第 {index} 条评测样本；问题必须独立完整，答案必须来自参考内容。'},
            )
            self._create_run(
                f'dataset.generate.{case_id}', 'GenerateDatasetCaseOperation', flow_tag='dataset_gen',
                stage_tag='generate_case', depends_on=[f'dataset.prepare.{case_id}'],
                required_artifact_ids=[f'case_preparation_{case_id}'],
                params={'case_preparation_ref': f'case_preparation_{case_id}@v1'},
                tags={'evo_step': 'dataset_gen.generate_case', 'writes_artifact_id': case_id},
            )
        case_ids = [f'case_{index:04d}' for index in range(1, self.case_count + 1)]
        self._create_run(
            'dataset.assemble', 'AssembleDatasetOperation', flow_tag='dataset_gen', stage_tag='assemble',
            depends_on=[f'dataset.generate.{case_id}' for case_id in case_ids], required_artifact_ids=case_ids,
            params={'dataset_id': 'eval_dataset', 'case_ids': case_ids}, tags={'writes_artifact_id': 'eval_dataset'},
        )

    def create_eval_runs(self, eval_dataset_ref: ArtifactRef | str | None = None) -> None:
        dataset_ref = _ref(eval_dataset_ref or self.artifacts.latest_ref('eval_dataset'))
        self._create_eval_report_runs('eval', dataset_ref, self.target_chat_url, 'eval_report')

    def create_analysis_runs(self, eval_report_ref: ArtifactRef | str | None = None) -> None:
        report_ref = _ref(eval_report_ref or self.artifacts.latest_ref('eval_report'))
        report = self.artifacts.get(report_ref)
        self.bad_case_ids = [str(row['case_id']) for row in report.get('bad_cases') or [] if row.get('case_id')]
        fine_refs = []
        for case_id in self.bad_case_ids:
            self._create_run(
                f'analysis.coarse.{case_id}', 'CaseCoarseClassificationOperation', flow_tag='analysis',
                stage_tag='coarse_classify', required_artifact_ids=['eval_report'],
                tags={'evo_step': 'analysis.coarse_classify',
                      'writes_artifact_id': f'case_coarse_classification_{case_id}'},
                params={'eval_report_ref': str(report_ref), 'case_id': case_id,
                        'output_id': f'case_coarse_classification_{case_id}'},
                inputs=[report_ref],
            )
            fine_refs.append(f'case_fine_classification_{case_id}@v1')
            self._create_run(
                f'analysis.fine.{case_id}', 'CaseFineClassificationOperation', flow_tag='analysis',
                stage_tag='fine_classify', required_artifact_ids=[f'case_coarse_classification_{case_id}'],
                tags={'evo_step': 'analysis.fine_classify',
                      'writes_artifact_id': f'case_fine_classification_{case_id}'},
                params={'coarse_classification_ref': f'case_coarse_classification_{case_id}@v1',
                        'output_id': f'case_fine_classification_{case_id}'},
                run_depends_on=[OperationRunRef(f'analysis.coarse.{case_id}')],
            )
        calibration = self._create_calibration_runs(report_ref, report)
        if fine_refs:
            self._create_run(
                'analysis.classification_report', 'AssembleClassificationReportOperation', flow_tag='analysis',
                stage_tag='classification_report',
                required_artifact_ids=[ref.split('@', 1)[0] for ref in fine_refs],
                tags={'evo_step': 'analysis.classification_report', 'writes_artifact_id': 'classification_report'},
                params={'eval_report_ref': str(report_ref), 'fine_classification_refs': fine_refs,
                        'calibration_classification_refs': [ref for _, ref in calibration],
                        'output_id': 'classification_report'},
                inputs=[report_ref],
                run_depends_on=[OperationRunRef(f'analysis.fine.{case_id}') for case_id in self.bad_case_ids]
                + [OperationRunRef(run_name) for run_name, _ in calibration],
            )

    def _create_calibration_runs(self, report_ref: ArtifactRef, report: dict[str, Any]) -> list[tuple[str, str]]:
        # ANALYSIS-07: classify a small goodcase sample so classifier false positives are measurable.
        failed = {str(row.get('case_id') or '') for row in report.get('execution_failures') or []}
        dataset = self.artifacts.get(_ref(str(report.get('eval_dataset_ref') or '')))
        case_ids = [str(item) for item in dataset.get('case_ids') or []]
        out = []
        for case_id in sorted(set(case_ids) - set(self.bad_case_ids) - failed)[:3]:
            run_name, output_id = f'analysis.calibration.{case_id}', f'case_coarse_calibration_{case_id}'
            self._create_run(
                run_name, 'CaseCoarseClassificationOperation', flow_tag='analysis', stage_tag='coarse_calibration',
                required_artifact_ids=['eval_report'],
                tags={'evo_step': 'analysis.coarse_calibration', 'writes_artifact_id': output_id},
                params={'eval_report_ref': str(report_ref), 'case_id': case_id, 'calibration': True,
                        'output_id': output_id},
                inputs=[report_ref],
            )
            out.append((run_name, f'{output_id}@v1'))
        return out

    def create_repair_plan_run(self, classification_report_ref: ArtifactRef | str | None = None,
                               params: dict[str, Any] | None = None) -> OperationRunRef:
        report_ref = _ref(classification_report_ref or self.artifacts.latest_ref('classification_report'))
        return self._create_run(
            'repair.plan', 'BuildRepairLoopPlanOperation', flow_tag='repair', stage_tag='plan',
            required_artifact_ids=['classification_report'],
            tags={'evo_step': 'repair.plan', 'writes_artifact_id': 'repair_loop_plan'},
            params={'classification_report_ref': str(report_ref), 'output_id': 'repair_loop_plan', **(params or {})},
            inputs=[report_ref],
        )

    def create_repair_loop_run(self, repair_loop_plan_ref: ArtifactRef | str | None = None, *,
                               loop_system_params: dict[str, Any] | None = None,
                               depends_on: list[OperationRunRef] | None = None,
                               inputs: list[ArtifactRef] | None = None) -> OperationRunRef:
        if loop_system_params is not None:
            self.loop_system_params = dict(loop_system_params)
            self.refresh_context()
        plan_ref = _ref(repair_loop_plan_ref or self.artifacts.latest_ref('repair_loop_plan'))
        return self._create_run(
            'repair.loop', 'RepairLoopAgentOperation', flow_tag='repair', stage_tag='repair_loop',
            required_artifact_ids=['repair_loop_plan'],
            tags={'evo_step': 'repair.loop', 'writes_artifact_id': 'repair_loop_agent'},
            params={'repair_loop_plan_ref': str(plan_ref), 'output_id': 'repair_loop_agent',
                    **self.loop_system_params},
            inputs=[plan_ref, *(inputs or [])], run_depends_on=depends_on,
        )

    def create_candidate_workspace_run(self, params: dict[str, Any] | None = None) -> OperationRunRef:
        self.loop_system_params = candidate_params(run_root=self.store.run_dir(self.run_id),
                                                   dataset_name=self.dataset_id, overrides=params)
        self.candidate_chat_url = str(self.loop_system_params['candidate_chat_url'])
        self.refresh_context()
        return self._create_run(
            'repair.candidate_workspace', 'PrepareCandidateWorkspaceOperation', flow_tag='repair',
            stage_tag='candidate_workspace',
            tags={'evo_step': 'repair.candidate_workspace', 'writes_artifact_id': 'candidate_workspace'},
            params={**self.loop_system_params, 'output_id': 'candidate_workspace'},
        )

    def create_candidate_service_run(self) -> OperationRunRef:
        workspace_ref = self.artifacts.latest_ref('candidate_workspace')
        return self._create_run(
            'abtest.candidate_service.start', 'StartCandidateServiceOperation', flow_tag='abtest',
            stage_tag='candidate_service_start', required_artifact_ids=['candidate_workspace'],
            tags={'evo_step': 'abtest.candidate_service.start', 'writes_artifact_id': 'candidate_service'},
            params={**self.loop_system_params, 'candidate_workspace_ref': str(workspace_ref),
                    'output_id': 'candidate_service'},
            inputs=[workspace_ref],
        )

    def create_candidate_service_stop_run(self, service_ref: OperationRunRef) -> OperationRunRef:
        candidate_service_ref = str(self.artifacts.latest_ref('candidate_service'))
        return self._create_run(
            'abtest.candidate_service.stop', 'StopCandidateServiceOperation', flow_tag='abtest',
            stage_tag='candidate_service_stop', required_artifact_ids=['candidate_service'],
            tags={'evo_step': 'abtest.candidate_service.stop', 'writes_artifact_id': 'candidate_service_stop'},
            params={'candidate_service_ref': candidate_service_ref, 'output_id': 'candidate_service_stop'},
            inputs=[ArtifactRef.parse(candidate_service_ref)], run_depends_on=[service_ref],
        )

    def _create_candidate_eval_run(self, eval_dataset_ref: ArtifactRef | str | None = None, *,
                                   depends_on: list[OperationRunRef] | None = None) -> OperationRunRef:
        if not self.candidate_chat_url or self.candidate_chat_url == self.target_chat_url:
            raise ValueError('candidate_chat_url must be present and differ from target_chat_url')
        dataset_ref = _ref(eval_dataset_ref or self.artifacts.latest_ref('eval_dataset'))
        candidate_ref, _ = self._create_eval_report_runs(
            'candidate_eval', dataset_ref, self.candidate_chat_url, 'candidate_eval_report', depends_on=depends_on,
            candidate_service_ref=str(self.artifacts.latest_ref('candidate_service')) if depends_on else '',
            flow_tag='abtest',
        )
        return candidate_ref

    def create_abtest_compare_run(self, baseline_eval_report_ref: ArtifactRef | str,
                                  candidate_eval_report_ref: ArtifactRef | str) -> OperationRunRef:
        baseline_ref, candidate_ref = _ref(baseline_eval_report_ref), _ref(candidate_eval_report_ref)
        return self._create_run(
            'abtest.compare', 'CompareABTestOperation', flow_tag='abtest', stage_tag='compare',
            required_artifact_refs=[baseline_ref, candidate_ref],
            tags={'evo_step': 'abtest.compare', 'writes_artifact_id': 'abtest_comparison'},
            params={'baseline_eval_report_ref': str(baseline_ref), 'candidate_eval_report_ref': str(candidate_ref),
                    'target_mean_delta': TARGET_MEAN_DELTA, 'output_id': 'abtest_comparison'},
            inputs=[baseline_ref, candidate_ref],
        )

    def create_candidate_cutover_run(self) -> OperationRunRef | None:
        comparison_ref = self.artifacts.latest_ref('abtest_comparison')
        if (self.artifacts.get(comparison_ref).get('decision') or {}).get('status') != 'accept':
            self._flow_progress('candidate_cutover', 'skipped', 'abtest rejected candidate; cutover skipped')
            return None
        if not self.router_admin_url: raise RuntimeError('candidate cutover requires router_admin_url')
        workspace_ref = self.artifacts.latest_ref('candidate_workspace')
        # run_root carries the thread id; run_id alone ('run_1') is shared by every thread,
        # so router-facing identifiers must include both to stay globally unique.
        algorithm_id = f'evo_{self.run_root.name}_{self.run_id}_{int(time.time())}'
        return self._create_run(
            'abtest.candidate_cutover', 'CutoverCandidateAlgorithmOperation', flow_tag='abtest',
            stage_tag='candidate_cutover', required_artifact_ids=['abtest_comparison', 'candidate_workspace'],
            tags={'evo_step': 'abtest.candidate_cutover', 'writes_artifact_id': 'candidate_algorithm_cutover'},
            params={'abtest_comparison_ref': str(comparison_ref), 'candidate_workspace_ref': str(workspace_ref),
                    'router_admin_url': self.router_admin_url, 'algorithm_id': algorithm_id,
                    'output_id': 'candidate_algorithm_cutover'},
            inputs=[comparison_ref, workspace_ref],
        )

    def execute_candidate_cutover(self, message_id: str = 'msg_manual_cutover',
                                  ) -> tuple[OperationRunRef | None, list[OperationResult], bool]:
        if _has_latest(self.artifacts, 'candidate_algorithm_cutover'): return None, [], True
        cutover_ref = self.graph.active_run_for('abtest.candidate_cutover') or self.create_candidate_cutover_run()
        if not cutover_ref: return None, [], False
        results = self._dispatch_stage('candidate_cutover', f'{message_id}_candidate_cutover',
                                       ['candidate_algorithm_cutover'])
        return cutover_ref, results, False

    def manual_cutover_confirmed(self) -> bool:
        for event in reversed(self.store.read_events(self.run_id)):
            payload = event.payload or {}
            if event.event_type == 'checkpoint.wait' and payload.get('checkpoint_kind') == 'manual_cutover':
                return False
            if event.event_type != 'checkpoint.continue': continue
            context = payload.get('resume_context') if isinstance(payload.get('resume_context'), dict) else {}
            if context.get('kind') == 'stage' and context.get('source') == 'manual_cutover': return True
        return False

    def recover_candidate_context(self) -> None:
        if self.candidate_chat_url and self.loop_system_params: return
        ref = self.graph.active_run_for('repair.candidate_workspace')
        if not ref: return
        params = dict(self.graph.get_run(ref).spec.params or {})
        if params.get('candidate_chat_url'):
            self.loop_system_params = params
            self.candidate_chat_url = str(params['candidate_chat_url'])
            self.refresh_context()

    def send_message(self, message_id: str, message: str, *, allowed_capabilities: list[str] | None = None,
                     dispatch: bool = True, max_dispatch: int | None = 1) -> FlowMessageResult:
        self.refresh_context()
        allowed = self.registry.capability_ids() if allowed_capabilities is None else list(allowed_capabilities)
        blocked_result = self.blocked_confirmation_result(message_id)
        if blocked_result: return blocked_result
        if dispatch: self.checkpoints.open_dispatch(self.run_id, message_id=message_id)
        return self._plan_message(message_id, message, allowed, dispatch=dispatch, max_dispatch=max_dispatch,
                                  scope=MessageExecutionScope.ROOT)

    def send_checkpoint_message(self, message_id: str, message: str, *,
                                allowed_capabilities: list[str] | None = None, dispatch: bool = True,
                                max_dispatch: int | None = 1) -> FlowMessageResult:
        self.refresh_context()
        allowed = self.registry.capability_ids() if allowed_capabilities is None else list(allowed_capabilities)
        return self._plan_message(message_id, message, allowed, dispatch=dispatch, max_dispatch=max_dispatch,
                                  scope=MessageExecutionScope.CHECKPOINT)

    def _plan_message(self, message_id: str, message: str, allowed: list[str], *, dispatch: bool,
                      max_dispatch: int | None, scope: MessageExecutionScope) -> FlowMessageResult:
        if not allowed:
            return FlowMessageResult(message_id, {'next_task': {'type': 'no_allowed_capabilities'}}, 'reject',
                                     skipped=True)
        checkpoint = self.checkpoints.create_checkpoint(self.run_id, None, message, allowed_capabilities=allowed)
        message_ref = self.artifacts.commit_artifact(ArtifactDraft(
            f'user_message_{message_id}', 'UserMessage', {'message_id': message_id, 'message': message}, 'user',
            role='external_input',
        ))
        capabilities = self.registry.planning_context(self.store, self.run_id, checkpoint)
        parse_ref = self._create_run(
            f'intent.parse.{message_id}', 'IntentParseOperation', category='intent',
            params={'message_id': message_id, 'message': message, 'checkpoint_id': checkpoint.checkpoint_id,
                    'capabilities': capabilities,
                    'prompt': layered_intent_prompt(message, capabilities, completed_tasks=self.completed)},
            inputs=[message_ref],
        )
        parse_result = self._run_checkpoint_parse(parse_ref) if scope is MessageExecutionScope.CHECKPOINT \
            else self._run_single(parse_ref)
        if _has_error(parse_result): raise RuntimeError(f'intent parse failed: {parse_result}')
        parse_artifact_ref = self.artifacts.latest_ref(f'intent_parse_{message_id}')
        raw = self.artifacts.get(parse_artifact_ref)['raw_response']
        result = IntentHarness(self.store, self.run_id, checkpoint, LayeredIntentParser(raw), self.registry,
                               self.factory).handle(
            IntentRequest(message_id, message, checkpoint.checkpoint_id, message_ref, parse_artifact_ref))
        operation_refs = [str(proposal.operation_ref) for proposal in result.proposals]
        if result.action != 'propose_operations':
            self._remember(_completed(message_id, result, []))
            return FlowMessageResult(message_id, {'next_task': parse_next_task(raw)}, result.action, operation_refs)
        confirmation_checkpoint_id = _confirmation_checkpoint_id(result.proposals)
        if confirmation_checkpoint_id:
            self.checkpoints.block_intent_confirmation(
                self.run_id, checkpoint_id=confirmation_checkpoint_id, operation_refs=operation_refs,
                capability_id=result.intents[0].action if result.intents else '', message_id=message_id,
                as_child=scope is MessageExecutionScope.CHECKPOINT,
            )
            self._remember(_completed(message_id, result, []))
            return FlowMessageResult(message_id, {'next_task': parse_next_task(raw)}, result.intents[0].action,
                                     operation_refs, [], True, True, confirmation_checkpoint_id)
        outputs, skipped = self._apply_control(result, message_id, dispatch=dispatch, max_dispatch=max_dispatch)
        if dispatch and not skipped:
            if scope is MessageExecutionScope.CHECKPOINT:
                if result.intents[0].action != 'read_run_status_query':
                    raise RuntimeError('checkpoint message execution requires confirmation')
                outputs = self.run_checkpoint_query([proposal.operation_ref for proposal in result.proposals])
            else:
                outputs = self._run_root_refs([proposal.operation_ref for proposal in result.proposals], message_id,
                                              max_dispatch=max_dispatch)
        self.refresh_context()
        self._remember(_completed(message_id, result, outputs))
        return FlowMessageResult(message_id, {'next_task': parse_next_task(raw)}, result.intents[0].action,
                                 operation_refs, outputs, skipped)

    def dispatch(self, operation_ref: OperationRunRef | None = None, *, message_id: str = 'msg_dispatch',
                 max_dispatch: int | None = None) -> list[OperationResult]:
        self.checkpoints.open_dispatch(self.run_id, message_id=message_id)
        old_limit = self.runtime.max_dispatch
        if max_dispatch is not None: self.runtime.max_dispatch = max_dispatch
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

    def _run_checkpoint_parse(self, operation_ref: OperationRunRef) -> OperationResult:
        return self.runtime.run_scoped([operation_ref], mode=ScopedExecutionMode.PRESERVE_CHECKPOINT)[0]

    def resume_checkpointed(self, *, input_policy: str, dispatch: bool = True) -> list[OperationResult]:
        if input_policy not in {RESUME_FROM_SNAPSHOT, RESUME_WITH_INTERVENTIONS}:
            raise ValueError(f'unsupported checkpoint resume input policy: {input_policy}')
        self.checkpoints.resume_operation_runs(
            self.run_id, self.graph, self.graph.run_refs({'checkpointed'}),
            checkpoint_id='operation_checkpointed', input_policy=input_policy,
            old_refs_for=self._operation_input_refs,
        )
        return self.dispatch(message_id='msg_resume') if dispatch else []

    def apply_stage_interventions(self, checkpoint_id: str,
                                  input_policy: ResumeInputPolicy) -> dict[str, list[dict[str, str]]]:
        if input_policy == RESUME_FROM_SNAPSHOT: return {}
        if input_policy != RESUME_WITH_INTERVENTIONS:
            raise ValueError(f'unsupported checkpoint resume input policy: {input_policy}')
        replacements = self.checkpoints.adopted_replacements_since_checkpoint(self.run_id, checkpoint_id)
        self._rebuild_eval_dataset_if_cases_changed(replacements)
        return self.checkpoints.rebind_stage_resume_inputs(self.run_id, checkpoint_id, self.graph)

    def resume_stage_checkpoint(self, checkpoint: CheckpointState, *, source: str, input_policy: ResumeInputPolicy,
                                thread_id: str = '') -> ArtifactRef:
        rebound = self.apply_stage_interventions(checkpoint.checkpoint_id, input_policy)
        resume_ref = self.checkpoints.record_resume(
            self.run_id, checkpoint.checkpoint_id, input_policy=input_policy, next_operations=[],
            rebound_input_refs=rebound,
            resume_context={'kind': 'stage', 'stage': checkpoint.stage, 'next_stage': checkpoint.next_stage,
                            'source': str(source or ''), 'recovered': False},
        )
        self.checkpoints.open_dispatch(self.run_id, checkpoint_id=checkpoint.checkpoint_id,
                                       message_id=str(source or ''), thread_id=thread_id)
        self.refresh_context()
        return resume_ref

    def _operation_input_refs(self, operation_ref: OperationRunRef) -> list[ArtifactRef]:
        run = self.graph.get_run(operation_ref)
        return [*run.input_refs, *run.spec.required_artifact_refs]

    def _rebuild_eval_dataset_if_cases_changed(self, replacements: dict[str, ArtifactRef]) -> None:
        changed_cases = sorted(ref.artifact_id for ref in replacements.values() if ref.artifact_id.startswith('case_'))
        if not changed_cases or not _has_latest(self.artifacts, 'eval_dataset'): return
        dataset = self.artifacts.get(self.artifacts.latest_ref('eval_dataset'))
        case_ids = [str(item) for item in dataset.get('case_ids') or []]
        if not set(changed_cases) & set(case_ids): return
        assemble_ref = self._create_run(
            f'dataset.assemble.intervention.{int(time.time() * 1000)}', 'AssembleDatasetOperation',
            flow_tag='dataset_gen', stage_tag='assemble', required_artifact_ids=case_ids,
            params={'dataset_id': 'eval_dataset', 'case_ids': case_ids, 'source_message_id': 'stage_intervention'},
            tags={'writes_artifact_id': 'eval_dataset'},
            inputs=[self.artifacts.latest_ref(case_id) for case_id in case_ids],
        )
        self.runtime.run_scoped([assemble_ref], mode=ScopedExecutionMode.PRESERVE_CHECKPOINT)

    def confirm_checkpoint(self, checkpoint_id: str, message_id: str) -> FlowMessageResult:
        checkpoint = self.checkpoints.active_checkpoint(self.run_id)
        if (checkpoint is None or checkpoint.checkpoint_id != checkpoint_id
                or checkpoint.dispatch_block_reason != 'confirmation_required'
                or not checkpoint.is_intent_confirmation):
            raise RuntimeError(f'intent confirmation checkpoint is not active: {checkpoint_id}')
        refs = self.checkpoints.resume_operations(self.run_id, CheckpointRef(checkpoint_id))
        if not refs: raise RuntimeError('intent confirmation checkpoint has no operations')
        results = self.run_confirmed_checkpoint_operations(checkpoint_id, refs)
        # A confirmed intent operation runs against the exact inputs the user previewed,
        # so its own resume is always from snapshot; interventions apply at the parent stage gate.
        self.checkpoints.record_resume(
            self.run_id, checkpoint_id, input_policy=RESUME_FROM_SNAPSHOT, next_operations=refs,
            rebound_input_refs={}, resume_context={'kind': 'intent_confirmation', 'message_id': message_id},
        )
        if not self.checkpoints.restore_parent_dispatch(self.run_id, message_id=message_id):
            self.checkpoints.open_dispatch(self.run_id, checkpoint_id=checkpoint_id, message_id=message_id)
        return FlowMessageResult(message_id,
                                 {'next_task': {'type': 'intent_confirmation', 'checkpoint_id': checkpoint_id}},
                                 'confirm_intent_operation', [str(ref) for ref in refs], results)

    def confirmation_succeeded(self, result: FlowMessageResult) -> bool:
        if not result.results: return False
        for output in result.results:
            run = self.graph.get_run(OperationRunRef(output.operation_run_id))
            if run.status != 'ended' or run.outcome != 'success': return False
        return True

    def blocked_confirmation_result(self, message_id: str) -> FlowMessageResult | None:
        blocked = self._active_intent_confirmation()
        if not blocked: return None
        return FlowMessageResult(message_id, {'next_task': {'type': 'intent_confirmation_required'}},
                                 'intent_confirmation_required',
                                 list(blocked.next_operations or blocked.blocked_operations), skipped=True,
                                 requires_confirmation=True, confirmation_checkpoint_id=blocked.checkpoint_id)

    def refresh_context(self) -> None:
        self.bad_case_ids = self._bad_cases()
        self.registry = self._registry()
        self.factory = IntentOperationFactory(store=self.store, operation_graph=self.graph,
                                              capability_registry=self.registry, checkpoint_manager=self.checkpoints)

    @property
    def artifacts(self):
        return self.store.artifact_graph(self.run_id)

    def _create_run(self, operation_id: str, operation_type: str, *, inputs=None, run_depends_on=None, **spec: Any):
        if (spec.get('tags') or {}).get('writes_artifact_id'): spec['write_policy'] = 'versioned'
        active = self.graph.active_run_for(operation_id)
        if active is not None:
            run = self.graph.get_run(active)
            status = run.status
            if status == 'checkpointed': self.graph.reset_run(active)
            if status in {'pending', 'running', 'checkpointed'}: return active
            if status == 'ended' and run.outcome == 'success': return active
        return self.graph.create_run(
            OperationSpec(operation_id, operation_type, **spec), inputs=list(inputs or []), depends_on=run_depends_on
        )

    def _dispatch_stage(self, stage: str, message_id: str, required_artifact_ids: list[str], *,
                        max_workers: int | None = None) -> list[OperationResult]:
        self._flow_progress(stage, 'running', f'{stage} started')
        old_workers = self.runtime.max_workers
        if max_workers is not None: self.runtime.max_workers = max(1, int(max_workers))
        results: list[OperationResult] = []

        def pending() -> tuple[list[str], list[str]]:
            failed = [str(ref) for ref in self._latest_failed_operation_refs()]
            missing = [aid for aid in required_artifact_ids if not _has_latest(self.artifacts, aid)]
            return failed, missing

        try:
            while True:
                batch = self.dispatch(message_id=message_id)
                results.extend(batch)
                failed, missing = pending()
                if failed or not missing: break
                if not batch or not self.graph.schedule_state().ready: break
                self.checkpoints.open_dispatch(self.run_id, message_id=message_id)
        finally:
            self.runtime.max_workers = old_workers
        failed, missing = pending()
        if failed or missing:
            detail = {'failed_operations': failed, 'missing_artifacts': missing}
            self._flow_progress(stage, 'failed', f'{stage} failed', detail)
            raise RuntimeError(f'{stage} failed: {detail}')
        self.refresh_context()
        self._flow_progress(stage, 'success', f'{stage} finished', {'result_count': len(results)})
        return results

    def _dispatch_stop(self, stop_ref: OperationRunRef) -> list[OperationResult]:
        self._flow_progress('candidate_service_stop', 'running', 'candidate_service_stop started')
        self.checkpoints.open_dispatch(self.run_id, message_id='msg_flow_candidate_service_stop')
        result = self.runtime.run(stop_ref)
        if _has_error(result) or not _has_latest(self.artifacts, 'candidate_service_stop'):
            detail = {'operation_ref': result.operation_run_id, 'output_refs': [str(ref) for ref in result.output_refs]}
            self._flow_progress('candidate_service_stop', 'failed', 'candidate_service_stop failed', detail)
            raise RuntimeError(f'candidate_service_stop failed: {detail}')
        self._flow_progress('candidate_service_stop', 'success', 'candidate_service_stop finished')
        return [result]

    def _flow_progress(self, stage: str, status: str, message: str, detail: dict[str, Any] | None = None) -> None:
        self.store.append_event(Event('evo_flow.progress', self.run_id, {
            'stage': stage, 'status': status, 'message': message, 'detail': detail or {}, 'timestamp': time.time(),
        }))

    def _eval_report_checks(self) -> dict[str, Any]:
        if not _has_latest(self.artifacts, 'eval_report'): return {}
        report = self.artifacts.get(self.artifacts.latest_ref('eval_report'))
        return report.get('checks') or {}

    def _eval_report_ready(self) -> bool:
        checks = self._eval_report_checks()
        return checks.get('ready') is not False

    def _require_eval_report_ready(self) -> None:
        """Infra failures (e.g. chat 503) yield no quality data; fail the eval stage instead of 'no badcase'."""
        checks = self._eval_report_checks()
        if checks.get('ready') is not False: return
        detail = {'errors': list(checks.get('errors') or [])[:5]}
        self._flow_progress('eval', 'failed', 'eval report failed quality gate', detail)
        raise RuntimeError(f'eval report failed quality gate: {detail}')

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
        self._flow_progress('repair_loop', 'success', 'verified repair ready for final ABTest',
                            {'verified_ref': str(verified_ref)})

    def _latest_ref_prefix(self, prefix: str) -> ArtifactRef | None:
        refs = []
        for manifest in self.artifacts.manifest_dir.glob(f'{prefix}*.json'):
            try:
                refs.append(self.artifacts.latest_ref(manifest.stem))
            except KeyError:
                pass
        return sorted(refs, key=lambda ref: ref.artifact_id)[-1] if refs else None

    def _runtime(self) -> OperationRuntime:
        executors: dict[str, Any] = {cls.__name__: cls() for cls in (
            LoadCorpusOperation, BuildCorpusSnapshotOperation, AssembleDatasetOperation, EvalAggregateOperation,
            CaseCoarseClassificationOperation, AssembleClassificationReportOperation, BuildRepairLoopPlanOperation,
            PrepareCandidateWorkspaceOperation, StartCandidateServiceOperation, StopCandidateServiceOperation,
            CompareABTestOperation, CutoverCandidateAlgorithmOperation, ReadArtifactQueryOperation,
            PatchArtifactOperation, RegenerateDatasetCaseOperation, RejudgeCaseOperation, RedirectResearchOperation,
            RespondToUserOperation,
        )}
        executors.update({
            'PrepareDatasetCaseOperation': PrepareDatasetCaseOperation(self.llm),
            'GenerateDatasetCaseOperation': GenerateDatasetCaseOperation(self.llm),
            'RagAnswerOperation': RagAnswerOperation(self.model_config),
            'JudgeAnswerOperation': JudgeAnswerOperation(self.llm),
            'CaseFineClassificationOperation': CaseFineClassificationOperation(self.llm),
            'RepairLoopAgentOperation': RepairLoopAgentOperation(self.llm, self.model_config),
            'IntentParseOperation': IntentParseOperation(self.llm),
            'ReadOperationQueryOperation': ReadOperationQueryOperation(self.store),
            'ReadRunStatusQueryOperation': ReadRunStatusQueryOperation(self.store),
        })
        return OperationRuntime(
            run_id=self.run_id, operation_graph=self.graph, artifact_graph=self.artifacts, executors=executors,
            draft_root=self.store.run_dir(self.run_id) / 'tmp' / 'drafts',
            progress_reporter=StoreProgressReporter(self.store, self.run_id),
            call_recorder_factory=lambda op_id: CompactStoreCallRecorder(self.store, self.run_id, op_id),
            run_lifecycle=StoreRunLifecycle(self.store, self.run_id), dispatch_gate=self.dispatch_gate,
            max_dispatch=500, max_workers=self.max_workers,
        )

    def _registry(self) -> CapabilityRegistry:
        baseline_ref = _latest_or(self.artifacts, 'eval_report')
        if _has_latest(self.artifacts, 'baseline_eval_report'):
            baseline_ref = str(self.artifacts.latest_ref('baseline_eval_report'))
        candidate_ref = 'candidate_eval_report@v1'
        if _has_latest(self.artifacts, 'candidate_eval_report'):
            candidate_ref = str(self.artifacts.latest_ref('candidate_eval_report'))
        running = self.graph.run_refs({'running'})
        return CapabilityRegistry(step_capabilities(
            run_id=self.run_id, dataset_id=self.dataset_id,
            eval_dataset_ref=_latest_or(self.artifacts, 'eval_dataset'),
            eval_report_ref=_latest_or(self.artifacts, 'eval_report'),
            classification_report_ref=_latest_or(self.artifacts, 'classification_report'),
            abtest_baseline_report_ref=baseline_ref, abtest_candidate_report_ref=candidate_ref,
            abtest_comparison_ref=_latest_or(self.artifacts, 'abtest_comparison'),
            candidate_workspace_ref=_latest_or(self.artifacts, 'candidate_workspace'),
            bad_case_ids=self.bad_case_ids, target_chat_url=self.target_chat_url,
            router_admin_url=self.router_admin_url, running_operation_id=str(running[-1]) if running else '',
            loop_system_params=self.loop_system_params,
        ))

    def _dataset_specs(self) -> list[OperationSpec]:
        return [
            OperationSpec(
                'dataset.load_corpus', 'LoadCorpusOperation', flow_tag='dataset_gen', stage_tag='load_corpus',
                params={'sources': [{'type': 'kb', 'source_id': self.dataset_id, 'dataset_id': self.dataset_id,
                                     'max_docs': int(os.getenv('EVO_FLOW_MAX_DOCS', '8')),
                                     'doc_page_size': int(os.getenv('EVO_FLOW_DOC_PAGE_SIZE', '1000'))}]},
            ),
            OperationSpec(
                'dataset.build_corpus_snapshot', 'BuildCorpusSnapshotOperation', flow_tag='dataset_gen',
                stage_tag='build_corpus_snapshot', depends_on=['dataset.load_corpus'],
                required_artifact_ids=['corpus_load_report'],
                params={'source_report_ref': 'corpus_load_report@v1',
                        'segment_page_size': int(os.getenv('EVO_FLOW_SEGMENT_PAGE_SIZE', '1000')),
                        'min_segment_chars': int(os.getenv('EVO_FLOW_MIN_SEGMENT_CHARS', '80')),
                        'segment_groups': ['block', 'line']},
            ),
        ]

    def _available_question_types(self) -> list[str]:
        snapshot = self.artifacts.get(self.artifacts.latest_ref('corpus_snapshot'))
        stats, doc_counts = snapshot.get('stats', {}), self._snapshot_doc_unit_counts(snapshot)
        counts = stats.get('unit_type_counts', {})
        if int(counts.get('paragraph') or 0) < 1:
            raise RuntimeError('corpus_snapshot has no paragraph source units for dataset generation')
        types = ['single_hop']
        if any(count >= 2 for count in doc_counts.values()): types.append('single_doc_multi_hop')
        if int(stats.get('document_with_units_count') or 0) >= 2: types.append('multi_doc_multi_hop')
        if int(counts.get('table') or 0) + int(counts.get('list') or 0) + int(counts.get('mixed') or 0):
            types.append('table_list')
        if int(counts.get('formula') or 0) + int(counts.get('mixed') or 0): types.append('formula')
        return types

    def _snapshot_doc_unit_counts(self, snapshot: dict[str, Any]) -> dict[str, int]:
        counts: dict[str, int] = {}
        for ref in snapshot.get('source_unit_page_refs') or []:
            for unit in self.artifacts.get(ArtifactRef.parse(str(ref))).get('source_units', []):
                if str(unit.get('unit_type') or 'paragraph') == 'paragraph':
                    doc_id = str(unit.get('doc_id') or '')
                    counts[doc_id] = counts.get(doc_id, 0) + 1
        return counts

    def _create_eval_report_runs(self, prefix: str, dataset_ref: ArtifactRef, chat_url: str, report_id: str, *,
                                 depends_on: list[OperationRunRef] | None = None, candidate_service_ref: str = '',
                                 flow_tag: str = 'eval',
                                 ) -> tuple[OperationRunRef, dict[str, tuple[OperationRunRef, OperationRunRef]]]:
        case_ids = list(self.artifacts.get(dataset_ref)['case_ids'])
        judge_result_ids = {case_id: _eval_artifact_id(prefix, 'judge_result', case_id) for case_id in case_ids}
        case_runs = {
            case_id: self._create_eval_case_runs(prefix, dataset_ref, case_id, chat_url, depends_on=depends_on,
                                                 candidate_service_ref=candidate_service_ref, flow_tag=flow_tag)
            for case_id in case_ids
        }
        aggregate = self._create_run(
            f'{prefix}.aggregate', 'EvalAggregateOperation', flow_tag=flow_tag, stage_tag='aggregate',
            required_artifact_ids=[dataset_ref.artifact_id, *[judge_result_ids[case_id] for case_id in case_ids]],
            tags={'evo_step': 'eval.aggregate', 'writes_artifact_id': report_id},
            params={'eval_dataset_ref': str(dataset_ref), 'report_id': report_id,
                    'judge_result_ids': judge_result_ids},
            inputs=[dataset_ref], run_depends_on=[case_runs[case_id][1] for case_id in case_ids],
        )
        return aggregate, case_runs

    def _create_eval_case_runs(self, prefix: str, dataset_ref: ArtifactRef, case_id: str, chat_url: str, *,
                               depends_on: list[OperationRunRef] | None, candidate_service_ref: str = '',
                               flow_tag: str = 'eval') -> tuple[OperationRunRef, OperationRunRef]:
        common = {'eval_dataset_ref': str(dataset_ref), 'case_id': case_id}
        rag_output_id = _eval_artifact_id(prefix, 'rag_answer', case_id)
        judge_output_id = _eval_artifact_id(prefix, 'judge_result', case_id)
        params = {**common, 'target_chat_url': chat_url, 'dataset_name': self.dataset_id, 'require_trace': True}
        if candidate_service_ref: params['candidate_service_ref'] = candidate_service_ref
        rag = self._create_run(
            f'{prefix}.rag.{case_id}', 'RagAnswerOperation', flow_tag=flow_tag, stage_tag='rag_answer',
            required_artifact_ids=[dataset_ref.artifact_id, *(['candidate_service'] if candidate_service_ref else [])],
            tags={'evo_step': 'eval.rag_answer', 'writes_artifact_id': rag_output_id},
            params={**params, 'output_id': rag_output_id}, inputs=[dataset_ref], run_depends_on=depends_on,
        )
        judge = self._create_run(
            f'{prefix}.judge.{case_id}', 'JudgeAnswerOperation', flow_tag=flow_tag, stage_tag='judge_answer',
            required_artifact_ids=[dataset_ref.artifact_id, rag_output_id],
            tags={'evo_step': 'eval.judge_answer', 'writes_artifact_id': judge_output_id},
            params={**common, 'rag_answer_ref': rag_output_id, 'output_id': judge_output_id},
            inputs=[dataset_ref], run_depends_on=[rag],
        )
        return rag, judge

    def _bad_cases(self) -> list[str]:
        try:
            report = self.artifacts.get(self.artifacts.latest_ref('eval_report'))
        except KeyError:
            return []
        return [str(row['case_id']) for row in report.get('bad_cases') or [] if row.get('case_id')]

    def _apply_control(self, result, message_id: str, *, dispatch: bool,
                       max_dispatch: int | None) -> tuple[list[OperationResult], bool]:
        if not result.intents: return [], False
        intent = result.intents[0]
        if intent.action not in {'retry_operation', 'cancel_operation', 'cancel_running_operation',
                                 'resume_checkpointed'}:
            return [], False
        for proposal in result.proposals:
            self.graph.start_run(proposal.operation_ref)
            self.graph.end_run(proposal.operation_ref, [], outcome='success')
        if intent.action == 'resume_checkpointed':
            if not self.graph.run_refs({'checkpointed'}): return [], True
            policy = str(intent.params.get('input_policy') or RESUME_WITH_INTERVENTIONS)
            if policy not in {RESUME_FROM_SNAPSHOT, RESUME_WITH_INTERVENTIONS}:
                raise ValueError(f'unsupported checkpoint resume input policy: {policy}')
            return self.resume_checkpointed(input_policy=policy, dispatch=False), True
        if intent.action == 'retry_operation':
            refs = self.graph.retry_with_downstream(OperationRunRef(str(intent.params['operation_run_id'])),
                                                    source_message_id=message_id)
            if not dispatch or not refs:
                return [OperationResult(str(ref), [], 'pending') for ref in refs], True
            return self._run_root_refs(refs, message_id, max_dispatch=1), True
        ref = OperationRunRef(str(intent.params['operation_run_id']))
        run = self.graph.get_run(ref)
        if run.status != 'running':
            self.store.append_event(Event('control.noop', self.run_id, {
                'message_id': message_id, 'operation_run_id': str(ref), 'action': intent.action,
                'reason': f'operation is {run.status}',
            }))
            return [self.runtime.settle(ref)], True
        self.runtime.request_interrupt(ref)
        return [self.runtime.settle_running(ref)], True

    def _run_root_refs(self, refs: list[OperationRunRef], message_id: str, *,
                       max_dispatch: int | None = 1) -> list[OperationResult]:
        old_limit, old_workers = self.runtime.max_dispatch, self.runtime.max_workers
        self.runtime.max_dispatch = max_dispatch
        if max_dispatch == 1: self.runtime.max_workers = 1
        try:
            outputs = []
            for ref in refs:
                self.checkpoints.open_dispatch(self.run_id, message_id=message_id)
                outputs.append(self.runtime.run(ref))
            return outputs
        finally:
            self.runtime.max_dispatch, self.runtime.max_workers = old_limit, old_workers

    def run_checkpoint_query(self, refs: list[OperationRunRef]) -> list[OperationResult]:
        for ref in refs:
            if self.graph.get_run(ref).spec.operation_type != 'ReadRunStatusQueryOperation':
                raise RuntimeError(f'checkpoint query cannot run mutable operation: {ref}')
        return self.runtime.run_scoped(refs, mode=ScopedExecutionMode.PRESERVE_CHECKPOINT)

    def run_confirmed_checkpoint_operations(self, checkpoint_id: str,
                                            refs: list[OperationRunRef]) -> list[OperationResult]:
        checkpoint = self.checkpoints.active_checkpoint(self.run_id)
        if (checkpoint is None or checkpoint.checkpoint_id != checkpoint_id
                or checkpoint.dispatch_block_reason != 'confirmation_required'
                or not checkpoint.is_intent_confirmation):
            raise RuntimeError(f'intent confirmation checkpoint is not active: {checkpoint_id}')
        allowed = set(checkpoint.next_operations or checkpoint.blocked_operations)
        requested = {str(ref) for ref in refs}
        if not requested or not requested <= allowed:
            raise RuntimeError(f'checkpoint confirmation refs do not match active checkpoint: {checkpoint_id}')
        return self.runtime.run_scoped(refs, mode=ScopedExecutionMode.PRESERVE_CHECKPOINT)

    def _latest_failed_operation_refs(self) -> list[OperationRunRef]:
        latest: dict[str, tuple[int, OperationRunRef]] = {}
        for ref in self.graph.run_refs():
            run = self.graph.get_run(ref)
            current = latest.get(run.spec.operation_id)
            if current is None or run.attempt > current[0]: latest[run.spec.operation_id] = (run.attempt, ref)
        return [ref for _, ref in latest.values()
                if self.graph.get_run(ref).status == 'ended' and self.graph.get_run(ref).outcome == 'failed']

    def _remember(self, item: dict[str, Any]) -> None:
        self.completed = [*self.completed, item][-20:]

    def _active_intent_confirmation(self) -> CheckpointState | None:
        checkpoint = self.checkpoints.active_checkpoint(self.run_id)
        if (checkpoint is not None and checkpoint.dispatch_block_reason == 'confirmation_required'
                and checkpoint.is_intent_confirmation):
            return checkpoint
        return None


def _artifact_field_matches(artifacts, artifact_id: str, field: str, ref: ArtifactRef | str) -> bool:
    """Whether the latest artifact was produced from the given input ref (stage already up to date)."""
    try:
        payload = artifacts.get(artifacts.latest_ref(artifact_id))
    except KeyError:
        return False
    return str(payload.get(field) or '') == str(ref)


def _abtest_comparison_matches(artifacts, baseline_ref: ArtifactRef | str, candidate_ref: ArtifactRef | str) -> bool:
    try:
        payload = artifacts.get(artifacts.latest_ref('abtest_comparison'))
    except KeyError:
        return False
    return (str(payload.get('baseline_eval_report_ref') or '') == str(baseline_ref)
            and str(payload.get('candidate_eval_report_ref') or '') == str(candidate_ref))


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


def _eval_artifact_id(prefix: str, kind: str, case_id: str) -> str:
    validate_id(kind, 'eval_artifact_kind')
    validate_id(case_id, 'case_id')
    prefix = validate_id(str(prefix or 'eval'), 'eval_prefix')
    return f'{kind}_{case_id}' if prefix == 'eval' else f'{prefix}_{kind}_{case_id}'


def _dataset_difficulty(index: int, total: int) -> str:
    return 'medium' if total <= 1 else ('easy', 'medium', 'hard')[(index - 1) % 3]


def _has_error(result: OperationResult) -> bool:
    return result.status != 'ended' or any(ref.artifact_id.startswith('error_') for ref in result.output_refs)


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
        'message_id': result.message_id, 'raw': result.raw, 'action': result.action, 'skipped': result.skipped,
        'requires_confirmation': result.requires_confirmation,
        'confirmation_checkpoint_id': result.confirmation_checkpoint_id,
        'operation_refs': list(result.operation_refs), 'results': [asdict(item) for item in result.results],
    }


def _confirmation_checkpoint_id(proposals) -> str:
    for proposal in proposals:
        if proposal.requires_confirmation: return proposal.confirmation_checkpoint_id
    return ''
