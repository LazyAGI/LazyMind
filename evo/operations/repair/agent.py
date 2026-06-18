from __future__ import annotations

import hashlib
import json
import os
import random
import shutil
import subprocess
from pathlib import Path
from typing import Any
from uuid import uuid4

import numpy as np

from ..analysis.candidate import candidate_failure_categories, candidate_row_refs, candidate_trace_summary
from ..analysis.candidate import classify_candidate_case
from ..analysis.coarse import NON_REPAIRABLE_CATEGORIES
from ..analysis.utils import METRICS, score, typed_payload
from ...artifacts import ArtifactDraft, ArtifactGraph, ArtifactRef
from ..dataset.utils import json_object, validate_case_id
from ... import validate_id
from ...runtime import AdapterCall, OperationContext, OperationOutput, evo_llm
from ..eval.judge_answer import _hits, _recall, _scores, build_judge_prompt
from ..eval.policy import DEFAULT_EVALUATION_POLICY, failure_type as policy_failure_type
from ..eval.policy import quality_label as policy_quality_label
from ..eval.rag_answer import KB_CHAT_TOOLS, _call_chat
from .analyzer import RepairAnalyzer, direction_config
from .analyzer import extract_stage_hits as analyzer_stage_hits
from .branch import apply_branch_decision, branch_state_after, branch_state_before, prepare_physical_attempt
from .branch import state_transition
from .candidate import command_args, default_repair_scope, ensure_git_baseline, start_candidate_process, terminate_pid
from .critic import build_branch_decision, build_patch_critique
from .opencode import PASSTHROUGH_PREFIXES, run_opencode_streaming, trace_payload
from .patch_validate import assess_patch_correctness
from .schemas import probe_gate_status, validate_patch_gate_artifacts, validate_repair_artifact
from .worker_report import build_worker_report

DEFAULT_REPAIR_ATTEMPT_BUDGET = 100
DETERMINISTIC_GATE_FAILURES = {'no_primary_location', 'stage_conflict_requires_reanalysis'}
PATCH_IGNORES = ('.evo_repair_logs',), {'opencode.json'}
CATEGORY_PRIMARY_METRIC = {
    'retrieval_doc_miss': 'doc_recall',
    'retrieval_chunk_miss': 'context_recall',
    'topk_cutoff_issue': 'context_recall',
    'rrf_merge_drop': 'context_recall',
    'rerank_drop': 'context_recall',
}
ANSWER_GUARDRAIL_TOLERANCE = 0.05
ANCHOR_LOCK_RELEASE_AFTER = 3
MECHANISM_PROGRESS_KINDS = {'fixed_transition_failure', 'moved_later'}


class BuildRepairLoopPlanOperation:
    def execute(self, ctx: OperationContext) -> OperationOutput:
        report_ref = ArtifactRef.parse(str(ctx.params.get('classification_report_ref') or ''))
        report = typed_payload(ctx, report_ref, 'ClassificationReport')
        eval_ref = ArtifactRef.parse(str(report.get('eval_report_ref') or ''))
        eval_report = typed_payload(ctx, eval_ref, 'EvalReport')
        output_id = validate_id(str(ctx.params.get('output_id') or 'repair_loop_plan'), 'output_id')
        priorities = [p for p in report.get('priorities') or [] if isinstance(p, dict)]
        category = _category(ctx, priorities)
        targets, baseline = _target_rows(ctx, report, priorities, category), _baseline(ctx)
        guards, guard_meta = _goodcase_guard(ctx, eval_report, targets)
        heldout = _heldout_policy(ctx, report, eval_report, targets, guards, category)
        delta = float(ctx.params.get('target_mean_delta', 0.02))
        primary_metric = (
            ctx.params.get('primary_metric')
            or CATEGORY_PRIMARY_METRIC.get(category)
            or 'answer_correctness'
        )
        policy = {'primary_metric': str(primary_metric),
                  'target_mean_delta': round(delta / 100 if delta >= 1 else delta, 4),
                  'goodcase_regression_ratio_limit': round(float(ctx.params.get(
                      'goodcase_regression_ratio_limit', 0.34
                  )), 4)}
        if policy['primary_metric'] not in METRICS:
            raise ValueError(f'unsupported primary_metric: {policy["primary_metric"]}')
        payload = {'id': output_id, 'classification_report_ref': str(report_ref), 'eval_report_ref': str(eval_ref),
                   'eval_dataset_ref': str(eval_report.get('eval_dataset_ref') or report.get('eval_dataset_ref') or ''),
                   'target': {'fine_category': category,
                              'coarse_categories': sorted({str(row.get('coarse_category') or '') for row in targets}),
                              'badcase_ids': [row['case_id'] for row in targets],
                              'fine_refs': [row['fine_classification_ref'] for row in targets],
                              'baseline_judge_refs': [row['judge_result_ref'] for row in targets]},
                   'guard': {'goodcase_ids': [row['case_id'] for row in guards],
                             'goodcase_judge_refs': [row['judge_ref'] for row in guards], **guard_meta,
                             'sample_seed': str(ctx.params.get('random_seed')
                                                or f'{ctx.run_id}:{report_ref}:{category}')},
                   'heldout': heldout, 'policy': policy, 'baseline': baseline,
                   'loop_state': {'opencode_session_id': '',
                                  'candidate_workspace_ref': baseline.get('code_base_ref') or ''},
                   'source_message_id': str(ctx.params.get('source_message_id') or '')}
        refs = [report_ref, eval_ref, *[ArtifactRef.parse(row['fine_classification_ref']) for row in targets],
                *[ArtifactRef.parse(row['judge_ref']) for row in guards]]
        skipped = (heldout.get('summary') or {}).get('skipped_stratified_goodcases') or []
        refs += [ArtifactRef.parse(str(ref)) for ref in (
            list(heldout.get('sibling_badcase_judge_refs') or [])
            + list(heldout.get('stratified_goodcase_judge_refs') or [])
            + list(heldout.get('stratified_goodcase_case_refs') or [])
            + [str(row.get('judge_ref') or '') for row in skipped]
            + [str(row.get('case_ref') or '') for row in skipped]
        ) if str(ref or '')]
        refs += [ArtifactRef.parse(ref) for ref in (baseline.get('source_ref'),
                                                    baseline.get('metric_baseline_ref')) if ref]
        ctx.report_progress(phase='repair_plan', status='success', message=f'planned repair loop for {category}',
                            detail={'target_badcases': len(targets), 'guard_goodcases': len(guards)})
        return OperationOutput([_draft(output_id, 'RepairLoopPlan', payload, ctx, list(dict.fromkeys(refs)))])


class RepairLoopAgentOperation:
    def __init__(self, llm: Any | None = None, model_config: dict[str, Any] | None = None):
        self.llm, self.model_config = llm, model_config or {}

    def execute(self, ctx: OperationContext) -> OperationOutput:
        plan_ref = ArtifactRef.parse(str(ctx.params.get('repair_loop_plan_ref') or ''))
        plan = typed_payload(ctx, plan_ref, 'RepairLoopPlan')
        output_id = validate_id(str(ctx.params.get('output_id') or 'repair_loop_agent'), 'output_id')
        workspace = _workspace(ctx, plan)
        ensure_git_baseline(workspace)
        previous = _latest_state(ctx, plan_ref)
        if previous.get('status') == 'passed':
            ctx.report_progress(phase='repair_loop', status='success',
                                message='repair loop already terminal: passed',
                                detail={'current_attempt': previous.get('current_attempt'),
                                        'last_state_ref': previous.get('id', '')})
            return OperationOutput([])
        if previous.get('status') == 'failed':
            # A failed loop is resumable: a re-dispatched run continues from the recorded
            # attempt with a fresh budget instead of refusing to work.
            ctx.report_progress(phase='repair_loop', status='running',
                                message=f"resuming failed repair loop from attempt {previous.get('current_attempt')}",
                                detail={'current_attempt': previous.get('current_attempt')})
        memory = typed_payload(ctx, ArtifactRef.parse(previous['last_memory_ref']), 'RepairLoopMemory') \
            if previous.get('last_memory_ref') else {}
        session = str(previous.get('opencode_session_id')
                      or (plan.get('loop_state') or {}).get('opencode_session_id') or '')
        attempt = int(previous.get('current_attempt') or (plan.get('loop_state') or {}).get('current_attempt') or 0)
        raw_budget = (ctx.params.get('repair_attempt_budget') or ctx.params.get('max_attempts')
                      or os.getenv('EVO_REPAIR_ATTEMPT_BUDGET'))
        budget = DEFAULT_REPAIR_ATTEMPT_BUDGET if raw_budget in (None, '') else (
            int(raw_budget) if int(raw_budget) > 0 else None)
        start = attempt
        if self.llm is None:
            self.llm = evo_llm(self.model_config)
        drafts, decision, evaluation, patch, correctness = [], {}, {}, {}, {}
        while budget is None or attempt - start < budget:
            attempt += 1
            ctx.check_interrupt()
            ctx.report_progress(phase='repair_loop', status='running', message=f'starting repair attempt {attempt}',
                                detail={'attempt': attempt, 'attempt_budget': budget or 'unlimited'})
            made, decision, evaluation, memory, session, patch, correctness = _attempt(
                ctx, plan_ref, plan, workspace, attempt, memory, session, self.llm, self.model_config,
                budget_exhausted=budget is not None and attempt - start >= budget
            )
            drafts.extend(made)
            if decision['decision'] == 'passed':
                vid = f'verified_repair_{output_id}_attempt_{attempt}'
                refs = [plan_ref, ArtifactRef.parse(f"{patch['id']}@v1"),
                        ArtifactRef.parse(f"{evaluation['id']}@v1"), ArtifactRef.parse(f"{correctness['id']}@v1")]
                best = memory.get('best_baseline') if isinstance(memory.get('best_baseline'), dict) else {}
                drafts.append(_draft(vid, 'VerifiedRepair', _verified(
                    vid, plan_ref, Path(str(best.get('workspace_ref') or workspace)), patch, evaluation,
                    correctness, plan
                ), ctx, refs))
                break
            if decision['decision'] == 'failed':
                break
            if budget is None or attempt - start < budget:
                ctx.report_progress(phase='repair_loop', status='running',
                                    message=f'attempt {attempt} failed; continuing',
                                    detail={'failure': evaluation.get('failure_summary', '')})
        status = 'success' if decision.get('decision') == 'passed' else 'failed'
        ctx.report_progress(phase='repair_loop', status=status,
                            message=f"repair attempt {attempt} {decision.get('decision')}",
                            detail={'decision': decision.get('decision'),
                                    'evaluation_status': evaluation.get('status')})
        return OperationOutput(drafts)


