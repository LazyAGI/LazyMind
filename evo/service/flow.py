from __future__ import annotations

import os
import time
from contextlib import contextmanager
from dataclasses import asdict, dataclass, field
from enum import Enum
from pathlib import Path
from typing import Any, Callable

from .. import validate_id
from ..artifacts import ArtifactDraft, ArtifactRef
from ..checkpoints import CheckpointManager, CheckpointRef, CheckpointState
from ..checkpoints.models import RESUME_FROM_SNAPSHOT, RESUME_WITH_INTERVENTIONS, ResumeInputPolicy
from ..internal_ids import is_synthetic_operation, latest_failed_stage_from_events, stage_group
from ..intent import (CapabilityRegistry, IntentConversationContextBuilder, IntentHarness, IntentOperationFactory,
                      IntentRequest, LayeredIntentParser, layered_intent_prompt, parse_next_task, remaining_message,
                      step_capabilities)
from ..operations import FlowGraphDefinition, OperationGraph, OperationRunRef, OperationSpec, downstream_rebuild_roots
from ..operations.abtest import CompareABTestOperation, CutoverCandidateAlgorithmOperation
from ..operations.analysis import (AssembleClassificationReportOperation, CaseCoarseClassificationOperation,
                                   CaseFineClassificationOperation)
from ..operations.dataset import (AssembleDatasetOperation, BuildCorpusSnapshotOperation, GenerateDatasetCaseOperation,
                                  LoadCorpusOperation, PrepareDatasetCaseOperation)
from ..operations.eval import EvalAggregateOperation, JudgeAnswerOperation, RagAnswerOperation
from ..operations.intent import (ExplainRunFailureQueryOperation, IntentParseOperation, PatchArtifactOperation,
                                 PatchClassificationOperation, PatchJudgeResultOperation, ReadArtifactQueryOperation,
                                 ReadOperationQueryOperation, ReadRunStatusQueryOperation, RedirectResearchOperation,
                                 RegenerateDatasetCaseOperation, RejudgeCaseOperation, RenderIntentAnswerOperation,
                                 RespondToUserOperation)
from ..operations.repair import (BuildRepairLoopPlanOperation, PrepareCandidateWorkspaceOperation,
                                 RepairLoopAgentOperation, StartCandidateServiceOperation,
                                 StopCandidateServiceOperation, candidate_params, cleanup_candidate_artifacts)
from ..runtime import (DispatchGate, OperationResult, OperationRuntime, ScopedExecutionMode, evo_llm,
                       load_core_model_config)
from ..store import (Event, EvoStore, CompactStoreCallRecorder, StoreOperationRunObserver, StoreProgressReporter,
                     StoreRunLifecycle)