def _attempt(ctx, plan_ref, plan, workspace, attempt, memory, session, llm, model_config,
             budget_exhausted: bool = False):
    branch_mode = str(ctx.params.get('repair_branch_mode') or 'physical').strip()
    if branch_mode not in {'physical', 'record'}:
        raise ValueError(f'unsupported repair_branch_mode: {branch_mode}')
    if branch_mode == 'physical':
        workspace, memory = prepare_physical_attempt(workspace, attempt, memory)
    branch_before = branch_state_before(attempt, plan_ref, workspace, memory, mode=branch_mode)
    if branch_before.get('status') != 'ready':
        return _prepare_failed_attempt(ctx, plan_ref, workspace, attempt, memory, session, branch_before)
    analyzer = RepairAnalyzer(ctx, plan_ref, plan, workspace)
    evidence = analyzer.collect_evidence(attempt, memory)
    fault_report = analyzer.localize_fault(attempt, evidence, memory)
    probe_plan = analyzer.build_probe_plan(attempt, fault_report)
    probe_result = analyzer.run_local_probe(attempt, probe_plan, fault_report)
    diagnosis = analyzer.diagnose(attempt, evidence, fault_report, probe_result, memory)
    explore_hypothesis, explore_repair_plan = _compat_artifacts(
        ctx, plan_ref, plan, attempt, evidence, fault_report, probe_result, diagnosis, memory
    )
    explore_artifacts: list[tuple[str, dict[str, Any]]] = []
    working_session = session
    probe_instruction_ref = ''
    if analyzer.needs_opencode_explore(probe_plan, probe_result, fault_report):
        before_explore = _git_status(workspace, *PATCH_IGNORES)
        explore_instruction = analyzer.build_explore_instruction(attempt, diagnosis, fault_report, probe_plan,
                                                                 probe_result)
        probe_instruction_ref = f"{explore_instruction['id']}@v1"
        explore_repair_plan['opencode_probe_instruction_ref'] = probe_instruction_ref
        explore = _run_opencode(ctx, plan, explore_repair_plan, explore_hypothesis, workspace, attempt,
                                working_session, explore_instruction, 'probe')
        working_session = explore.session_id or working_session
        explore_trace = trace_payload(explore, f"{explore_repair_plan['id']}@v1", attempt,
                                      _aid('opencode_probe_trace', attempt))
        probe_files, _, _ = _git_status(workspace, *PATCH_IGNORES)
        explore_worker_report = build_worker_report(attempt, explore_instruction, explore_trace, probe_files,
                                                    phase='probe')
        validate_repair_artifact('OpenCodeWorkerReport', explore_worker_report)
        if explore_worker_report.get('protocol_status') == 'invalid':
            _restore_diff_since(workspace, before_explore, *PATCH_IGNORES)
        probe_result = analyzer.merge_probe_result_from_worker(attempt, probe_result, fault_report,
                                                               explore_worker_report, explore_trace)
        diagnosis = analyzer.diagnose(attempt, evidence, fault_report, probe_result, memory)
        explore_artifacts = [('OpenCodeInstruction', explore_instruction), ('OpenCodeRunTrace', explore_trace),
                             ('OpenCodeWorkerReport', explore_worker_report)]
    hypothesis, repair_plan = _compat_artifacts(
        ctx, plan_ref, plan, attempt, evidence, fault_report, probe_result, diagnosis, memory, probe_instruction_ref
    )
    instruction = analyzer.build_instruction(attempt, diagnosis, fault_report, probe_result, memory)
    repair_plan['opencode_instruction_ref'] = f"{instruction['id']}@v1"
    terminal_gate = False
    if instruction.get('mode') == 'no_patch':
        gate_failure = (_gate_failure(explore_artifacts) or str(
            probe_gate_status(fault_report, probe_result, None).get('reason') or 'no_patch_gate_closed'))
        # These gates depend only on immutable run artifacts (classification report, trace),
        # so retrying further attempts cannot change the outcome.
        terminal_gate = gate_failure in DETERMINISTIC_GATE_FAILURES
        trace = _no_patch_trace(repair_plan['id'], instruction, attempt, gate_failure)
        worker_report = build_worker_report(attempt, instruction, trace, [], status='not_run', phase='no_patch')
        worker_report['stop_reason'] = gate_failure
        validate_repair_artifact('OpenCodeWorkerReport', worker_report)
        patch = _no_patch_candidate(workspace, plan_ref, repair_plan['id'], trace, attempt, gate_failure)
        service, proc = _placeholder_service(ctx, workspace, patch, attempt, gate_failure), None
        evaluation, candidate_report, candidate_drafts = (
            _incomplete(attempt, gate_failure), _candidate_report(attempt, [], gate_failure), []
        )
        opencode_session = working_session
    else:
        _validate_pending_patch_gate(ctx, instruction, fault_report, probe_result, explore_artifacts)
        before_patch_files, _, _ = _git_status(workspace, *PATCH_IGNORES)
        opencode = _run_opencode(ctx, plan, repair_plan, hypothesis, workspace, attempt, working_session,
                                 instruction, 'patch')
        trace = trace_payload(opencode, f"{repair_plan['id']}@v1", attempt, _aid('opencode_patch_trace', attempt))
        after_patch_files, _, _ = _git_status(workspace, *PATCH_IGNORES)
        patch_files = sorted(set(after_patch_files) - set(before_patch_files))
        worker_report = build_worker_report(attempt, instruction, trace, patch_files, phase='patch')
        validate_repair_artifact('OpenCodeWorkerReport', worker_report)
        patch = _patch(ctx, workspace, plan_ref, repair_plan['id'], trace, attempt, repair_plan, opencode,
                       worker_report, memory)
        service, proc = _service(ctx, workspace, patch, attempt)
        try:
            evaluation, candidate_report, candidate_drafts = _evaluate(ctx, plan, service, patch, attempt, llm,
                                                                       model_config)
        finally:
            if proc:
                terminate_pid(proc.pid)
        opencode_session = opencode.session_id or session
    validate_repair_artifact('RepairEvaluation', evaluation)
    correctness = assess_patch_correctness(attempt, patch, evaluation, worker_report)
    critique = build_patch_critique(attempt, diagnosis, instruction, worker_report, patch, evaluation,
                                    candidate_report, correctness, plan.get('policy') or {})
    branch_decision = build_branch_decision(
        attempt, critique, evaluation, patch, service, correctness, worker_report, plan.get('policy') or {},
        budget_exhausted=budget_exhausted, terminal_failure=terminal_gate,
    )
    branch_apply = apply_branch_decision(workspace, branch_before, branch_decision)
    if branch_apply.get('status') == 'failed':
        branch_decision = {**branch_decision, 'decision': 'stop_failed',
                           'reason': f"branch apply failed: {branch_apply.get('failure')}",
                           'decision_inputs': {**(branch_decision.get('decision_inputs') or {}),
                                               'branch_apply_status': 'failed',
                                               'branch_apply_failure': branch_apply.get('failure', '')}}
        validate_repair_artifact('BranchDecision', branch_decision)
    _complete_patch_application_model(patch, branch_apply)
    validate_repair_artifact('CodePatchCandidate', patch)
    branch_after = branch_state_after(attempt, plan_ref, workspace, branch_before, branch_decision, patch,
                                      evaluation, branch_apply)
    transition = state_transition(attempt, branch_before, branch_decision, branch_after)
    decision = _decision(evaluation, attempt, correctness, branch_decision)
    probe_worker_report = next((payload for schema, payload in explore_artifacts
                                if schema == 'OpenCodeWorkerReport'), None)
    memory = _memory(hypothesis, patch, evaluation, candidate_report, attempt, critique, branch_decision,
                     branch_after, probe_worker_report=probe_worker_report, prev_memory=memory)
    state_workspace = Path(str((branch_after.get('active_branch') or {}).get('workspace_ref')
                               or branch_after.get('workspace_ref') or workspace))
    state = _state(plan_ref, state_workspace, attempt, opencode_session, memory, patch, evaluation, decision,
                   branch_after, transition)
    artifacts = [('RepairBranchState', branch_before),
                 ('RepairEvidencePacket', evidence), ('FaultLocalizationReport', fault_report),
                 ('DiagnosticProbePlan', probe_plan), ('DiagnosticProbeResult', probe_result),
                 ('RepairDiagnosis', diagnosis), *explore_artifacts, ('OpenCodeInstruction', instruction),
                 ('RepairHypothesis', hypothesis), ('RepairPlan', repair_plan), ('OpenCodeRunTrace', trace),
                 ('OpenCodeWorkerReport', worker_report),
                 ('CodePatchCandidate', patch), ('CandidateServiceRun', service), ('RepairEvaluation', evaluation),
                 ('PatchCorrectnessAssessment', correctness), ('PatchCritique', critique),
                 ('BranchDecision', branch_decision), ('RepairLoopMemory', memory),
                 ('RepairBranchState', branch_after), ('RepairStateTransition', transition),
                 ('RepairLoopDecision', decision), ('RepairLoopState', state)]
    drafts = [_draft(payload['id'], schema, payload, ctx, [plan_ref]) for schema, payload in artifacts]
    drafts.extend(candidate_drafts)
    refs = [plan_ref, *[ArtifactRef.parse(ref) for row in candidate_report.get('cases', [])
                        for ref in candidate_row_refs(row)]]
    drafts.append(_draft(candidate_report['id'], 'CandidateClassificationReport', candidate_report, ctx, refs))
    return drafts, decision, evaluation, memory, opencode_session, patch, correctness


def _validate_pending_patch_gate(ctx: OperationContext, instruction: dict[str, Any], fault_report: dict[str, Any],
                                 probe_result: dict[str, Any],
                                 explore_artifacts: list[tuple[str, dict[str, Any]]]) -> None:
    gate_root = ctx.draft_dir / f'patch_gate_attempt_{instruction["attempt"]}_{uuid4().hex[:8]}'
    graph = ArtifactGraph(gate_root)
    for schema, payload in explore_artifacts:
        if schema in {'OpenCodeRunTrace', 'OpenCodeWorkerReport'}:
            graph.commit_artifact(ArtifactDraft(payload['id'], schema, payload, ctx.operation_run_id))
    graph.commit_artifact(ArtifactDraft(probe_result['id'], 'DiagnosticProbeResult', probe_result,
                                        ctx.operation_run_id))
    validate_patch_gate_artifacts(graph, instruction, fault_report)


def _prepare_failed_attempt(ctx, plan_ref, workspace, attempt, memory, session, branch_before):
    reason = str((branch_before.get('prepare_invariant') or {}).get('failure') or 'branch_prepare_failed')
    patch = _empty_patch_candidate(workspace, plan_ref, attempt, reason)
    evaluation = _incomplete(attempt, reason)
    validate_repair_artifact('RepairEvaluation', evaluation)
    candidate_report = _candidate_report(attempt, [], reason)
    correctness = assess_patch_correctness(
        attempt, patch, evaluation, {'id': _aid('opencode_worker_report', attempt), 'protocol_status': 'not_run'}
    )
    branch_decision = _terminal_branch_decision(attempt, reason)
    branch_apply = {'status': 'passed', 'action': 'prepare_failed', 'decision': 'stop_failed',
                    'checkpoint_status': 'not_run', 'failure': reason,
                    'before_head': (branch_before.get('workspace_status') or {}).get('git_head', ''),
                    'after_head': (branch_before.get('workspace_status') or {}).get('git_head', '')}
    branch_after = branch_state_after(attempt, plan_ref, workspace, branch_before, branch_decision, patch,
                                      evaluation, branch_apply)
    transition = state_transition(attempt, branch_before, branch_decision, branch_after)
    decision = _decision(evaluation, attempt, correctness, branch_decision)
    hypothesis = {'id': _aid('repair_hypothesis', attempt), 'supported_directions': [], 'rejected_directions': [],
                  'trace_steps_read': [], 'source_files_read': []}
    loop_memory = _memory(hypothesis, patch, evaluation, candidate_report, attempt, None, branch_decision,
                          branch_after, prev_memory=memory)
    state = _state(plan_ref, workspace, attempt, session, loop_memory, patch, evaluation, decision,
                   branch_after, transition)
    artifacts = [('RepairBranchState', branch_before), ('CodePatchCandidate', patch),
                 ('RepairEvaluation', evaluation), ('PatchCorrectnessAssessment', correctness),
                 ('BranchDecision', branch_decision), ('RepairLoopMemory', loop_memory),
                 ('RepairBranchState', branch_after), ('RepairStateTransition', transition),
                 ('RepairLoopDecision', decision), ('RepairLoopState', state)]
    drafts = [_draft(payload['id'], schema, payload, ctx, [plan_ref]) for schema, payload in artifacts]
    drafts.append(_draft(candidate_report['id'], 'CandidateClassificationReport', candidate_report, ctx, [plan_ref]))
    return drafts, decision, evaluation, loop_memory, session, patch, correctness


def _compat_hypothesis(plan_ref: ArtifactRef, plan: dict[str, Any], attempt: int, evidence: dict[str, Any],
                       fault_report: dict[str, Any], probe_result: dict[str, Any], diagnosis: dict[str, Any],
                       memory: dict[str, Any] | None) -> dict[str, Any]:
    signature = diagnosis.get('target_signature') if isinstance(diagnosis.get('target_signature'), dict) else {}
    category = str(signature.get('fine_category') or (plan.get('target') or {}).get('fine_category') or '')
    experiment = diagnosis.get('next_experiment') if isinstance(diagnosis.get('next_experiment'), dict) else {}
    primary = (diagnosis.get('root_cause_hypotheses') or [{}])[0]
    locations = diagnosis.get('suspected_code_locations') if isinstance(
        diagnosis.get('suspected_code_locations'), list
    ) else []
    entrypoints = [_location_entrypoint(item) for item in locations if isinstance(item, dict)]
    trace_steps = [{'case_id': item.get('case_id'), 'trace_id': item.get('trace_id'),
                    'step_ids': item.get('selected_step_ids', [])}
                   for item in evidence.get('trace_observations') or [] if isinstance(item, dict)]
    edit_focus = str(primary.get('claim') or experiment.get('goal') or '').strip()
    return {
        'id': _aid('repair_hypothesis', attempt), 'repair_loop_plan_ref': str(plan_ref), 'attempt': attempt,
        'compatibility_source': 'RepairDiagnosis',
        'tool_investigation': [
            {'tool': 'repair_evidence_packet', 'observation': {'ref': f"{evidence['id']}@v1"}},
            {'tool': 'fault_localization_report', 'observation': {'ref': f"{fault_report['id']}@v1"}},
            {'tool': 'diagnostic_probe_result',
             'observation': {'ref': f"{probe_result['id']}@v1",
                             'protocol_status': probe_result.get('protocol_status', ''),
                             'raw_trace_ref': probe_result.get('raw_trace_ref', '')}},
            {'tool': 'repair_diagnosis', 'observation': diagnosis},
        ],
        'analysis_card': {
            'fine_category': category, 'conclusion': str(experiment.get('goal') or ''), 'root_cause': primary,
            'edit_focus': edit_focus, 'trace_finding': str(signature.get('shared_failure') or ''),
            'entrypoints': entrypoints, 'previous_failure_summary': (memory or {}).get('next_focus', ''),
            'avoid_repeating': (memory or {}).get('failed_patch_summaries', [])[-3:],
        },
        'supported_directions': [category] if category else [],
        'rejected_directions': (memory or {}).get('rejected_directions', []),
        'trace_steps_read': trace_steps,
        'source_files_read': [item['path'] for item in entrypoints if item.get('path')],
        'diagnosis_ref': f"{diagnosis['id']}@v1", 'fault_localization_report_ref': f"{fault_report['id']}@v1",
    }


def _compat_repair_plan(ctx: OperationContext, plan_ref: ArtifactRef, hypothesis: dict[str, Any],
                        plan: dict[str, Any], attempt: int, diagnosis: dict[str, Any],
                        fault_report: dict[str, Any]) -> dict[str, Any]:
    cfg = direction_config(str((plan.get('target') or {}).get('fine_category') or ''))
    direction, edit_instruction = str(cfg['direction']), str(cfg['edit_hint'])
    scope = _repair_scope(ctx)
    experiment = diagnosis.get('next_experiment') if isinstance(diagnosis.get('next_experiment'), dict) else {}
    card = hypothesis.get('analysis_card') if isinstance(hypothesis.get('analysis_card'), dict) else {}
    focused_edits = [str(card.get('edit_focus') or '').strip(), str(experiment.get('success_signal') or '').strip(),
                     edit_instruction]
    seed_files = list(dict.fromkeys([
        *[str(item.get('path') or '') for item in fault_report.get('ranked_locations') or [] if isinstance(item, dict)],
        *list(scope.get('seed_files') or []),
    ]))
    return {
        'id': _aid('repair_plan', attempt), 'repair_loop_plan_ref': str(plan_ref),
        'repair_hypothesis_ref': f"{hypothesis['id']}@v1", 'attempt': attempt,
        'compatibility_source': 'RepairDiagnosis', 'diagnosis_ref': f"{diagnosis['id']}@v1",
        'change_plan': {
            'scope': 'minimal', **scope, 'seed_files': [item for item in seed_files if item],
            'edits': [item for item in dict.fromkeys(focused_edits) if item],
            'user_note': str(ctx.params.get('repair_instruction') or '').strip(),
            'repair_direction': str(experiment.get('goal') or direction),
        },
    }


def _compat_artifacts(ctx: OperationContext, plan_ref: ArtifactRef, plan: dict[str, Any], attempt: int,
                      evidence: dict[str, Any], fault_report: dict[str, Any], probe_result: dict[str, Any],
                      diagnosis: dict[str, Any], memory: dict[str, Any] | None,
                      probe_instruction_ref: str = '') -> tuple[dict[str, Any], dict[str, Any]]:
    hypothesis = _compat_hypothesis(plan_ref, plan, attempt, evidence, fault_report, probe_result, diagnosis, memory)
    repair_plan = _compat_repair_plan(ctx, plan_ref, hypothesis, plan, attempt, diagnosis, fault_report)
    if probe_instruction_ref:
        repair_plan['opencode_probe_instruction_ref'] = probe_instruction_ref
    return hypothesis, repair_plan


def _location_entrypoint(location: dict[str, Any]) -> dict[str, Any]:
    return {'path': str(location.get('path') or ''), 'symbol': str(location.get('symbol') or ''),
            'line_start': int(location.get('line_start') or 0), 'line_end': int(location.get('line_end') or 0),
            'confidence': str(location.get('confidence') or '')}


def _run_opencode(ctx, plan, repair_plan, hypothesis, workspace, attempt, session, instruction=None,
                  phase: str = 'patch'):
    env = {key: value for key, value in os.environ.items() if value and key.startswith(PASSTHROUGH_PREFIXES)}
    scope = repair_plan['change_plan']
    ctx.report_progress(phase='opencode', status='running', message=f'starting opencode {phase} attempt {attempt}',
                        detail={'repair_scope': {key: scope.get(key) for key in (
                            'allowed_roots', 'seed_files', 'blocked_roots', 'allow_new_files'
                        )}})
    container = str(ctx.params.get('opencode_container') or os.getenv('EVO_FLOW_OPENCODE_CONTAINER') or '')
    return run_opencode_streaming(
        container=container if container and shutil.which('docker') else '', workdir=str(workspace),
        prompt=json.dumps(instruction, ensure_ascii=False, indent=2),
        artifact_dir=workspace / '.evo_repair_logs' / 'opencode' / f'attempt_{attempt}' / phase,
        session_id=session, env=env,
        timeout_s=int(ctx.params.get('opencode_timeout_s') or os.getenv('OPENCODE_TIMEOUT_S')
                      or os.getenv('LAZYMIND_EVO_CODE_TIMEOUT_S') or 900),
        first_response_timeout_s=int(ctx.params.get('opencode_first_response_timeout_s')
                                     or os.getenv('OPENCODE_FIRST_RESPONSE_TIMEOUT_S')
                                     or os.getenv('LAZYMIND_EVO_CODE_FIRST_RESPONSE_TIMEOUT_S') or 300),
        register_cancel=ctx.register_cancel_callback,
        on_event=lambda _e, c: _opencode_progress(ctx, c, attempt, phase))


def _opencode_progress(ctx: OperationContext, compact: dict[str, Any], attempt: int, phase_name: str = 'patch') -> None:
    ui = compact.get('ui_event') if isinstance(compact.get('ui_event'), dict) else {}
    title = str(ui.get('title') or '').strip()
    summary = str(compact.get('summary') or ui.get('summary') or '').strip()
    event = str(compact.get('event_type') or 'event').strip()
    status = 'failed' if event in {'error', 'setup_failed', 'process_failed', 'timeout',
                                   'first_response_timeout'} else 'running'
    message = summary or title or f'opencode {event}'
    ctx.report_progress(phase='opencode', status=status, message=f'{phase_name} attempt {attempt}: {message[:140]}',
                        current_item=str(ui.get('kind') or event), detail=compact)


def _patch(ctx, workspace, plan_ref, repair_ref, trace, attempt, repair_plan, opencode,
           worker_report: dict[str, Any] | None = None, memory: dict[str, Any] | None = None) -> dict[str, Any]:
    base_head = _git(workspace, ['rev-parse', '--verify', 'HEAD']).strip()
    changed, created, _ = _git_status(workspace, *PATCH_IGNORES)
    if created:
        _git(workspace, ['add', '-N', *created])
    diff = _git(workspace, ['diff', '--', *changed]) if changed else ''
    diff_ref = _write_patch_diff(workspace, attempt, diff)
    check = _scope_check(changed, created, repair_plan['change_plan'])
    worker_failure = _worker_failure(worker_report or {})
    opencode_error = isinstance(opencode.last_error, dict) and opencode.last_error.get('type')
    failure = f"opencode_{opencode.last_error['type']}" if opencode_error else ''
    failure = failure or (
        'opencode_mapping_failed' if trace.get('mapping_status') not in {'complete', 'events_and_diff'} else ''
    )
    failure = failure or ('opencode_failed' if opencode.returncode else '') or check['failure'] or worker_failure
    failure = failure or ('no_diff' if not changed or not diff.strip() else '')
    hunks = _changed_hunks(workspace, diff)
    budget = int(ctx.params.get('diff_budget_lines') or 500)
    changed_lines = sum(1 for line in diff.splitlines() if (line.startswith('+') and not line.startswith('+++'))
                        or (line.startswith('-') and not line.startswith('---')))
    failure = failure or (f'diff_budget_exceeded: {changed_lines} > {budget} changed lines'
                          if changed_lines > budget else '')
    failure = failure or ('comment_only_patch' if hunks and all(h.get('comment_only') is True for h in hunks) else '')
    fingerprint = _patch_fingerprint(diff)
    # A patch that is semantically identical to an already-failed one is rejected before any
    # candidate service or judge budget is spent on re-evaluating it.
    failure = failure or ('duplicate_patch' if fingerprint and fingerprint in set(
        (memory or {}).get('failed_patch_fingerprints') or []
    ) else '')
    check |= {'status': 'failed' if failure else 'passed', 'failure': failure,
              'opencode_returncode': opencode.returncode, 'opencode_error': opencode.last_error}
    ctx.report_progress(phase='repair_patch', status='success' if check['status'] == 'passed' else 'failed',
                        message=f"patch scope {check['status']}",
                        detail={'attempt': attempt, 'files_changed': changed, 'failure': failure})
    return {'id': _aid('code_patch_candidate', attempt), 'repair_loop_plan_ref': str(plan_ref),
            'repair_plan_ref': f'{repair_ref}@v1', 'attempt': attempt,
            'workspace_ref': str(workspace), 'files_changed': changed, 'files_created': created,
            'base_git_head': base_head, 'post_patch_git_head': '', 'diff_ref': diff_ref,
            'apply_status': 'applied' if diff.strip() else 'failed',
            'reverse_apply_status': _reverse_apply_status(workspace, diff), 'changed_hunks': hunks,
            'diff': diff, 'fingerprint': fingerprint, 'scope_check': check,
            'opencode_run_trace_ref': f"{trace['id']}@v1"}


def _patch_fingerprint(diff: str) -> str:
    """Normalized semantic fingerprint of a diff (per-file added/removed code lines, whitespace
    and comments stripped) used to reject near-identical retries of already-failed patches."""
    current, tokens = '', set()
    for line in diff.splitlines():
        if line.startswith('+++ b/'):
            current = line[6:]
            continue
        if (line.startswith('+') and not line.startswith('+++')) or (
            line.startswith('-') and not line.startswith('---')
        ):
            text = ' '.join(line[1:].split())
            if text and not text.startswith('#'):
                tokens.add(f'{current}::{line[0]}{text}')
    digest = hashlib.sha256('\n'.join(sorted(tokens)).encode('utf-8')).hexdigest()
    return f'sha256:{digest}' if tokens else ''


def _no_patch_trace(repair_ref: str, instruction: dict[str, Any], attempt: int,
                    reason: str = 'no_patch_gate_closed') -> dict[str, Any]:
    return {
        'id': _aid('opencode_patch_trace', attempt), 'repair_plan_ref': f'{repair_ref}@v1', 'attempt': attempt,
        'returncode': 0, 'raw_paths': {'prompt': ''},
        'prompt_delivery': {'mode': 'no_patch', 'instruction': '', 'prompt_path': ''},
        'provider': '', 'model': '', 'mapping_status': 'no_patch',
        'session_mapping': {'status': 'not_started', 'source': 'no_patch_gate', 'session_id': ''},
        'event_counts': {},
        'ui_events': [{'index': 0, 'kind': 'analysis', 'title': '跳过代码修改',
                       'summary': 'Analyzer patch gate closed before opencode', 'status': 'completed',
                       'raw_event_index': None}],
        'files_modified': [],
        'last_error': {'type': 'no_patch_gate_closed', 'message': instruction.get('objective', ''),
                       'reason': reason},
        'duration_seconds': 0.0, 'setup_seconds': 0.0, 'first_response_seconds': None,
        'first_response_diagnosis': {'status': 'not_started', 'reason': reason},
    }


def _gate_failure(explore_artifacts: list[tuple[str, dict[str, Any]]]) -> str:
    for schema, payload in reversed(explore_artifacts):
        if schema == 'OpenCodeRunTrace':
            failure = _trace_failure(payload)
            if failure:
                return failure
    for schema, payload in reversed(explore_artifacts):
        if schema == 'OpenCodeWorkerReport':
            failure = _worker_failure(payload)
            if failure:
                return failure
    return ''


def _trace_failure(trace: dict[str, Any]) -> str:
    last_error = trace.get('last_error') if isinstance(trace.get('last_error'), dict) else {}
    error_type = str(last_error.get('type') or '')
    if error_type:
        return f'opencode_{error_type}'
    if trace and trace.get('mapping_status') == 'failed':
        return 'opencode_mapping_failed'
    if int(trace.get('returncode') or 0):
        return 'opencode_failed'
    return ''


def _worker_failure(worker_report: dict[str, Any]) -> str:
    status = str(worker_report.get('protocol_status') or '')
    if status == 'valid' or not worker_report:
        return ''
    if status == 'missing':
        return 'worker_report_missing'
    if status == 'invalid':
        return 'worker_protocol_violation'
    return f'worker_report_{status}'