# Single source of truth for the improvement threshold: the repair loop must aim for
# the same delta that ABTest compare later requires, otherwise repair "wins" get rejected.
TARGET_MEAN_DELTA = 0.02
MESSAGE_LOOP_LIMIT = 8


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
               thread_id: str = '',
               candidate_chat_url: str = '', router_admin_url: str = '', case_count: int = 20, max_workers: int = 2,
               model_config: dict[str, Any] | None = None, user_context: dict[str, str] | None = None,
               dispatch_gate: DispatchGate | None = None,
               recover: bool = False) -> None:
        self.run_root = Path(run_root)
        self.run_id, self.thread_id, self.dataset_id = run_id, thread_id, dataset_id
        self.target_chat_url, self.candidate_chat_url = target_chat_url, candidate_chat_url
        self.router_admin_url = router_admin_url
        self.user_context = user_context or {}
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
        self.flow_graph = FlowGraphDefinition()
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
                      repair_plan_params: dict[str, Any] | None = None) -> dict[str, list[OperationResult]]:
        stages = ('dataset', 'eval', 'analysis', 'repair', 'abtest')
        if start_stage not in stages: raise ValueError(f'unknown evo start_stage: {start_stage}')
        start = stages.index(start_stage)
        out: dict[str, list[OperationResult]] = {}
        self._flow_progress('full_flow', 'running', 'starting evo full flow')
        leftover = _business_operation_refs(self.graph.run_refs({'checkpointed'}))
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
        if start <= 1 and (not self._stage_fresh('eval') or not self._eval_report_ready()):
            self._flow_progress('eval', 'running', 'eval preparing operation graph')
            self.create_eval_runs(eval_dataset_ref)
            out['eval'] = self._dispatch_stage('eval', 'msg_flow_eval', ['eval_report'])
            self._require_eval_report_ready()
        eval_report_ref = self.artifacts.latest_ref('eval_report')
        if start <= 2:
            if not self._stage_fresh('analysis'):
                self.create_analysis_runs(eval_report_ref)
                out['analysis'] = self._dispatch_stage('analysis', 'msg_flow_analysis', ['classification_report']) \
                    if self.bad_case_ids else []
        self.refresh_context()
        if not self.bad_case_ids:
            if include_repair_loop: self._flow_progress('repair_loop', 'skipped', 'no badcase; repair loop skipped')
            if include_abtest: self._flow_progress('abtest_compare', 'skipped', 'no badcase; abtest skipped')
            self.refresh_context()
            self._flow_progress('full_flow', 'success', 'evo full flow finished')
            return out
        if include_repair_loop and start <= 3:
            if not self._stage_fresh('repair'):
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
            eval_dataset_ref = self.artifacts.latest_ref('eval_dataset')
            eval_report_ref = self.artifacts.latest_ref('eval_report')
        if include_abtest and start <= 4:
            self.recover_candidate_context()
            if not self.candidate_chat_url:
                raise RuntimeError('ABTest requires candidate_chat_url or repair loop candidate service params')
            service_ref = None
            try:
                if not self.flow_graph.is_candidate_eval_fresh(self.artifacts, eval_dataset_ref):
                    service_ref = self.create_candidate_service_run() if include_repair_loop else None
                    out['candidate_service_start'] = self._dispatch_stage(
                        'candidate_service_start', 'msg_flow_candidate_service_start', ['candidate_service']
                    ) if service_ref else []
                    self._create_candidate_eval_run(eval_dataset_ref,
                                                    depends_on=[service_ref] if service_ref else None)
                    out['candidate_eval'] = self._dispatch_stage('candidate_eval', 'msg_flow_candidate_eval',
                                                                 ['candidate_eval_report'], max_workers=1)
                candidate_eval_ref = self.artifacts.latest_ref('candidate_eval_report')
                if not self._stage_fresh('abtest'):
                    self.create_abtest_compare_run(eval_report_ref, candidate_eval_ref)
                    out['abtest_compare'] = self._dispatch_stage('abtest_compare', 'msg_flow_abtest_compare',
                                                                 ['abtest_comparison'])
                comparison_ref = self.artifacts.latest_ref('abtest_comparison')
                accepted = (self.artifacts.get(comparison_ref).get('decision') or {}).get('status') == 'accept'
                if (accepted and self.router_admin_url
                        and not _has_latest(self.artifacts, 'candidate_algorithm_cutover')):
                    _, out['candidate_cutover'], _ = self.execute_candidate_cutover('msg_flow_candidate_cutover')
            finally:
                stop_source = service_ref or self._latest_lifecycle_run('candidate_service_start')
                if stop_source and _has_latest(self.artifacts, 'candidate_service') \
                        and not self.flow_graph.is_candidate_service_stopped(self.artifacts):
                    stop_ref = self.create_candidate_service_stop_run(stop_source)
                    out['candidate_service_stop'] = self._dispatch_stop(stop_ref)
                elif service_ref:
                    cleanup_candidate_artifacts(self.store.run_dir(self.run_id))
                    self._flow_progress('candidate_service_stop', 'success',
                                        'candidate service cleanup finished before startup')
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
        existing = self._unfinished_lifecycle_run('candidate_service_start')
        if existing: return existing
        workspace_ref = self.artifacts.latest_ref('candidate_workspace')
        operation_id = self._next_lifecycle_operation_id('abtest.candidate_service.start')
        return self._create_run(
            operation_id, 'StartCandidateServiceOperation', flow_tag='abtest',
            stage_tag='candidate_service_start', required_artifact_ids=['candidate_workspace'],
            tags={'evo_step': 'abtest.candidate_service.start', 'writes_artifact_id': 'candidate_service'},
            params={**self.loop_system_params, 'candidate_workspace_ref': str(workspace_ref),
                    'output_id': 'candidate_service'},
            inputs=[workspace_ref],
        )

    def create_candidate_service_stop_run(self, service_ref: OperationRunRef) -> OperationRunRef:
        candidate_service_ref = str(self.artifacts.latest_ref('candidate_service'))
        existing = self._unfinished_lifecycle_run(
            'candidate_service_stop',
            lambda run: str(run.spec.params.get('candidate_service_ref') or '') == candidate_service_ref,
        )
        if existing: return existing
        operation_id = self._next_lifecycle_operation_id('abtest.candidate_service.stop')
        return self._create_run(
            operation_id, 'StopCandidateServiceOperation', flow_tag='abtest',
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

    def recover_candidate_context(self) -> None:
        if self.candidate_chat_url and self.loop_system_params: return
        ref = self.graph.active_run_for('repair.candidate_workspace')
        if not ref: return
        params = dict(self.graph.get_run(ref).spec.params or {})
        if params.get('candidate_chat_url'):
            self.loop_system_params = params
            self.candidate_chat_url = str(params['candidate_chat_url'])
            self.refresh_context()

    def _next_lifecycle_operation_id(self, base: str) -> str:
        active = self.graph.active_run_for(base)
        if active is None or self.graph.get_run(active).status != 'ended':
            return base
        return f'{base}.{int(time.time() * 1000)}'

    def _unfinished_lifecycle_run(self, stage_tag: str, predicate: Callable[[Any], bool] | None = None
                                  ) -> OperationRunRef | None:
        for ref in reversed(self.graph.run_refs({'pending', 'running', 'checkpointed'})):
            run = self.graph.get_run(ref)
            if (run.spec.flow_tag == 'abtest' and run.spec.stage_tag == stage_tag
                    and (predicate is None or predicate(run))):
                return ref
        return None

    def _latest_lifecycle_run(self, stage_tag: str) -> OperationRunRef | None:
        for ref in reversed(self.graph.run_refs()):
            run = self.graph.get_run(ref)
            if not run.superseded_by and run.spec.flow_tag == 'abtest' and run.spec.stage_tag == stage_tag:
                return ref
        return None

    def send_message(self, message_id: str, message: str, *, allowed_capabilities: list[str] | None = None,
                     dispatch: bool = True, max_dispatch: int | None = 1,
                     pending_reader: Callable[[], str] | None = None) -> FlowMessageResult:
        self.refresh_context()
        allowed = self.registry.capability_ids() if allowed_capabilities is None else list(allowed_capabilities)
        blocked_result = self.blocked_confirmation_result(message_id)
        if blocked_result: return blocked_result
        if dispatch: self.checkpoints.open_dispatch(self.run_id, message_id=message_id)
        return self._plan_message(message_id, message, allowed, dispatch=dispatch, max_dispatch=max_dispatch,
                                  scope=MessageExecutionScope.ROOT, pending_reader=pending_reader)

    def send_checkpoint_message(self, message_id: str, message: str, *,
                                allowed_capabilities: list[str] | None = None, dispatch: bool = True,
                                max_dispatch: int | None = 1,
                                pending_reader: Callable[[], str] | None = None) -> FlowMessageResult:
        self.refresh_context()
        allowed = self.registry.capability_ids() if allowed_capabilities is None else list(allowed_capabilities)
        return self._plan_message(message_id, message, allowed, dispatch=dispatch, max_dispatch=max_dispatch,
                                  scope=MessageExecutionScope.CHECKPOINT, pending_reader=pending_reader)

    def _plan_message(self, message_id: str, message: str, allowed: list[str], *, dispatch: bool,
                      max_dispatch: int | None, scope: MessageExecutionScope,
                      pending_reader: Callable[[], str] | None = None) -> FlowMessageResult:
        current, steps, outputs, operation_refs = message, [], [], []
        action, skipped = 'no_operations', False
        for index in range(MESSAGE_LOOP_LIMIT):
            step_id = message_id if index == 0 else f'{message_id}_step{index + 1}'
            step = self._plan_message_once(step_id, current, allowed, dispatch=dispatch, max_dispatch=max_dispatch,
                                           scope=scope)
            outputs.extend(step.results)
            operation_refs.extend(step.operation_refs)
            steps.append(step.raw)
            action, skipped = step.action, step.skipped
            self._message_loop_event(message_id, index + 1, step, current)
            if step.requires_confirmation or step.action in {'ask_clarification', 'reject'}:
                return FlowMessageResult(message_id, {'steps': steps}, action, operation_refs, outputs, skipped,
                                         step.requires_confirmation, step.confirmation_checkpoint_id)
            residual = str(step.raw.get('remaining_message') or '')
            pending = pending_reader() if pending_reader else ''
            current = '\n'.join(part for part in (residual, pending) if part.strip()).strip()
            if not current:
                break
        raw = {'steps': steps, 'remaining_message': current}
        outputs.extend(self._render_message_reply(message_id, message, action, outputs, steps))
        return FlowMessageResult(message_id, raw, action, operation_refs, outputs, skipped)

    def _plan_message_once(self, message_id: str, message: str, allowed: list[str], *, dispatch: bool,
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
        intent_context = IntentConversationContextBuilder(self.store, self.graph).build(self.run_id)
        parse_ref = self._create_run(
            f'intent.parse.{message_id}', 'IntentParseOperation', category='intent',
            params={'message_id': message_id, 'message': message, 'checkpoint_id': checkpoint.checkpoint_id,
                    'capabilities': capabilities,
                    'prompt': layered_intent_prompt(message, capabilities, completed_tasks=self.completed,
                                                    conversation_context=intent_context)},
            inputs=[message_ref],
        )
        # Intent parse is synchronous message-path work; it must not be blocked by
        # ThreadDispatchGate (e.g. cancelled/paused thread meta).
        parse_result = self._run_checkpoint_parse(parse_ref)
        if _has_error(parse_result): raise RuntimeError(f'intent parse failed: {parse_result}')
        parse_artifact_ref = self.artifacts.latest_ref(f'intent_parse_{message_id}')
        raw = self.artifacts.get(parse_artifact_ref)['raw_response']
        result = IntentHarness(self.store, self.run_id, checkpoint, LayeredIntentParser(raw), self.registry,
                               self.factory).handle(
            IntentRequest(message_id, message, checkpoint.checkpoint_id, message_ref, parse_artifact_ref))
        result_raw = {'next_task': parse_next_task(raw), 'remaining_message': remaining_message(raw, message),
                      'intents': [asdict(intent) for intent in result.intents],
                      'reasons': list(result.reasons), 'issues': [asdict(issue) for issue in result.issues]}
        operation_refs = [str(proposal.operation_ref) for proposal in result.proposals]
        if result.action != 'propose_operations':
            self._remember(_completed(message_id, result, []))
            return FlowMessageResult(message_id, result_raw, result.action, operation_refs)
        confirmation_checkpoint_id = _confirmation_checkpoint_id(result.proposals)
        if confirmation_checkpoint_id:
            self.checkpoints.block_intent_confirmation(
                self.run_id, checkpoint_id=confirmation_checkpoint_id, operation_refs=operation_refs,
                capability_id=result.intents[0].action if result.intents else '', message_id=message_id,
                as_child=scope is MessageExecutionScope.CHECKPOINT,
            )
            self._remember(_completed(message_id, result, []))
            return FlowMessageResult(message_id, result_raw, result.intents[0].action,
                                     operation_refs, [], True, True, confirmation_checkpoint_id)
        outputs, skipped = self._apply_control(result, message_id, dispatch=dispatch, max_dispatch=max_dispatch)
        if dispatch and not skipped:
            if result.intents[0].action in {'read_run_status_query', 'explain_run_failure_query',
                                            'read_operation_query'}:
                outputs = self.run_checkpoint_query([proposal.operation_ref for proposal in result.proposals])
            elif scope is MessageExecutionScope.CHECKPOINT:
                raise RuntimeError('checkpoint message execution requires confirmation')
            else:
                outputs = self._run_root_refs([proposal.operation_ref for proposal in result.proposals], message_id,
                                              max_dispatch=max_dispatch)
                outputs.extend(self._rebuild_downstream_for_outputs(
                    outputs, message_id, dispatch=dispatch, max_dispatch=max_dispatch))
        self.refresh_context()
        self._remember(_completed(message_id, result, outputs))
        return FlowMessageResult(message_id, result_raw, result.intents[0].action, operation_refs, outputs, skipped)

    def dispatch(self, operation_ref: OperationRunRef | None = None, *, message_id: str = 'msg_dispatch',
                 max_dispatch: int | None = None) -> list[OperationResult]:
        self.checkpoints.open_dispatch(self.run_id, message_id=message_id)
        with self._runtime_limits(max_dispatch=max_dispatch):
            return self.runtime.dispatch(operation_ref)

    def recover_stale_running(self) -> list[OperationRunRef]:
        recovered = []
        for ref in list(self.graph.run_refs({'running'})):
            if is_synthetic_operation(str(ref)):
                continue
            self.graph.reset_run(ref)
            recovered.append(ref)
        if recovered:
            self.store.append_event(Event('thread_control.stale_running_recovered', self.run_id, {
                'operation_refs': [str(ref) for ref in recovered],
            }))
        return recovered

    def emit_ready_stage_progress(self, message_id: str) -> None:
        stages = {
            stage_group(self.graph.get_run(ref).spec.flow_tag)
            for ref in [*self.graph.run_refs({'running'}), *self.graph.ready_runs()]
        }
        for stage in sorted(item for item in stages if item):
            self._flow_progress(stage, 'running', f'{stage} stage running', {'message_id': message_id})

    def _run_checkpoint_parse(self, operation_ref: OperationRunRef) -> OperationResult:
        return self.runtime.run_scoped([operation_ref], mode=ScopedExecutionMode.PRESERVE_CHECKPOINT)[0]

    def resume_checkpointed(self, *, input_policy: str, dispatch: bool = True) -> list[OperationResult]:
        if input_policy not in {RESUME_FROM_SNAPSHOT, RESUME_WITH_INTERVENTIONS}:
            raise ValueError(f'unsupported checkpoint resume input policy: {input_policy}')
        checkpointed = _business_operation_refs(self.graph.run_refs({'checkpointed'}))
        if not checkpointed: return []
        self.checkpoints.resume_operation_runs(
            self.run_id, self.graph, checkpointed,
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

    def _rebuild_downstream_for_outputs(self, outputs: list[OperationResult], message_id: str, *, dispatch: bool,
                                        max_dispatch: int | None) -> list[OperationResult]:
        old_refs, replacements = [], {}
        for output in outputs:
            run = self.graph.get_run(OperationRunRef(output.operation_run_id))
            if not run.spec.operation_type.startswith('Patch') or not output.output_refs:
                continue
            for new_ref in output.output_refs:
                old_ref = next((ref for ref in run.input_refs if ref.artifact_id == new_ref.artifact_id), None)
                if old_ref is None: continue
                old_refs.append(old_ref)
                replacements[old_ref.artifact_id] = new_ref
        if not old_refs: return []
        self._rebuild_eval_dataset_if_cases_changed(replacements)
        retry_refs = self.graph.retry_with_downstream_many(
            list(downstream_rebuild_roots(self.graph, self.artifacts, old_refs)),
            source_message_id=message_id,
        )
        impact = self.artifacts.impact(old_refs)
        self.store.append_event(Event('graph_mutation.downstream_rebuild', self.run_id, {
            'message_id': message_id, 'changed_refs': [str(ref) for ref in old_refs],
            'replacement_refs': {key: str(value) for key, value in replacements.items()},
            'impacted_refs': [str(ref) for ref in impact.impacted],
            'operation_refs': [str(ref) for ref in retry_refs],
        }))
        if not dispatch or not retry_refs:
            return [OperationResult(str(ref), [], 'pending') for ref in retry_refs]
        return self._dispatch_until_settled(retry_refs, message_id, max_dispatch=max_dispatch or 1)

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

    @contextmanager
    def _runtime_limits(self, *, max_dispatch: int | None = None, max_workers: int | None = None):
        old_limit, old_workers = self.runtime.max_dispatch, self.runtime.max_workers
        if max_dispatch is not None: self.runtime.max_dispatch = max_dispatch
        if max_workers is not None: self.runtime.max_workers = max(1, int(max_workers))
        try:
            yield
        finally:
            self.runtime.max_dispatch, self.runtime.max_workers = old_limit, old_workers

    def _stage_fresh(self, stage: str) -> bool:
        return self.flow_graph.is_stage_fresh(self.artifacts, stage)

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
        results: list[OperationResult] = []

        def pending() -> tuple[list[str], list[str]]:
            failed = [str(ref) for ref in self._latest_failed_operation_refs()]
            missing = [aid for aid in required_artifact_ids if not _has_latest(self.artifacts, aid)]
            return failed, missing

        with self._runtime_limits(max_workers=max_workers):
            while True:
                batch = self.dispatch(message_id=message_id)
                results.extend(batch)
                failed, missing = pending()
                if failed or not missing: break
                if not batch or not self.graph.schedule_state().ready: break
                self.checkpoints.open_dispatch(self.run_id, message_id=message_id)
        failed, missing = pending()
        if failed or missing:
            checkpointed = [str(ref) for ref in self.graph.run_refs({'checkpointed'})]
            if checkpointed and not failed:
                detail = {'checkpointed_operations': checkpointed, 'missing_artifacts': missing}
                self._flow_progress(stage, 'checkpointed', f'{stage} paused', detail)
                raise RuntimeError(f'{stage} paused: {detail}')
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
            PatchArtifactOperation, PatchClassificationOperation, PatchJudgeResultOperation,
            RegenerateDatasetCaseOperation, RejudgeCaseOperation, RedirectResearchOperation, RespondToUserOperation,
        )}
        executors.update({
            'PrepareDatasetCaseOperation': PrepareDatasetCaseOperation(self.llm),
            'GenerateDatasetCaseOperation': GenerateDatasetCaseOperation(self.llm),
            'RagAnswerOperation': RagAnswerOperation(self.model_config, self.user_context),
            'JudgeAnswerOperation': JudgeAnswerOperation(self.llm),
            'CaseFineClassificationOperation': CaseFineClassificationOperation(self.llm),
            'RepairLoopAgentOperation': RepairLoopAgentOperation(self.llm, self.model_config),
            'IntentParseOperation': IntentParseOperation(self.llm),
            'RenderIntentAnswerOperation': RenderIntentAnswerOperation(self.llm),
            'ExplainRunFailureQueryOperation': ExplainRunFailureQueryOperation(self.store),
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
            thread_id=self.thread_id, current_stage=self._current_stage(),
            loop_system_params=self.loop_system_params,
        ))

    def _current_stage(self) -> str:
        active = self.checkpoints.active_checkpoint(self.run_id)
        if active and active.stage: return active.stage
        refs = self.graph.run_refs({'running', 'checkpointed', 'failed'})
        if refs:
            run = self.graph.get_run(refs[-1])
            return run.spec.stage_tag or run.spec.flow_tag
        return ''

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
        if intent.action not in {'pause_thread', 'cancel_thread', 'retry_thread',
                                 'retry_stage', 'retry_operation', 'cancel_operation', 'cancel_running_operation',
                                 'resume_checkpointed', 'ensure_stage', 'restart_from_stage',
                                 'retry_dataset_cases'}:
            return [], False
        for proposal in result.proposals:
            self.graph.start_run(proposal.operation_ref)
            self.graph.end_run(proposal.operation_ref, [], outcome='success')
        if intent.action == 'resume_checkpointed':
            if not self.graph.run_refs({'checkpointed'}):
                return self._continue_from_latest_artifact(message_id, dispatch=dispatch,
                                                           max_dispatch=max_dispatch), True
            policy = str(intent.params.get('input_policy') or RESUME_WITH_INTERVENTIONS)
            if policy not in {RESUME_FROM_SNAPSHOT, RESUME_WITH_INTERVENTIONS}:
                raise ValueError(f'unsupported checkpoint resume input policy: {policy}')
            return self.resume_checkpointed(input_policy=policy, dispatch=False), True
        if intent.action == 'pause_thread':
            return self._pause_thread_control(message_id), True
        if intent.action == 'cancel_thread':
            return self._cancel_thread_control(message_id), True
        if intent.action == 'retry_thread':
            return self._retry_thread_control(message_id, dispatch=dispatch, max_dispatch=max_dispatch), True
        if intent.action in {'retry_stage', 'restart_from_stage', 'ensure_stage'}:
            stage = str(intent.params.get('stage') or intent.target.get('stage') or '')
            return self._stage_control(intent.action, stage, message_id, dispatch=dispatch,
                                       max_dispatch=max_dispatch), True
        if intent.action == 'retry_dataset_cases':
            return self._retry_dataset_cases_control(intent.params.get('target_case_ids') or [], message_id,
                                                     dispatch=dispatch, max_dispatch=max_dispatch), True
        if intent.action == 'retry_operation':
            refs = self.graph.retry_with_downstream(OperationRunRef(str(intent.params['operation_run_id'])),
                                                    source_message_id=message_id)
            if not dispatch or not refs:
                return [OperationResult(str(ref), [], 'pending') for ref in refs], True
            return self._dispatch_until_settled(refs, message_id, max_dispatch=1), True
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

    def _stage_control(self, action: str, stage: str, message_id: str, *, dispatch: bool,
                       max_dispatch: int | None = 1) -> list[OperationResult]:
        stage = stage_group(stage)
        if not stage: return []
        self._cancel_checkpoint_for_control(message_id, action=action, stage=stage)
        if action == 'ensure_stage':
            plan = self.flow_graph.plan_ensure_stage(self.artifacts, stage)
            self.store.append_event(Event('thread_control.ensure_stage', self.run_id, {
                'message_id': message_id, 'thread_id': self.thread_id, 'stage': stage,
                'blocked': plan.blocked, 'reason': plan.blocked_reason,
                'missing_artifact_ids': list(plan.missing_artifact_ids),
                'stale_stages': list(plan.stale_stages),
            }))
            if plan.blocked or not dispatch or not plan.stale_stages: return []
            stage = plan.stale_stages[0]
        if stage == 'eval':
            return self._retry_eval_stage(message_id, dispatch=dispatch, max_dispatch=max_dispatch)
        refs = self._stage_retry_refs(stage, restart=action == 'restart_from_stage')
        retry_refs = self.graph.retry_with_downstream_many(refs, source_message_id=message_id) if refs else []
        self.store.append_event(Event(f'thread_control.{action}', self.run_id, {
            'message_id': message_id, 'thread_id': self.thread_id, 'stage': stage,
            'operation_refs': [str(ref) for ref in retry_refs],
        }))
        if retry_refs:
            self._flow_progress(stage, 'running', f'{stage} stage running',
                                {'message_id': message_id, 'operation_count': len(retry_refs)})
        if retry_refs:
            return self._dispatch_until_settled(retry_refs, message_id, max_dispatch=max_dispatch or 1) if dispatch \
                else [OperationResult(str(ref), [], self.graph.get_run(ref).status) for ref in retry_refs]
        if action == 'ensure_stage' and dispatch:
            return [item for results in self.run_full_flow(start_stage=stage,
                                                           include_repair_loop=stage in {'repair', 'abtest'},
                                                           include_abtest=stage == 'abtest').values()
                    for item in results]
        return []

    def _pause_thread_control(self, message_id: str) -> list[OperationResult]:
        for ref in self.graph.run_refs({'running'}):
            self.runtime.request_interrupt(ref)
        outputs = [self.runtime.settle_running(ref) for ref in self.graph.run_refs({'running'})]
        StoreRunLifecycle(self.store, self.run_id).mark_paused(thread_id=self.thread_id, reason='user_intervention')
        self.store.append_event(Event('thread_control.paused', self.run_id, {
            'message_id': message_id, 'thread_id': self.thread_id, 'operation_count': len(outputs),
        }))
        return outputs

    def _cancel_thread_control(self, message_id: str) -> list[OperationResult]:
        for ref in self.graph.run_refs({'running'}):
            self.runtime.request_interrupt(ref)
        outputs = [self.runtime.settle_running(ref) for ref in self.graph.run_refs({'running'})]
        self.checkpoints.cancel_active(self.run_id, thread_id=self.thread_id)
        StoreRunLifecycle(self.store, self.run_id).mark_cancelled(thread_id=self.thread_id)
        self.store.append_event(Event('thread_control.cancelled', self.run_id, {
            'message_id': message_id, 'thread_id': self.thread_id, 'operation_count': len(outputs),
        }))
        return outputs

    def _retry_thread_control(self, message_id: str, *, dispatch: bool,
                              max_dispatch: int | None) -> list[OperationResult]:
        all_checkpointed = self.graph.run_refs({'checkpointed'})
        checkpointed = _business_operation_refs(all_checkpointed)
        if all_checkpointed and not checkpointed:
            self.checkpoints.cancel_active(self.run_id, thread_id=self.thread_id)
        if checkpointed:
            return self.resume_checkpointed(input_policy=RESUME_WITH_INTERVENTIONS, dispatch=False)
        stage = self._latest_failed_stage()
        if stage:
            return self._stage_control('retry_stage', stage, message_id, dispatch=dispatch, max_dispatch=max_dispatch)
        refs = self.graph.retry_with_downstream(
            self._latest_failed_operation_refs()[0], source_message_id=message_id,
        ) if self._latest_failed_operation_refs() else []
        if not dispatch or not refs: return [OperationResult(str(ref), [], 'pending') for ref in refs]
        return self._dispatch_until_settled(refs, message_id, max_dispatch=max_dispatch or 1)

    def _retry_dataset_cases_control(self, raw_case_ids: list, message_id: str, *, dispatch: bool,
                                     max_dispatch: int | None) -> list[OperationResult]:
        case_ids = [_normalize_case_id(item) for item in raw_case_ids]
        self._cancel_checkpoint_for_control(message_id, action='retry_dataset_cases', stage='dataset')
        roots_by_case = {case_id: self._dataset_case_retry_roots(case_id) for case_id in case_ids}
        roots = [ref for refs in roots_by_case.values() for ref in refs]
        missing = [case_id for case_id, refs in roots_by_case.items() if not refs]
        if case_ids and not roots:
            raise RuntimeError(f'no dataset case operations found for: {case_ids}')
        retry_refs = self.graph.retry_with_downstream_many(roots, source_message_id=message_id) if roots else []
        self.store.append_event(Event('thread_control.retry_dataset_cases', self.run_id, {
            'message_id': message_id, 'thread_id': self.thread_id, 'case_ids': case_ids,
            'missing_case_ids': missing,
            'operation_refs': [str(ref) for ref in retry_refs],
        }))
        if retry_refs:
            self._flow_progress('dataset', 'running', 'dataset case retry running',
                                {'message_id': message_id, 'case_ids': case_ids,
                                 'operation_count': len(retry_refs)})
        if not dispatch or not retry_refs:
            return [OperationResult(str(ref), [], self.graph.get_run(ref).status) for ref in retry_refs]
        return self._dispatch_until_settled(retry_refs, message_id, max_dispatch=max_dispatch or 1)

    def _dataset_case_retry_roots(self, case_id: str) -> list[OperationRunRef]:
        for operation_id in (f'dataset.prepare.{case_id}', f'dataset.generate.{case_id}'):
            ref = OperationRunRef(operation_id)
            try:
                run = self.graph.get_run(ref)
            except KeyError:
                continue
            if not run.superseded_by:
                return [ref]
        return []

    def _continue_from_latest_artifact(self, message_id: str, *, dispatch: bool,
                                       max_dispatch: int | None) -> list[OperationResult]:
        for stage in self.flow_graph.stages:
            plan = self.flow_graph.plan_ensure_stage(self.artifacts, stage)
            if plan.blocked:
                break
            if plan.stale_stages:
                return self._stage_control('ensure_stage', stage, message_id, dispatch=dispatch,
                                           max_dispatch=max_dispatch)
        self.store.append_event(Event('thread_control.resume_noop', self.run_id, {
            'message_id': message_id, 'thread_id': self.thread_id, 'reason': 'all_stages_fresh_or_blocked',
        }))
        return []

    def _stage_retry_refs(self, stage: str, *, restart: bool) -> list[OperationRunRef]:
        statuses = None if restart else {'checkpointed'}
        refs = self.graph.run_refs(statuses) if statuses else self.graph.run_refs()
        failed = [] if restart else self._latest_failed_operation_refs(stage=stage)
        return failed or [ref for ref in refs if stage_group(self.graph.get_run(ref).spec.flow_tag) == stage
                          and not is_synthetic_operation(str(ref)) and not self.graph.get_run(ref).superseded_by]

    def _cancel_checkpoint_for_control(self, message_id: str, *, action: str, stage: str = '') -> None:
        checkpoint_ids = self.checkpoints.active_checkpoint_ids(self.run_id)
        if not checkpoint_ids:
            return
        self.checkpoints.cancel_active(self.run_id, thread_id=self.thread_id, message_id=message_id,
                                       action=action, stage=stage, reason='user_control_override')

    def _retry_eval_stage(self, message_id: str, *, dispatch: bool,
                          max_dispatch: int | None) -> list[OperationResult]:
        self._close_failed_eval_retry_batches()
        pending_aggregate = self._pending_eval_retry_aggregate()
        if pending_aggregate is not None:
            refs = self._eval_retry_batch_refs(str(pending_aggregate).rsplit('.', 1)[0])
            self._reset_running_eval_retry_refs(refs)
            self._flow_progress('eval', 'running', 'eval stage running',
                                {'message_id': message_id, 'operation_count': len(refs)})
            if not dispatch:
                return [OperationResult(str(ref), [], self.graph.get_run(ref).status) for ref in refs]
            return self._dispatch_until_settled(refs, message_id, max_dispatch=max_dispatch or 1)
        dataset_ref = self.artifacts.latest_ref('eval_dataset')
        prefix = f'eval_retry_{int(time.time() * 1000)}'
        aggregate_ref, case_runs = self._create_eval_report_runs(prefix, dataset_ref, self.target_chat_url,
                                                                 'eval_report')
        refs = [ref for pair in case_runs.values() for ref in pair] + [aggregate_ref]
        self.store.append_event(Event('thread_control.retry_stage', self.run_id, {
            'message_id': message_id, 'thread_id': self.thread_id, 'stage': 'eval',
            'operation_refs': [str(ref) for ref in refs],
        }))
        self._flow_progress('eval', 'running', 'eval stage running',
                            {'message_id': message_id, 'operation_count': len(refs)})
        if not dispatch:
            return [OperationResult(str(ref), [], 'pending') for ref in refs]
        return self._dispatch_until_settled(refs, message_id, max_dispatch=max_dispatch or 1)

    def _reset_running_eval_retry_refs(self, refs: list[OperationRunRef]) -> None:
        for ref in refs:
            run = self.graph.get_run(ref)
            if run.status == 'running':
                self.graph.reset_run(ref)

    def _pending_eval_retry_aggregate(self) -> OperationRunRef | None:
        for ref in reversed(self.graph.run_refs({'pending', 'running', 'checkpointed'})):
            run = self.graph.get_run(ref)
            if not str(ref).startswith('eval_retry_') or not str(ref).endswith('.aggregate'):
                continue
            if self._eval_retry_batch_failed(str(ref).rsplit('.', 1)[0]):
                self._close_obsolete_eval_retry_batch(str(ref).rsplit('.', 1)[0])
                continue
            if run.spec.tags.get('writes_artifact_id') == 'eval_report':
                if run.status == 'checkpointed':
                    self.graph.reset_run(ref)
                return ref
        return None

    def _close_obsolete_pending_run(self, ref: OperationRunRef, reason: str) -> None:
        run = self.graph.get_run(ref)
        if run.status == 'ended': return
        if run.status == 'checkpointed':
            self.graph.reset_run(ref)
        if self.graph.get_run(ref).status == 'pending':
            self.graph.start_run(ref)
        if self.graph.get_run(ref).status == 'running':
            self.graph.end_run(ref, [], outcome='superseded')
            self.store.append_event(Event('operation.obsolete_pending_closed', self.run_id, {
                'operation_run_id': str(ref), 'reason': reason,
            }))

    def _eval_retry_batch_failed(self, prefix: str) -> bool:
        return any(str(ref).startswith(f'{prefix}.') and self.graph.get_run(ref).status == 'ended'
                   and self.graph.get_run(ref).outcome == 'failed' for ref in self.graph.run_refs())

    def _close_failed_eval_retry_batches(self) -> None:
        prefixes = {
            str(ref).rsplit('.', 1)[0] for ref in self.graph.run_refs()
            if str(ref).startswith('eval_retry_') and self.graph.get_run(ref).status == 'ended'
            and self.graph.get_run(ref).outcome == 'failed'
        }
        for prefix in sorted(prefixes):
            self._close_obsolete_eval_retry_batch(prefix)

    def _close_obsolete_eval_retry_batch(self, prefix: str) -> None:
        for ref in [ref for ref in self.graph.run_refs() if str(ref).startswith(f'{prefix}.')]:
            run = self.graph.get_run(ref)
            if run.status == 'ended' and run.outcome == 'failed':
                self.graph.mark_obsolete_failure(ref, reason='eval retry batch failed')
            elif run.status != 'ended':
                self._close_obsolete_pending_run(ref, 'eval retry batch failed')

    def _eval_retry_batch_refs(self, prefix: str) -> list[OperationRunRef]:
        return [ref for ref in self.graph.run_refs({'pending', 'running', 'checkpointed'})
                if str(ref).startswith(f'{prefix}.')]

    def _dispatch_until_settled(self, refs: list[OperationRunRef], message_id: str, *,
                                max_dispatch: int | None = 1) -> list[OperationResult]:
        results: list[OperationResult] = []
        with self._runtime_limits(max_dispatch=max_dispatch, max_workers=1 if max_dispatch == 1 else None):
            while True:
                self.checkpoints.open_dispatch(self.run_id, message_id=message_id)
                batch = self.runtime.dispatch()
                results.extend(batch)
                if all(self.graph.get_run(ref).status == 'ended' for ref in refs): return results
                if any(self.graph.get_run(ref).status == 'checkpointed' for ref in refs): return results
                if any(self.graph.get_run(ref).status == 'ended' and self.graph.get_run(ref).outcome == 'failed'
                       for ref in refs):
                    return results
                if not batch and not self.graph.schedule_state().ready: return results

    def _run_root_refs(self, refs: list[OperationRunRef], message_id: str, *,
                       max_dispatch: int | None = 1) -> list[OperationResult]:
        with self._runtime_limits(max_dispatch=max_dispatch, max_workers=1 if max_dispatch == 1 else None):
            outputs = []
            for ref in refs:
                self.checkpoints.open_dispatch(self.run_id, message_id=message_id)
                outputs.append(self.runtime.run(ref))
            return outputs

    def run_checkpoint_query(self, refs: list[OperationRunRef]) -> list[OperationResult]:
        for ref in refs:
            if self.graph.get_run(ref).spec.operation_type not in {
                'ReadRunStatusQueryOperation', 'ExplainRunFailureQueryOperation', 'ReadOperationQueryOperation',
            }:
                raise RuntimeError(f'checkpoint query cannot run mutable operation: {ref}')
        return self.runtime.run_scoped(refs, mode=ScopedExecutionMode.PRESERVE_CHECKPOINT)

    def _render_query_reply(self, message_id: str, message: str, action: str,
                            query_outputs: list[OperationResult]) -> list[OperationResult]:
        input_refs = self._intent_answer_refs(query_outputs)
        if not input_refs:
            return []
        render_ref = self._create_run(
            f'intent.reply.{message_id}', 'RenderIntentAnswerOperation', category='intent',
            stage_tag='render_intent_answer',
            params={'query_intent_id': f'reply_{message_id}', 'source_message_id': message_id,
                    'message': message, 'query_action': action},
            inputs=input_refs,
        )
        return self.runtime.run_scoped([render_ref], mode=ScopedExecutionMode.PRESERVE_CHECKPOINT)

    def _render_message_reply(self, message_id: str, message: str, action: str, outputs: list[OperationResult],
                              steps: list[dict]) -> list[OperationResult]:
        input_refs = self._intent_answer_refs(outputs)
        render_ref = self._create_run(
            f'intent.reply.{message_id}', 'RenderIntentAnswerOperation', category='intent',
            stage_tag='render_intent_answer',
            params={'query_intent_id': f'reply_{message_id}', 'source_message_id': message_id,
                    'message': message, 'query_action': action, 'task_results': steps},
            inputs=input_refs,
        )
        return self.runtime.run_scoped([render_ref], mode=ScopedExecutionMode.PRESERVE_CHECKPOINT)

    def _intent_answer_refs(self, outputs: list[OperationResult]) -> list[ArtifactRef]:
        refs = [ref for output in outputs for ref in output.output_refs]
        return [ref for ref in refs if _is_intent_answer_payload(self.artifacts.get(ref))]

    def _message_loop_event(self, message_id: str, index: int, result: FlowMessageResult, message: str) -> None:
        self.store.append_event(Event('intent.message_loop.step', self.run_id, {
            'message_id': message_id, 'index': index, 'action': result.action,
            'operation_refs': result.operation_refs, 'remaining_message': result.raw.get('remaining_message', ''),
            'message_preview': message[:200], 'timestamp': time.time(),
        }))

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

    def _latest_failed_stage(self) -> str:
        refs = self._latest_failed_operation_refs()
        if refs:
            run = self.graph.get_run(refs[0])
            return stage_group(run.spec.flow_tag) or stage_group(run.spec.stage_tag)
        return latest_failed_stage_from_events(self.store.read_events(self.run_id))

    def _latest_failed_operation_refs(self, *, stage: str = '') -> list[OperationRunRef]:
        stage = stage_group(stage)
        latest: dict[str, tuple[int, OperationRunRef]] = {}
        for ref in self.graph.run_refs():
            if is_synthetic_operation(str(ref)): continue
            run = self.graph.get_run(ref)
            if stage and stage not in {stage_group(run.spec.flow_tag), stage_group(run.spec.stage_tag)}:
                continue
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


def _normalize_case_id(value: Any) -> str:
    text = str(value or '').strip()
    if text.isdigit():
        text = f'case_{int(text):04d}'
    return validate_id(text, 'case_id')


def _business_operation_refs(refs: list[OperationRunRef]) -> list[OperationRunRef]:
    return [ref for ref in refs if not is_synthetic_operation(str(ref))]


def _dataset_difficulty(index: int, total: int) -> str:
    return 'medium' if total <= 1 else ('easy', 'medium', 'hard')[(index - 1) % 3]


def _has_error(result: OperationResult) -> bool:
    return result.status != 'ended' or any(ref.artifact_id.startswith('error_') for ref in result.output_refs)


def _is_intent_answer_payload(payload: Any) -> bool:
    return isinstance(payload, dict) and 'answer' in payload and 'query_intent_id' in payload


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