def _no_patch_candidate(workspace: Path, plan_ref: ArtifactRef, repair_ref: str, trace: dict[str, Any],
                        attempt: int, failure: str = 'no_patch_gate_closed') -> dict[str, Any]:
    base_head = _git(workspace, ['rev-parse', '--verify', 'HEAD']).strip()
    changed, created, _ = _git_status(workspace, *PATCH_IGNORES)
    diff = _git(workspace, ['diff', '--', *changed]) if changed else ''
    diff_ref = _write_patch_diff(workspace, attempt, diff)
    return {'id': _aid('code_patch_candidate', attempt), 'repair_loop_plan_ref': str(plan_ref),
            'repair_plan_ref': f'{repair_ref}@v1', 'attempt': attempt, 'workspace_ref': str(workspace),
            'files_changed': changed, 'files_created': created, 'base_git_head': base_head,
            'post_patch_git_head': '', 'diff_ref': diff_ref, 'apply_status': 'failed',
            'reverse_apply_status': _reverse_apply_status(workspace, diff),
            'changed_hunks': _changed_hunks(workspace, diff), 'diff': diff,
            'scope_check': {'status': 'failed', 'unexpected_files': changed, 'allow_new_files': True,
                            'failure': failure, 'opencode_returncode': 0,
                            'opencode_error': trace.get('last_error')},
            'opencode_run_trace_ref': f"{trace['id']}@v1"}


def _empty_patch_candidate(workspace: Path, plan_ref: ArtifactRef, attempt: int, failure: str) -> dict[str, Any]:
    base_head = _git(workspace, ['rev-parse', '--verify', 'HEAD']).strip()
    changed, created, _ = _git_status(workspace, *PATCH_IGNORES)
    diff_ref = _write_patch_diff(workspace, attempt, '')
    patch = {'id': _aid('code_patch_candidate', attempt), 'repair_loop_plan_ref': str(plan_ref),
             'repair_plan_ref': '', 'attempt': attempt, 'workspace_ref': str(workspace),
             'files_changed': changed, 'files_created': created, 'base_git_head': base_head,
             'post_patch_git_head': '', 'diff_ref': diff_ref, 'apply_status': 'failed',
             'reverse_apply_status': 'not_checked', 'changed_hunks': [], 'diff': '',
             'scope_check': {'status': 'failed', 'unexpected_files': changed, 'allow_new_files': True,
                             'failure': failure, 'opencode_returncode': 0, 'opencode_error': None},
             'opencode_run_trace_ref': ''}
    validate_repair_artifact('CodePatchCandidate', patch)
    return patch


def _complete_patch_application_model(patch: dict[str, Any], branch_apply: dict[str, Any]) -> None:
    patch['post_patch_git_head'] = ''
    checkpoint = branch_apply.get('candidate_checkpoint') if isinstance(
        branch_apply.get('candidate_checkpoint'), dict
    ) else branch_apply
    if checkpoint.get('action') == 'keep_current_branch':
        patch['post_patch_git_head'] = str(checkpoint.get('after_head') or '')
    elif branch_apply.get('action') == 'stop_failed':
        patch['post_patch_git_head'] = str(branch_apply.get('after_head') or '')
    if branch_apply.get('status') == 'failed':
        patch['apply_status'] = 'failed'
    elif checkpoint.get('action') == 'keep_current_branch' and checkpoint.get('checkpoint_status') in {
        'committed', 'not_needed',
    }:
        patch['apply_status'] = 'applied' if patch.get('diff') else 'not_applied'


def _write_patch_diff(workspace: Path, attempt: int, diff: str) -> str:
    ref = f'snapshots/patch_candidate_attempt_{attempt}.diff'
    path = workspace / '.evo_repair_logs' / ref
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(diff, encoding='utf-8')
    return ref


def _reverse_apply_status(workspace: Path, diff: str) -> str:
    if not diff.strip():
        return 'not_checked'
    result = subprocess.run(
        ['git', '-c', f'safe.directory={workspace}', '-C', str(workspace), 'apply', '--reverse', '--check', '-'],
        input=diff, capture_output=True, text=True, check=False,
    )
    return 'verified' if result.returncode == 0 else 'failed'


def _changed_hunks(workspace: Path, diff: str) -> list[dict[str, Any]]:
    hunks: list[dict[str, Any]] = []
    current, hunk = '', None
    for line in [*diff.splitlines(), 'diff --git sentinel sentinel']:
        if line.startswith('diff --git') or line.startswith('@@') or line.startswith('+++ b/'):
            if hunk:
                hunks.append(_finalize_hunk(workspace, hunk))
                hunk = None
        if line.startswith('+++ b/'):
            current = line[6:]
            continue
        if line.startswith('@@'):
            start, end = _hunk_range(line)
            hunk = {'path': current, 'line_start': start, 'line_end': end, 'lines': []}
            continue
        if hunk is not None:
            hunk['lines'].append(line)
    return hunks


def _hunk_range(line: str) -> tuple[int, int]:
    marker = next((part for part in line.split() if part.startswith('+')), '+0')
    start, _, count = marker[1:].partition(',')
    line_start, line_count = int(start or 0), int(count or '1')
    return line_start, max(line_start, line_start + line_count - 1)


def _finalize_hunk(workspace: Path, hunk: dict[str, Any]) -> dict[str, Any]:
    added = [line[1:].strip() for line in hunk.get('lines') or []
             if isinstance(line, str) and line.startswith('+') and not line.startswith('+++')]
    non_empty = [line for line in added if line]
    return {'path': hunk.get('path') or '', 'line_start': hunk.get('line_start') or 0,
            'line_end': hunk.get('line_end') or hunk.get('line_start') or 0,
            'symbol': _symbol_at(workspace / str(hunk.get('path') or ''), int(hunk.get('line_start') or 0)),
            'comment_only': bool(non_empty) and all(line.startswith('#') for line in non_empty)}


def _symbol_at(path: Path, line_number: int) -> str:
    if not path.exists() or line_number <= 0:
        return '__module__'
    symbol = '__module__'
    for index, line in enumerate(path.read_text(encoding='utf-8', errors='ignore').splitlines(), start=1):
        stripped = line.strip()
        if stripped.startswith('def ') or stripped.startswith('class '):
            symbol = stripped.split('(', 1)[0].split(':', 1)[0].split()[-1]
        if index >= line_number:
            return symbol
    return symbol


def _service(ctx, workspace, patch, attempt):
    log_path = workspace / '.evo_repair_logs' / f'candidate_service_attempt_{attempt}.log'
    command = command_args(ctx.params.get('candidate_service_command'))
    chat_url = str(ctx.params.get('candidate_chat_url') or '')
    health_url = str(ctx.params.get('candidate_healthcheck_url') or '')
    process = {'pid': 0, 'log_path': str(log_path)}
    health, proc = {'status': 'not_started' if not command else 'pending'}, None
    if patch['scope_check']['status'] != 'passed':
        health = {'status': 'not_started', 'reason': patch['scope_check'].get('failure') or 'patch_failed'}
    elif command:
        ctx.report_progress(phase='repair_candidate_service', status='running',
                            message='starting candidate service from candidate worktree',
                            detail={'workspace_ref': str(workspace), 'command': command})
        try:
            proc, health, process = start_candidate_process(
                ctx, workspace, command, chat_url, health_url, log_path,
                timeout_s=int(ctx.params.get('candidate_health_timeout_s', 60)))
            ctx.report_progress(phase='repair_candidate_service', status='success',
                                message='candidate service healthcheck passed', detail={'pid': proc.pid})
        except Exception as exc:
            health = {'status': 'failed', 'error': str(exc)}
            ctx.report_progress(phase='repair_candidate_service', status='failed',
                                message='candidate service healthcheck failed', detail=health)
    return {'id': _aid('candidate_service', attempt), 'code_patch_candidate_ref': f"{patch['id']}@v1",
            'attempt': attempt, 'workspace_ref': str(workspace), 'service_url': chat_url,
            'dataset_name': str(ctx.params.get('dataset_name') or ''), 'healthcheck': health,
            'process': process}, proc


def _placeholder_service(ctx, workspace, patch, attempt, reason: str) -> dict[str, Any]:
    log_path = workspace / '.evo_repair_logs' / f'candidate_service_attempt_{attempt}.log'
    return {'id': _aid('candidate_service', attempt), 'code_patch_candidate_ref': f"{patch['id']}@v1",
            'attempt': attempt, 'workspace_ref': str(workspace), 'service_url': '',
            'dataset_name': str(ctx.params.get('dataset_name') or ''),
            'healthcheck': {'status': 'not_started', 'reason': reason},
            'process': {'pid': 0, 'log_path': str(log_path), 'status': 'not_started'}}


def _evaluate(ctx, plan, service, patch, attempt, llm, model_config):
    if patch['scope_check']['status'] != 'passed':
        failure = patch['scope_check'].get('failure') or 'patch_failed'
        return _incomplete(attempt, failure, stage='patch_gate'), _candidate_report(attempt, [], failure), []
    if service.get('healthcheck', {}).get('status') != 'passed':
        # Service startup failure is an infra failure of the attempt, distinct from a quality miss.
        failure = ('candidate_service_startup_failed: '
                   f"{(service.get('healthcheck') or {}).get('error') or 'service not started'}")
        return (_incomplete(attempt, failure, stage='candidate_service_startup'),
                _candidate_report(attempt, [], failure), [])
    target_url = service.get('service_url')
    dataset_name = str(ctx.params.get('dataset_name') or service.get('dataset_name') or '')
    failure = '' if target_url and dataset_name and str(target_url).endswith('/api/chat/stream') else (
        'candidate_chat_url and dataset_name are required for real repair evaluation')
    if failure:
        return _incomplete(attempt, failure), _candidate_report(attempt, [], failure), []
    heldout_plan = plan.get('heldout') if isinstance(plan.get('heldout'), dict) else {}
    primary = plan['policy']['primary_metric']
    bad = [_safe_eval_case(ctx, plan, case_id, target_url, dataset_name, model_config, llm)
           for case_id in (plan.get('target') or {}).get('badcase_ids') or []]
    trace_delta_bad = _trace_delta(ctx, plan, bad)
    mechanism = _mechanism_progress(trace_delta_bad)
    bad_delta = round(_avg(row['after'][primary] for row in bad) - _avg(row['before'][primary] for row in bad), 4)
    if mechanism == 'unchanged' and bad_delta <= 0:
        # Mechanism gate: the patch neither moved the failing trace transition nor improved the
        # primary metric on target cases. Fail fast and keep the guard/heldout/judge budget.
        ctx.report_progress(phase='repair_evaluation', status='failed',
                            message='mechanism gate failed: trace transition unchanged',
                            detail={'attempt': attempt, 'primary_metric': primary, 'delta_mean': bad_delta})
        return _mechanism_gate_failed(plan, attempt, bad, trace_delta_bad, primary, bad_delta)
    rest_ids = [*list(heldout_plan.get('sibling_badcase_ids') or []),
                *list((plan.get('guard') or {}).get('goodcase_ids') or []),
                *list(heldout_plan.get('stratified_goodcase_ids') or [])]
    rest = [_safe_eval_case(ctx, plan, case_id, target_url, dataset_name, model_config, llm)
            for case_id in rest_ids]
    sibling_end = len(heldout_plan.get('sibling_badcase_ids') or [])
    guard_end = sibling_end + len((plan.get('guard') or {}).get('goodcase_ids') or [])
    heldout_bad, good, heldout_good = rest[:sibling_end], rest[sibling_end:guard_end], rest[guard_end:]
    rows = [*bad, *rest]
    overall = _overall(bad, primary, float(plan['policy']['target_mean_delta']),
                       mechanism_progress=mechanism == 'improved')
    guard = _guard(good, float(plan['policy'].get('goodcase_regression_ratio_limit', 0.34)),
                   str((plan.get('guard') or {}).get('mode') or 'sampled'))
    commands = [_run_command(ctx, command, attempt, service.get('workspace_ref'), index=index)
                for index, command in enumerate(ctx.params.get('verification_commands') or [], start=1)]
    command_failures = [item for item in commands if item.get('status') == 'failed' or item.get('exit_code')]
    if not commands:
        # Mandatory verification profile: a patch without any verification command cannot pass.
        command_failures = [{'status': 'failed', 'failure': 'verification_profile_missing',
                             'command': '', 'exit_code': -1}]
    failed_command = bool(command_failures)
    status = 'passed' if overall['passed'] and guard['passed'] and not failed_command else 'failed'
    failure = '' if status == 'passed' else '; '.join(item for item in (
        overall.get('failure'), guard.get('failure'), 'verification command failed' if failed_command else ''
    ) if item)
    classified = [_safe_classify_candidate_case(ctx, row, plan, attempt, llm) for row in _candidate_focus(rows, bad)]
    trace_delta = trace_delta_bad + _trace_delta(ctx, plan, rest)
    case_failures = [row['case_failure'] for row in rows if isinstance(row.get('case_failure'), dict)]
    evaluation = {'id': _aid('repair_evaluation', attempt), 'attempt': attempt, 'status': status,
                  'overall_eval': overall, 'badcase_eval': _bad(bad, primary), 'goodcase_impact': guard,
                  'goodcase_guard': guard,
                  'metric_delta_by_case': {str(row.get('case_id') or ''): row.get('delta') or {}
                                           for row in rows if row.get('case_id')},
                  'trace_delta_by_case': trace_delta,
                  'target_success_cases': _case_ids(bad, {'improved'}),
                  'target_unchanged_cases': _case_ids(bad, {'unchanged'}),
                  'target_regressed_cases': _case_ids(bad, {'regressed', 'failed'}),
                  'guard_regressed_cases': _case_ids(good, {'regressed', 'failed'}),
                  'heldout_eval': _heldout_eval(heldout_plan, heldout_bad, heldout_good),
                  'candidate_execution_failures': _candidate_execution_failures(rows),
                  'verification_command_failures': command_failures,
                  'evaluation_confidence': ('low' if case_failures or command_failures else 'medium' if any(
                      item.get('delta') == 'unknown' for item in trace_delta) else 'high'),
                  'evaluation_error': _evaluation_error(case_failures, command_failures, trace_delta),
                  'case_failures': case_failures,
                  'candidate_classification_report_ref': f"{_aid('candidate_classification_report', attempt)}@v1",
                  'command_results': commands, 'failure_summary': failure,
                  'next_attempt_guidance': failure or 'target reached'}
    return evaluation, _candidate_report(attempt, [row for row, _ in classified], failure, trace_delta), [
        draft for _, drafts in classified for draft in drafts
    ]


def _safe_eval_case(ctx, plan, case_id, target_url, dataset_name, model_config, llm) -> dict[str, Any]:
    try:
        return _eval_case(ctx, plan, case_id, target_url, dataset_name, model_config, llm)
    except Exception as exc:
        return _case_failure_row(validate_case_id(str(case_id)), '', _zero_metrics(), 'load_artifact',
                                 str(exc)[:300], recoverable=False)


def _safe_classify_candidate_case(ctx, row, plan, attempt, llm) -> tuple[dict[str, Any], list[ArtifactDraft]]:
    if not str(row.get('case_ref') or '').strip():
        return row, []
    try:
        return classify_candidate_case(ctx, row, plan, attempt, llm)
    except Exception as exc:
        return {**row, 'classification_error': str(exc)[:300]}, []


def _eval_case(ctx, plan, case_id, target_url, dataset_name, model_config, llm) -> dict[str, Any]:
    dataset = typed_payload(ctx, ArtifactRef.parse(str(plan.get('eval_dataset_ref') or '')), 'EvalDataset')
    case_id = validate_case_id(case_id)
    case_ref = ArtifactRef.parse(str(dataset['case_refs'][list(dataset['case_ids']).index(case_id)]))
    case = typed_payload(ctx, case_ref, 'DatasetCase')
    baseline_metrics = ((plan.get('baseline') or {}).get('metric_baseline') or {}).get(case_id) or typed_payload(
        ctx, ArtifactRef.parse(_baseline_ref(plan, case_id)), 'JudgeResult')
    before = {metric: baseline_metrics.get(metric, 0.0) for metric in METRICS}
    primary = plan['policy']['primary_metric']
    try:
        payload = {'query': case['question'], 'history': [], 'trace': True,
                   'session_id': f'repair-{ctx.operation_run_id}-{case_id}-{uuid4().hex[:6]}',
                   'dataset': dataset_name, 'filters': {'kb_id': [dataset_name]}, 'reasoning': False,
                   'available_tools': KB_CHAT_TOOLS}
        rag = AdapterCall('rag.candidate.chat', lambda req: _call_chat(
            ctx, req['target_chat_url'], {**req['payload'], 'llm_config': model_config or None},
            timeout_s=float(ctx.params.get('candidate_case_timeout_s', 90)),
        )).run(ctx, {'target_chat_url': target_url, 'payload': payload},
               phase='repair_candidate_rag', item_ref=case_id).response
        trace_summary = candidate_trace_summary(ctx, rag)
    except Exception as exc:
        return _case_failure_row(case_id, str(case_ref), before, 'candidate_chat', str(exc)[:300])
    try:
        prompt, _ = build_judge_prompt(case, rag)
        raw = AdapterCall('llm.repair_judge', lambda payload: llm(payload['prompt'], stream=False)).run(
            ctx, {'prompt': prompt}, phase='repair_judge', item_ref=case_id).response
        scores = _scores(json_object(raw))
        doc_hits, doc_misses = _hits(case.get('reference_doc_ids'), rag.get('doc_ids'))
        chunk_hits, chunk_misses = _hits(case.get('reference_chunk_ids'), rag.get('chunk_ids'))
        after = scores | {'doc_recall': _recall(doc_hits, doc_misses),
                          'context_recall': _recall(chunk_hits, chunk_misses)}
    except Exception as exc:
        return _case_failure_row(case_id, str(case_ref), before, 'repair_judge', str(exc)[:300],
                                 rag=rag, trace_summary=trace_summary)
    reason = scores.get('reason')
    after['quality_label'] = policy_quality_label(
        DEFAULT_EVALUATION_POLICY, after['answer_correctness'], after['faithfulness'],
        after['doc_recall'], after['context_recall'])
    after['failure_type'] = policy_failure_type(
        DEFAULT_EVALUATION_POLICY, after['quality_label'], after['answer_correctness'], after['faithfulness'],
        after['doc_recall'], after['context_recall'])
    delta = {metric: round(float(after[metric]) - float(before[metric]), 4) for metric in METRICS}
    outcome = 'failed' if after['failure_type'] == 'candidate_execution_failed' else 'unchanged'
    outcome = 'improved' if outcome == 'unchanged' and delta[primary] > 0 else outcome
    outcome = 'regressed' if outcome == 'unchanged' and delta[primary] < 0 else outcome
    rag_keys = ('answer', 'contexts', 'doc_ids', 'chunk_ids', 'trace_id', 'kb_errors')
    candidate_rag = {key: rag.get(key, [] if key in {'contexts', 'doc_ids', 'chunk_ids', 'kb_errors'} else '')
                     for key in rag_keys}
    return {'case_id': case_id, 'case_ref': str(case_ref), 'baseline_judge_ref': _baseline_ref(plan, case_id),
            'candidate_rag_answer': candidate_rag, 'candidate_trace_summary': trace_summary,
            'candidate_judge_result': {**{metric: after[metric] for metric in METRICS},
                                       'is_correct': bool(after.get('is_correct')),
                                       'quality_label': after['quality_label'],
                                       'failure_type': after['failure_type'], 'reason': reason,
                                       'defect': reason if after['failure_type'] == 'candidate_execution_failed'
                                       else ''},
            'before': before, 'after': {metric: after[metric] for metric in METRICS}, 'delta': delta,
            'outcome': outcome}


def _case_failure_row(case_id: str, case_ref: str, before: dict[str, Any], stage: str, message: str, *,
                      recoverable: bool = True, rag: dict[str, Any] | None = None,
                      trace_summary: dict[str, Any] | None = None) -> dict[str, Any]:
    rag = rag or {'answer': '', 'contexts': [], 'doc_ids': [], 'chunk_ids': [], 'trace_id': '',
                  'kb_errors': [message]}
    trace_summary = trace_summary or {'trace_available': False, 'error': message}
    failure_type = 'candidate_judge_failed' if stage == 'repair_judge' else 'candidate_execution_failed'
    after = _zero_metrics()
    return {'case_id': case_id, 'case_ref': case_ref, 'baseline_judge_ref': '',
            'candidate_rag_answer': {'answer': rag.get('answer', ''), 'contexts': rag.get('contexts', []),
                                     'doc_ids': rag.get('doc_ids', []), 'chunk_ids': rag.get('chunk_ids', []),
                                     'trace_id': rag.get('trace_id', ''), 'kb_errors': rag.get('kb_errors', [])},
            'candidate_trace_summary': trace_summary,
            'candidate_judge_result': {**after, 'is_correct': False, 'quality_label': 'bad',
                                       'failure_type': failure_type, 'reason': message, 'defect': message},
            'before': before, 'after': after,
            'delta': {metric: round(float(after.get(metric, 0.0)) - float(before.get(metric, 0.0)), 4)
                      for metric in METRICS},
            'outcome': 'failed',
            'case_failure': {'case_id': case_id, 'stage': stage, 'message': message, 'recoverable': recoverable}}


def _category(ctx: OperationContext, priorities: list[dict[str, Any]]) -> str:
    requested = str(ctx.params.get('fine_category') or '').strip()
    available = [str(item.get('fine_category') or '') for item in priorities]
    repairable = [str(item.get('fine_category') or '') for item in priorities
                  if item.get('fine_category') not in NON_REPAIRABLE_CATEGORIES]
    if requested and requested not in available:
        raise ValueError(f'fine_category not found in ClassificationReport priorities: {requested}')
    if requested in NON_REPAIRABLE_CATEGORIES:
        raise ValueError(f'fine_category is not repairable: {requested}')
    if not (requested or repairable):
        raise ValueError('ClassificationReport has no repairable priorities')
    return requested or repairable[0]


def _target_rows(ctx, report, priorities, category) -> list[dict[str, Any]]:
    cases = {validate_case_id(str(row.get('case_id') or '')): row
             for row in report.get('cases') or [] if isinstance(row, dict)}
    priority = next((item for item in priorities if item.get('fine_category') == category), {})
    requested = [validate_case_id(str(item)) for item in ctx.params.get('target_case_ids') or []]
    reps = [str(ref).rsplit('@v', 1)[0].replace('case_fine_classification_', '')
            for ref in priority.get('representative_case_refs') or []]
    ids = requested or reps or list(priority.get('case_ids') or [])
    if not requested:
        ids = ids[: int(ctx.params.get('target_case_sample_size') or (1 if reps else len(ids)))]
    rows = []
    for case_id in ids:
        row = cases.get(validate_case_id(str(case_id)))
        if not row or row.get('fine_category') != category:
            raise ValueError(f'target case is not in selected fine_category {category}: {case_id}')
        fine = typed_payload(ctx, ArtifactRef.parse(str(row.get('fine_classification_ref') or '')),
                             'CaseFineClassification')
        rows.append({**row, 'judge_result_ref': str(fine.get('judge_result_ref') or '')})
    if not rows:
        raise ValueError('repair target badcase set is empty')
    return rows


def _goodcase_guard(ctx, eval_report, targets) -> tuple[list[dict[str, str]], dict[str, Any]]:
    bad_ids = {str(row.get('case_id') or '') for row in eval_report.get('bad_cases') or []}
    bad_ids |= {row['case_id'] for row in targets}
    candidates = []
    for raw_ref in eval_report.get('judge_result_refs') or []:
        ref = ArtifactRef.parse(str(raw_ref))
        judge = typed_payload(ctx, ref, 'JudgeResult')
        case_id = validate_case_id(str(judge.get('case_id') or ''))
        if case_id not in bad_ids and judge.get('quality_label') == 'good':
            candidates.append({'case_id': case_id, 'judge_ref': str(ref)})
    target_count, meta = len(targets), _guard_meta(ctx, len(candidates), len(targets))
    if not candidates:
        return [], meta | {'mode': 'no_goodcase', 'target_badcase_count': target_count}
    sample_size = meta['sample_size']
    rng = random.Random(str(ctx.params.get('random_seed') or f'{ctx.run_id}:'
                            f"{ctx.params.get('classification_report_ref')}"))
    guards = rng.sample(candidates, sample_size) if sample_size else []
    mode = 'sampled' if guards else 'disabled'
    return sorted(guards, key=lambda item: item['case_id']), meta | {'mode': mode,
                                                                     'target_badcase_count': target_count}


def _heldout_policy(ctx, report, eval_report, targets, guards, category: str) -> dict[str, Any]:
    if str(ctx.params.get('heldout_validation', 'true')).lower() in {'0', 'false', 'no'}:
        return {'enabled': False, 'sibling_badcase_ids': [], 'sibling_badcase_judge_refs': [],
                'stratified_goodcase_ids': [], 'stratified_goodcase_judge_refs': [], 'summary': {'reason': 'disabled'}}
    target_ids = {row['case_id'] for row in targets}
    guard_ids = {row['case_id'] for row in guards}
    sibling_cap = int(ctx.params.get('heldout_sibling_badcase_count') or 2)
    good_cap = int(ctx.params.get('heldout_stratified_goodcase_count') or 2)
    siblings = [{'case_id': str(row.get('case_id') or ''), 'judge_ref': str(row.get('judge_result_ref') or '')}
                for row in report.get('cases') or []
                if row.get('fine_category') == category and str(row.get('case_id') or '') not in target_ids
                and str(row.get('judge_result_ref') or '')][:max(0, sibling_cap)]
    stratified, skipped_goodcases = _stratified_goodcase_heldout(ctx, eval_report, target_ids | guard_ids,
                                                                 max(0, good_cap))
    return {
        'enabled': bool(siblings or stratified),
        'sibling_badcase_ids': [row['case_id'] for row in siblings],
        'sibling_badcase_judge_refs': [row['judge_ref'] for row in siblings],
        'stratified_goodcase_ids': [row['case_id'] for row in stratified],
        'stratified_goodcase_judge_refs': [row['judge_ref'] for row in stratified],
        'stratified_goodcase_case_refs': [row['case_ref'] for row in stratified],
        'summary': {
            'sibling_badcase_pool_size': sum(
                1 for row in report.get('cases') or []
                if row.get('fine_category') == category and str(row.get('case_id') or '') not in target_ids
            ),
            'selected_sibling_badcases': len(siblings), 'selected_stratified_goodcases': len(stratified),
            'goodcase_strata': sorted({row['stratum'] for row in stratified}),
            'skipped_stratified_goodcases': skipped_goodcases,
        },
    }


def _stratified_goodcase_heldout(ctx, eval_report, excluded_ids: set[str],
                                 cap: int) -> tuple[list[dict[str, str]], list[dict[str, str]]]:
    if cap <= 0:
        return [], []
    groups: dict[str, list[dict[str, str]]] = {}
    skipped: list[dict[str, str]] = []
    for raw_ref in eval_report.get('judge_result_refs') or []:
        ref = ArtifactRef.parse(str(raw_ref))
        judge = typed_payload(ctx, ref, 'JudgeResult')
        case_id = validate_case_id(str(judge.get('case_id') or ''))
        if case_id in excluded_ids or judge.get('quality_label') != 'good':
            continue
        case_ref = str(judge.get('case_ref') or '')
        try:
            case = typed_payload(ctx, ArtifactRef.parse(case_ref), 'DatasetCase')
        except Exception as exc:
            skipped.append({'case_id': case_id, 'judge_ref': str(ref), 'case_ref': case_ref,
                            'reason': str(exc)[:160]})
            continue
        question_type = str(case.get('question_type') or '').strip()
        difficulty = str(case.get('difficulty') or '').strip()
        if not question_type or not difficulty:
            skipped.append({'case_id': case_id, 'judge_ref': str(ref), 'case_ref': case_ref,
                            'reason': 'missing_stratification_metadata'})
            continue
        stratum = f'{question_type}:{difficulty}'
        groups.setdefault(stratum, []).append({'case_id': case_id, 'judge_ref': str(ref), 'case_ref': case_ref,
                                               'stratum': stratum})
    selected: list[dict[str, str]] = []
    for stratum in sorted(groups):
        for row in sorted(groups[stratum], key=lambda item: item['case_id']):
            selected.append(row)
            break
        if len(selected) >= cap:
            break
    return selected, skipped


def _guard_meta(ctx: OperationContext, goodcase_count: int, badcase_count: int) -> dict[str, Any]:
    value = float(ctx.params.get('goodcase_guard_ratio', 0.5))
    ratio = max(0.0, min(1.0, value / 100 if value > 1 else value))
    sample_cap = min(int(goodcase_count * ratio), badcase_count)
    seed_text = str(ctx.params.get('random_seed') or f'{ctx.run_id}:{ctx.params.get("classification_report_ref")}')
    seed = int(hashlib.sha256(seed_text.encode()).hexdigest()[:16], 16)
    sample_size = int(np.random.default_rng(seed).binomial(sample_cap, ratio))
    return {'goodcase_pool_size': goodcase_count, 'target_badcase_count': badcase_count,
            'distribution': 'binomial', 'guard_ratio': ratio, 'sample_cap': sample_cap, 'sample_size': sample_size}


def _baseline(ctx: OperationContext) -> dict[str, Any]:
    ref = str(ctx.params.get('verified_repair_ref') or '').strip()
    if not ref:
        return {'mode': 'original', 'source_ref': '', 'code_base_ref': '', 'metric_baseline_ref': '',
                'metric_baseline': {}}
    verified = typed_payload(ctx, ArtifactRef.parse(ref), 'VerifiedRepair')
    evaluation = typed_payload(ctx, ArtifactRef.parse(str(verified.get('winning_evaluation_ref') or '')),
                               'RepairEvaluation')
    workspace = str(verified.get('candidate_workspace_ref') or '')
    if verified.get('status') != 'ready_for_review' or evaluation.get('status') != 'passed' or (
        workspace and not Path(workspace).exists()
    ):
        raise ValueError(f'invalid VerifiedRepair baseline: {ref}')
    snapshot = verified.get('metric_after_snapshot') if isinstance(verified.get('metric_after_snapshot'), dict) else {}
    if not snapshot:
        raise ValueError(f'VerifiedRepair has no metric baseline snapshot: {ref}')
    return {'mode': 'verified_repair', 'source_ref': ref, 'code_base_ref': workspace,
            'metric_baseline_ref': str(verified.get('winning_evaluation_ref') or ''), 'metric_baseline': snapshot}


def _baseline_ref(plan: dict[str, Any], case_id: str) -> str:
    for bucket, ids_key, key in (
        (plan['target'], 'badcase_ids', 'baseline_judge_refs'),
        (plan.get('heldout') or {}, 'sibling_badcase_ids', 'sibling_badcase_judge_refs'),
        (plan.get('heldout') or {}, 'stratified_goodcase_ids', 'stratified_goodcase_judge_refs'),
        (plan['guard'], 'goodcase_ids', 'goodcase_judge_refs'),
    ):
        ids = list(bucket.get(ids_key) or bucket.get('badcase_ids') or bucket.get('goodcase_ids') or [])
        refs = list(bucket.get(key) or [])
        if not ids:
            continue
        if len(refs) != len(ids):
            raise ValueError(f'repair loop plan baseline refs mismatch: {ids_key}/{key}')
        if case_id in ids:
            return refs[ids.index(case_id)]
    raise ValueError(f'case is not in repair loop plan: {case_id}')


def _mechanism_progress(trace_deltas: list[dict[str, Any]]) -> str:
    kinds = {str(item.get('delta') or '') for item in trace_deltas if isinstance(item, dict)}
    if kinds & MECHANISM_PROGRESS_KINDS and 'new_failure' not in kinds:
        return 'improved'
    if kinds and kinds <= {'none'}:
        return 'unchanged'
    return 'inconclusive'


def _mechanism_gate_failed(plan: dict[str, Any], attempt: int, bad: list[dict[str, Any]],
                           trace_delta: list[dict[str, Any]], primary: str,
                           delta: float) -> tuple[dict[str, Any], dict[str, Any], list[Any]]:
    failure = (f'mechanism_unchanged: failing trace transition is unchanged and {primary} mean delta '
               f'{delta:+.4f} did not improve; try a different repair mechanism at the anchor')
    limit = float((plan.get('policy') or {}).get('goodcase_regression_ratio_limit', 0.34))
    guard = {'passed': False, 'skipped': True,
             'summary': {'mode': 'not_run', 'case_count': 0, 'regressed_case_count': 0,
                         'regression_ratio': 0.0, 'allowed_regression_ratio': limit},
             'cases': [], 'failure': failure}
    overall = {**_overall(bad, primary, float((plan.get('policy') or {}).get('target_mean_delta', 0.02))),
               'passed': False, 'failure': failure}
    case_failures = [row['case_failure'] for row in bad if isinstance(row.get('case_failure'), dict)]
    evaluation = {'id': _aid('repair_evaluation', attempt), 'attempt': attempt, 'status': 'failed',
                  'overall_eval': overall, 'badcase_eval': _bad(bad, primary),
                  'goodcase_impact': guard, 'goodcase_guard': guard,
                  'metric_delta_by_case': {str(row.get('case_id') or ''): row.get('delta') or {}
                                           for row in bad if row.get('case_id')},
                  'trace_delta_by_case': trace_delta,
                  'target_success_cases': _case_ids(bad, {'improved'}),
                  'target_unchanged_cases': _case_ids(bad, {'unchanged'}),
                  'target_regressed_cases': _case_ids(bad, {'regressed', 'failed'}),
                  'guard_regressed_cases': [],
                  'heldout_eval': {'enabled': False, 'sibling_badcase_ids': [], 'stratified_goodcase_ids': [],
                                   'passed': True, 'summary': {'reason': 'mechanism_gate'}},
                  'candidate_execution_failures': _candidate_execution_failures(bad),
                  'verification_command_failures': [],
                  'evaluation_confidence': 'low' if case_failures else 'high',
                  'evaluation_error': {'stage': 'mechanism_gate', 'message': failure, 'recoverable': True},
                  'case_failures': case_failures,
                  'candidate_classification_report_ref': f"{_aid('candidate_classification_report', attempt)}@v1",
                  'command_results': [], 'failure_summary': failure, 'next_attempt_guidance': failure}
    return evaluation, _candidate_report(attempt, [], failure, trace_delta), []


def _overall(rows: list[dict[str, Any]], primary: str, target: float, *,
             mechanism_progress: bool = False) -> dict[str, Any]:
    before, after = _avg(row['before'][primary] for row in rows), _avg(row['after'][primary] for row in rows)
    delta, failed = round(after - before, 4), sum(row['outcome'] == 'failed' for row in rows)
    answer_delta = round(_avg(row['after']['answer_correctness'] for row in rows)
                         - _avg(row['before']['answer_correctness'] for row in rows), 4)
    failure = 'candidate execution failed' if failed else ''
    # A patch that demonstrably fixes the failing mechanism passes with a non-regressing primary
    # metric; without mechanism evidence the full target delta is required.
    if not failure and delta < target and not (mechanism_progress and delta >= 0):
        failure = 'overall mean did not improve enough'
    if not failure and primary != 'answer_correctness' and answer_delta < -ANSWER_GUARDRAIL_TOLERANCE:
        failure = 'answer_correctness guardrail regressed'
    return {'passed': not failure,
            'summary': {'primary_metric': primary, 'case_count': len(rows), 'before_mean': before,
                        'after_mean': after, 'delta_mean': delta, 'required_delta_mean': target,
                        'failed_case_count': failed, 'mechanism_progress': mechanism_progress,
                        'answer_guardrail_delta': answer_delta},
            'failure': failure}


def _bad(rows: list[dict[str, Any]], primary: str) -> dict[str, Any]:
    delta = {metric: round(_avg(row['after'][metric] for row in rows) - _avg(row['before'][metric] for row in rows), 4)
             for metric in METRICS}
    counts = {outcome: sum(row['outcome'] == outcome for row in rows)
              for outcome in ('improved', 'unchanged', 'regressed')}
    return {'passed': True,
            'summary': {'primary_metric': primary, 'before_mean': _avg(row['before'][primary] for row in rows),
                        'after_mean': _avg(row['after'][primary] for row in rows), 'delta_mean': delta[primary],
                        'guard_delta_mean': delta,
                        **{f'{outcome}_case_count': count for outcome, count in counts.items()}},
            'case_outcomes': rows, 'failure': ''}


def _trace_delta(ctx: OperationContext, plan: dict[str, Any], rows: list[dict[str, Any]]) -> list[dict[str, Any]]:
    out = []
    for row in rows:
        try:
            case = typed_payload(ctx, ArtifactRef.parse(str(row.get('case_ref') or '')), 'DatasetCase')
            before_ref = ArtifactRef.parse(_baseline_ref(plan, str(row.get('case_id') or '')))
            before_judge = typed_payload(ctx, before_ref, 'JudgeResult')
            before_rag = typed_payload(ctx, ArtifactRef.parse(str(before_judge.get('rag_answer_ref') or '')),
                                       'RagAnswer')
            before = analyzer_stage_hits(ctx, case, before_rag, before_judge, {
                'case_id': row.get('case_id'),
                'fine_category': (plan.get('target') or {}).get('fine_category', ''),
                'evidence': {'fine_rule_hits': []},
            })
            after = analyzer_stage_hits(ctx, case, row.get('candidate_rag_answer') or {},
                                        row.get('candidate_judge_result') or {}, {
                'case_id': row.get('case_id'),
                'fine_category': (plan.get('target') or {}).get('fine_category', ''),
                'evidence': {'fine_rule_hits': []},
            })
            unknown_reason = _trace_delta_unknown_reason(before, after)
            out.append({'case_id': row.get('case_id', ''), 'before': _trace_delta_summary(before),
                        'after': _trace_delta_summary(after),
                        'delta': 'unknown' if unknown_reason else _trace_delta_kind(before, after),
                        **({'error': unknown_reason} if unknown_reason else {})})
        except Exception as exc:
            out.append({'case_id': row.get('case_id', ''), 'before': {}, 'after': {},
                        'delta': 'unknown', 'error': str(exc)[:200]})
    return out


def _trace_delta_unknown_reason(before: dict[str, Any], after: dict[str, Any]) -> str:
    for label, payload in (('before', before), ('after', after)):
        for item in payload.get('unknowns') or []:
            if isinstance(item, dict) and item.get('kind') == 'trace_read_error':
                return f'{label} trace read error: {item.get("reason", "")}'[:200]
        if str(payload.get('confidence') or '') == 'low' and payload.get('unknowns'):
            reason = '; '.join(str(item.get('reason') or item.get('kind') or '')
                               for item in payload.get('unknowns') if isinstance(item, dict))
            return f'{label} stage confidence low: {reason}'[:200]
    return ''


def _trace_delta_summary(stage_hits: dict[str, Any]) -> dict[str, Any]:
    aggregate = stage_hits.get('aggregate') if isinstance(stage_hits.get('aggregate'), dict) else {}
    return {'primary_transition_failure': stage_hits.get('primary_transition_failure'),
            **{key: aggregate.get(key) or {} for key in (
                'retriever_doc', 'retriever_chunk', 'merge_doc', 'merge_chunk', 'rerank_input_chunk',
                'rerank_output_chunk', 'final_doc', 'final_chunk')}}


def _trace_delta_kind(before: dict[str, Any], after: dict[str, Any]) -> str:
    before_failure = str(before.get('primary_transition_failure') or 'none')
    after_failure = str(after.get('primary_transition_failure') or 'none')
    if before_failure != 'none' and after_failure == 'none':
        return 'fixed_transition_failure'
    if before_failure == after_failure:
        return 'none'
    order = ['no_kb_search', 'retriever_miss', 'retriever_to_merge_drop', 'merge_to_rerank_input_drop',
             'rerank_drop', 'rerank_output_to_final_context_drop', 'generation_missed_available_context', 'none']
    if before_failure in order and after_failure in order:
        return 'moved_later' if order.index(after_failure) > order.index(before_failure) else 'new_failure'
    return 'partial'


def _guard(rows: list[dict[str, Any]], limit: float, mode: str) -> dict[str, Any]:
    if not rows:
        # A dataset without any goodcase (or an explicitly disabled guard) has nothing to regress
        # against: badcase eval, heldout eval and verification commands remain the gate. Only a
        # sampled guard set that unexpectedly came back empty still fails the evaluation.
        degraded = mode in {'no_goodcase', 'disabled'}
        return {'passed': degraded, 'skipped': True,
                'summary': {'mode': mode, 'case_count': 0, 'regressed_case_count': 0,
                            'regression_ratio': 0.0, 'allowed_regression_ratio': limit},
                'cases': [],
                'failure': '' if degraded else 'insufficient_guard_set: no goodcase guard cases available'}
    cases = [{**row, 'regressed': row['outcome'] in {'regressed', 'failed'}} for row in rows]
    regressed = sum(row['regressed'] for row in cases)
    ratio = round(regressed / len(cases), 4)
    return {'passed': ratio <= limit,
            'summary': {'mode': mode, 'case_count': len(cases), 'regressed_case_count': regressed,
                        'regression_ratio': ratio, 'allowed_regression_ratio': limit},
            'cases': cases, 'failure': '' if ratio <= limit else 'sampled goodcase regression ratio exceeded budget'}


def _incomplete(attempt: int, reason: str, *, stage: str = 'preflight') -> dict[str, Any]:
    guard = {'passed': False, 'cases': [], 'summary': {}, 'failure': reason}
    return {'id': _aid('repair_evaluation', attempt), 'attempt': attempt, 'status': 'incomplete',
            'overall_eval': {'passed': False, 'summary': {}, 'failure': reason},
            'badcase_eval': {'passed': False, 'summary': {}, 'case_outcomes': [], 'failure': reason},
            'goodcase_impact': guard, 'goodcase_guard': guard, 'metric_delta_by_case': {},
            'trace_delta_by_case': [], 'target_success_cases': [], 'target_unchanged_cases': [],
            'target_regressed_cases': [], 'guard_regressed_cases': [],
            'heldout_eval': {'enabled': False, 'sibling_badcase_ids': [], 'stratified_goodcase_ids': [],
                             'passed': True, 'summary': {'reason': stage}},
            'candidate_execution_failures': [], 'verification_command_failures': [],
            'evaluation_confidence': 'low',
            'evaluation_error': {'stage': stage, 'message': reason,
                                 'recoverable': stage == 'candidate_service_startup'},
            'case_failures': [],
            'candidate_classification_report_ref': f"{_aid('candidate_classification_report', attempt)}@v1",
            'command_results': [], 'failure_summary': reason, 'next_attempt_guidance': reason}


def _candidate_report(attempt: int, rows: list[dict[str, Any]], failure: str,
                      trace_delta: list[dict[str, Any]] | None = None) -> dict[str, Any]:
    status = 'not_started' if failure and not rows else 'failed' if failure else 'completed'
    return {'id': _aid('candidate_classification_report', attempt), 'attempt': attempt,
            'status': status, 'case_count': len(rows), 'cases': rows,
            'transition_delta_by_case': trace_delta or [], 'summary': failure}


def _candidate_focus(rows, bad) -> list[dict[str, Any]]:
    bad_ids = {id(row) for row in bad}
    focus = [row for row in rows if row['outcome'] in {'regressed', 'failed'}
             or id(row) in bad_ids and row['outcome'] == 'unchanged']
    improved = next((row for row in bad if row.get('outcome') == 'improved'), None)
    if improved and id(improved) not in {id(row) for row in focus}:
        focus.append(improved)
    return focus


def _zero_metrics() -> dict[str, float]:
    return {metric: 0.0 for metric in METRICS}


def _case_ids(rows: list[dict[str, Any]], outcomes: set[str]) -> list[str]:
    return [str(row.get('case_id') or '') for row in rows if row.get('outcome') in outcomes and row.get('case_id')]


def _heldout_eval(heldout: dict[str, Any], sibling_rows: list[dict[str, Any]],
                  good_rows: list[dict[str, Any]]) -> dict[str, Any]:
    enabled = bool(heldout.get('enabled')) and bool(sibling_rows or good_rows)
    sibling_failed = _case_ids(sibling_rows, {'unchanged', 'regressed', 'failed'})
    good_regressed = _case_ids(good_rows, {'regressed', 'failed'})
    passed = not (sibling_failed or good_regressed)
    return {'enabled': enabled,
            'sibling_badcase_ids': [str(row.get('case_id') or '') for row in sibling_rows if row.get('case_id')],
            'stratified_goodcase_ids': [str(row.get('case_id') or '') for row in good_rows if row.get('case_id')],
            'passed': True if not enabled else passed,
            'summary': {'sibling_badcase_count': len(sibling_rows),
                        'sibling_badcase_not_improved_ids': sibling_failed,
                        'stratified_goodcase_count': len(good_rows),
                        'stratified_goodcase_regressed_ids': good_regressed}}


def _candidate_execution_failures(rows: list[dict[str, Any]]) -> list[dict[str, Any]]:
    out = []
    for row in rows:
        result = row.get('candidate_judge_result') if isinstance(row.get('candidate_judge_result'), dict) else {}
        failure = row.get('case_failure') if isinstance(row.get('case_failure'), dict) else {}
        stage = str(failure.get('stage') or 'candidate_chat')
        if result.get('failure_type') == 'candidate_execution_failed' and stage == 'candidate_chat':
            out.append({'case_id': row.get('case_id', ''), 'stage': 'candidate_chat',
                        'message': result.get('reason', '')})
    return out


def _evaluation_error(case_failures: list[dict[str, Any]], command_failures: list[dict[str, Any]],
                      trace_delta: list[dict[str, Any]]) -> dict[str, Any]:
    if case_failures:
        first = case_failures[0]
        return {'stage': first.get('stage', 'case_eval'), 'message': first.get('message', ''),
                'recoverable': bool(first.get('recoverable', True))}
    if command_failures:
        return {'stage': 'verification_command', 'message': command_failures[0].get('failure', ''),
                'recoverable': True}
    if any(item.get('delta') == 'unknown' for item in trace_delta):
        return {'stage': 'trace_delta', 'message': 'trace delta extraction incomplete', 'recoverable': True}
    return {'stage': '', 'message': '', 'recoverable': True}


def _scope_check(changed: list[str], created: list[str], change: dict[str, Any]) -> dict[str, Any]:
    allowed, blocked = list(change.get('allowed_roots') or []), list(change.get('blocked_roots') or [])
    outside = [path for path in changed if not _path_in(path, allowed)]
    blocked_hits = [path for path in changed if _path_in(path, blocked)]
    new_hits = created if created and not change.get('allow_new_files', True) else []
    failure = next((label for items, label in (
        (blocked_hits, 'blocked_files'), (outside, 'outside_allowed_roots'), (new_hits, 'new_files_not_allowed'),
    ) if items), '')
    return {'status': 'passed' if not failure else 'failed',
            'unexpected_files': sorted(set(outside + blocked_hits + new_hits)),
            'allow_new_files': bool(change.get('allow_new_files', True)), 'failure': failure}


def _repair_scope(ctx: OperationContext) -> dict[str, Any]:
    raw = ctx.params.get('repair_scope') if isinstance(ctx.params.get('repair_scope'), dict) else {}
    scope = default_repair_scope() | raw
    return {'allowed_roots': [item.rstrip('/') for item in _norm_paths(scope.get('allowed_roots'))],
            'seed_files': _norm_paths(scope.get('seed_files')),
            'blocked_roots': [item.rstrip('/') for item in _norm_paths(scope.get('blocked_roots'))],
            'allow_new_files': bool(scope.get('allow_new_files', True))}


def _decision(evaluation: dict[str, Any], attempt: int, correctness: dict[str, Any] | None = None,
              branch_decision: dict[str, Any] | None = None) -> dict[str, Any]:
    verdict = str((correctness or {}).get('verdict') or '')
    branch = str((branch_decision or {}).get('decision') or '')
    mapped = ('passed' if branch == 'accept_verified'
              or (not branch_decision and evaluation['status'] == 'passed' and verdict == 'acceptable')
              else 'failed' if branch == 'stop_failed' else 'continue')
    return {'id': _aid('repair_loop_decision', attempt), 'attempt': attempt, 'decision': mapped,
            'branch_decision_ref': f"{branch_decision.get('id')}@v1" if branch_decision else '',
            'branch_decision': branch,
            'reason': (str(evaluation.get('failure_summary') or '') if evaluation.get('failure_summary')
                       else f'patch correctness verdict is {verdict or "missing"}'
                       if evaluation.get('status') == 'passed' and verdict != 'acceptable'
                       else 'patch passed scope, correctness, and verification'),
            'next_attempt': attempt + (0 if mapped in {'passed', 'failed'} else 1),
            'blocking_failures': [] if mapped == 'passed' else [{'kind': 'repair_verification_failed',
                                                                 'status': evaluation['status'],
                                                                 'correctness_verdict': verdict,
                                                                 'branch_decision': branch}]}


def _memory(hypothesis, patch, evaluation, report, attempt, critique=None, branch_decision=None,
            branch_state=None, probe_worker_report=None, prev_memory=None) -> dict[str, Any]:
    failed = [] if evaluation['status'] == 'passed' else [{
        'attempt': attempt, 'reason': evaluation.get('failure_summary', ''),
        'avoid': 'repeat same edit without new evidence',
    }]
    fingerprints = [str(item) for item in (prev_memory or {}).get('failed_patch_fingerprints') or []]
    if evaluation['status'] != 'passed' and str(patch.get('fingerprint') or ''):
        fingerprints = [*[item for item in fingerprints if item != patch['fingerprint']],
                        str(patch['fingerprint'])][-20:]
    anchor_lock, released_anchors = _anchor_lock_update(prev_memory or {}, patch, evaluation)
    memory_update = (critique or {}).get('memory_update') if isinstance((critique or {}).get('memory_update'),
                                                                        dict) else {}
    state = branch_state or {}
    best = state.get('best_baseline') if isinstance(state.get('best_baseline'), dict) else {}
    active = state.get('active_branch') if isinstance(state.get('active_branch'), dict) else {}
    return {'id': _aid('repair_loop_memory', attempt),
            'supported_directions': hypothesis.get('supported_directions', []),
            'rejected_directions': hypothesis.get('rejected_directions', []),
            'trace_steps_read': hypothesis.get('trace_steps_read', []),
            'source_files_read': hypothesis.get('source_files_read', []),
            'failed_patch_summaries': failed, 'failed_patch_fingerprints': fingerprints,
            'anchor_lock': anchor_lock, 'released_anchors': released_anchors,
            'candidate_failure_categories': candidate_failure_categories(report),
            'failure_library': [{'key': str(item), 'verdict': 'do_not_repeat',
                                 'observed_effect': evaluation.get('failure_summary', '')}
                                for item in memory_update.get('avoid_repeating') or []],
            'last_patch_critique_ref': f"{critique.get('id')}@v1" if critique else '',
            'last_branch_decision_ref': f"{branch_decision.get('id')}@v1" if branch_decision else '',
            'last_branch_state_ref': f"{branch_state.get('id')}@v1" if branch_state else '',
            'best_baseline': best, 'active_branch': active,
            'active_patch_lineage': active.get('patch_lineage') or [],
            'physical_root': (branch_state or {}).get('physical_root_ref') or '',
            'rejected_branches': (branch_state or {}).get('rejected_branches') or [],
            'invalidated_hypotheses': (branch_state or {}).get('abandoned_hypotheses') or [],
            # Worker exploration evidence feeds the next attempt's source ranking, so a
            # probe-rejected primary does not get re-selected and confirmed locations win.
            'worker_probe_evidence': {'confirmed_locations': (probe_worker_report or {}).get('confirmed_locations')
                                      or [],
                                      'rejected_locations': (probe_worker_report or {}).get('rejected_locations')
                                      or []},
            'next_focus': evaluation.get('next_attempt_guidance', '') or ((critique or {}).get('next_focus') or {})}


def _anchor_lock_update(prev: dict[str, Any], patch: dict[str, Any],
                        evaluation: dict[str, Any]) -> tuple[dict[str, Any], list[dict[str, str]]]:
    """Lock repair attempts onto the currently patched anchor: only edit ideas may change while
    no progress is observed; after ANCHOR_LOCK_RELEASE_AFTER fruitless attempts the anchor is
    released and demoted so localization moves elsewhere instead of drifting back and forth."""
    released = [dict(item) for item in prev.get('released_anchors') or [] if isinstance(item, dict)][-10:]
    anchor = next(({'path': str(hunk.get('path') or ''), 'symbol': str(hunk.get('symbol') or '')}
                   for hunk in patch.get('changed_hunks') or []
                   if isinstance(hunk, dict) and not hunk.get('comment_only') and hunk.get('path')), None)
    if evaluation.get('status') == 'passed' or not anchor:
        return dict(prev.get('anchor_lock') or {}), released
    kinds = {str(item.get('delta') or '') for item in evaluation.get('trace_delta_by_case') or []
             if isinstance(item, dict)}
    delta = float(((evaluation.get('overall_eval') or {}).get('summary') or {}).get('delta_mean') or 0.0)
    progressed = bool(kinds & MECHANISM_PROGRESS_KINDS) or delta > 0
    lock = prev.get('anchor_lock') if isinstance(prev.get('anchor_lock'), dict) else {}
    same = lock.get('path') == anchor['path'] and lock.get('symbol') == anchor['symbol']
    count = 0 if progressed else (int(lock.get('no_progress_count') or 0) + 1 if same else 1)
    if count >= ANCHOR_LOCK_RELEASE_AFTER:
        return {}, [*[item for item in released if item != anchor], dict(anchor)][-10:]
    return {**anchor, 'no_progress_count': count}, released


def _state(plan_ref, workspace, attempt, session, memory, patch, evaluation, decision,
           branch_state=None, transition=None) -> dict[str, Any]:
    return {'id': _aid('repair_loop_state', attempt), 'repair_loop_plan_ref': str(plan_ref), 'current_attempt': attempt,
            'opencode_session_id': session, 'candidate_workspace_ref': str(workspace),
            'last_memory_ref': f"{memory.get('id')}@v1", 'last_patch_ref': f"{patch.get('id')}@v1",
            'last_evaluation_ref': f"{evaluation.get('id')}@v1",
            'last_branch_state_ref': f"{branch_state.get('id')}@v1" if branch_state else '',
            'last_state_transition_ref': f"{transition.get('id')}@v1" if transition else '',
            'status': decision.get('decision', '')}


def _verified(vid, plan_ref, workspace, patch, evaluation, correctness, plan) -> dict[str, Any]:
    rows = (evaluation.get('badcase_eval') or {}).get('case_outcomes', [])
    rows += (evaluation.get('goodcase_impact') or {}).get('cases', [])
    return {'id': vid, 'repair_loop_plan_ref': str(plan_ref), 'winning_patch_ref': f"{patch['id']}@v1",
            'winning_evaluation_ref': f"{evaluation['id']}@v1", 'candidate_workspace_ref': str(workspace),
            'patch_correctness_assessment_ref': f"{correctness['id']}@v1",
            'baseline_mode': (plan.get('baseline') or {}).get('mode', 'original'),
            # The metric baseline is what the repair was measured against, not the winning result itself.
            'metric_baseline_ref': str((plan.get('baseline') or {}).get('metric_baseline_ref')
                                       or plan.get('eval_report_ref') or ''),
            'metric_after_snapshot': {row['case_id']: row.get('after', {}) for row in rows if row.get('case_id')},
            'status': 'ready_for_review',
            'summary': 'repair loop target reached; final acceptance is validated by downstream ABTest'}


def _latest_state(ctx: OperationContext, plan_ref: ArtifactRef) -> dict[str, Any]:
    latest: dict[str, Any] = {}
    for path in sorted(ctx.artifact_graph.manifest_dir.glob('repair_loop_state_attempt_*.json')):
        try:
            state = typed_payload(ctx, ctx.artifact_graph.latest_ref(path.stem), 'RepairLoopState')
        except Exception:
            continue
        if state.get('repair_loop_plan_ref') == str(plan_ref) and int(state.get('current_attempt') or 0) >= int(
            latest.get('current_attempt') or 0
        ):
            latest = state
    return latest


def _terminal_branch_decision(attempt: int, reason: str) -> dict[str, Any]:
    payload = {
        'id': _aid('branch_decision', attempt), 'attempt': attempt, 'decision': 'stop_failed', 'reason': reason,
        'next_base': {'workspace_ref': '', 'branch_id': 'branch_active', 'base_patch_ref': '', 'patch_lineage': []},
        'next_instruction_seed': {'focus_hypothesis_ids': [], 'abandoned_hypothesis_ids': [],
                                  'focus_case_ids': [], 'avoid': [], 'required_shift': 'stop'},
        'decision_inputs': {'metric_progress': 'execution_failed', 'trace_progress': 'unknown', 'diff_hit': False,
                            'goodcase_impact': 'unknown', 'correctness_verdict': 'needs_more_validation',
                            'worker_protocol_status': 'not_run', 'branch_prepare_status': 'failed',
                            'branch_prepare_failure': reason},
    }
    validate_repair_artifact('BranchDecision', payload)
    return payload


def _run_command(ctx: OperationContext, command: Any, attempt: int, workspace_ref: Any, *,
                 index: int | None = None) -> dict[str, Any]:
    workspace = Path(str(workspace_ref or ctx.params.get('candidate_workdir') or ctx.draft_dir))
    raw_token = repr(command)
    token = str(index) if index is not None else (
        hashlib.sha256(raw_token.encode()).hexdigest()[:8] + '_' + uuid4().hex[:8]
    )
    path = workspace / '.evo_repair_logs' / f'test_log_attempt_{attempt}_{token}.log'
    path.parent.mkdir(parents=True, exist_ok=True)

    def fail(argv: list[str], kind: str, exit_code: int, failure: str, log_text: str) -> dict[str, Any]:
        path.write_text(log_text, encoding='utf-8')
        return {'command': argv, 'status': 'failed', 'type': kind, 'exit_code': exit_code,
                'failure': failure, 'log_path': str(path)}

    try:
        argv = command_args(command)
    except ValueError as exc:
        return fail([], 'invalid_command', 127, str(exc), str(exc))
    if not argv:
        return fail(argv, 'empty_command', 127, 'empty verification command', 'empty verification command')
    try:
        timeout_s = int(ctx.params.get('verification_timeout_s', 600))
        if timeout_s <= 0:
            raise ValueError('verification_timeout_s must be positive')
    except (TypeError, ValueError) as exc:
        return fail(argv, 'invalid_timeout', 124, str(exc), str(exc))
    try:
        result = subprocess.run(argv, cwd=str(workspace), capture_output=True, text=True,
                                timeout=timeout_s, check=False)
    except FileNotFoundError as exc:
        return fail(argv, 'command_not_found', 127, str(exc), str(exc))
    except PermissionError as exc:
        return fail(argv, 'permission_denied', 126, str(exc), str(exc))
    except subprocess.TimeoutExpired as exc:
        stdout = exc.stdout.decode() if isinstance(exc.stdout, bytes) else (exc.stdout or '')
        stderr = exc.stderr.decode() if isinstance(exc.stderr, bytes) else (exc.stderr or '')
        return fail(argv, 'timeout', -1, f'verification command timed out after {exc.timeout}s', stdout + stderr)
    except OSError as exc:
        return fail(argv, 'command_launch_failed', 126, str(exc), str(exc))
    except Exception as exc:
        return fail(argv, 'command_launch_failed', 126, str(exc), str(exc))
    path.write_text((result.stdout or '') + (result.stderr or ''), encoding='utf-8')
    status = 'passed' if result.returncode == 0 else 'failed'
    return {'command': argv, 'status': status, 'type': '' if status == 'passed' else 'nonzero_exit',
            'exit_code': result.returncode,
            'failure': '' if status == 'passed' else f'verification command exited {result.returncode}',
            'log_path': str(path)}


def _workspace(ctx: OperationContext, plan: dict[str, Any]) -> Path:
    raw = str((plan.get('baseline') or {}).get('code_base_ref') or ctx.params.get('candidate_workdir') or '').strip()
    if not raw:
        raw = next((str(ctx.artifact_graph.get(ref).get('workspace_ref') or '') for ref in ctx.input_refs
                    if ctx.artifact_graph.schema_name(ref) == 'CandidateWorkspace'), '')
    workspace = Path(raw or ctx.draft_dir / 'candidate').resolve()
    if (plan.get('baseline') or {}).get('code_base_ref') and not workspace.exists():
        raise ValueError(f'verified repair workspace does not exist: {workspace}')
    workspace.mkdir(parents=True, exist_ok=True)
    return workspace


def _draft(artifact_id, schema, payload, ctx, refs) -> ArtifactDraft:
    return ArtifactDraft(validate_id(artifact_id, 'artifact_id'), schema, payload, ctx.operation_run_id,
                         input_refs=refs)


def _aid(prefix: str, attempt: int) -> str:
    return f'{prefix}_attempt_{attempt}'


def _norm_paths(items: Any) -> list[str]:
    out = []
    for item in items or []:
        rel = _rel_path(str(item))
        if rel and rel not in out:
            out.append(rel)
    return out


def _rel_path(path: str) -> str:
    raw, rel = path.strip(), Path(path.strip()).as_posix()
    return '' if Path(raw).is_absolute() or not rel or rel == '.' or rel.startswith('../') or '/../' in rel else rel


def _path_in(path: str, roots: list[str]) -> bool:
    return any(path == root or path.startswith(f'{root}/') for root in roots)


def _git(workspace: Path, args: list[str]) -> str:
    result = subprocess.run(['git', '-c', f'safe.directory={workspace}', '-C', str(workspace), *args],
                            capture_output=True, text=True, check=False)
    return result.stdout if result.returncode == 0 else ''


def _git_status(workspace, ignored_roots=(), ignored_files=None) -> tuple[list[str], list[str], list[str]]:
    buckets: tuple[list[str], list[str], list[str]] = ([], [], [])
    for line in _git(workspace, ['status', '--porcelain', '--untracked-files=all']).splitlines():
        code, path = line[:2], _rel_path(line[3:].split(' -> ')[-1]) if len(line) >= 4 else ''
        ignored = (not path or path in (ignored_files or set()) or path.endswith('.pyc')
                   or '__pycache__' in Path(path).parts or _path_in(path, list(ignored_roots)))
        if ignored:
            continue
        buckets[0].append(path)
        buckets[1].extend([path] if code == '??' or 'A' in code else [])
        buckets[2].extend([path] if 'D' in code else [])
    return tuple(sorted(set(items)) for items in buckets)


def _restore_diff_since(workspace: Path, before: tuple[list[str], list[str], list[str]], ignored_roots=(),
                        ignored_files=None) -> None:
    before_changed = set(before[0])
    changed, created, deleted = _git_status(workspace, ignored_roots, ignored_files)
    new_changed = sorted(set(changed) - before_changed)
    if not new_changed:
        return
    created_new = [path for path in created if path in new_changed]
    deleted_new = [path for path in deleted if path in new_changed]
    tracked = [path for path in new_changed if path not in set(created_new + deleted_new)]
    if tracked:
        _git(workspace, ['restore', '--staged', '--worktree', '--', *tracked])
    if created_new:
        subprocess.run(
            ['git', '-c', f'safe.directory={workspace}', '-C', str(workspace), 'clean', '-f', '--', *created_new],
            capture_output=True, text=True, check=False,
        )
    if deleted_new:
        _git(workspace, ['restore', '--staged', '--worktree', '--', *deleted_new])


def _avg(values: Any) -> float:
    nums = [score(value) for value in values]
    return round(sum(nums) / len(nums), 4) if nums else 0.0
