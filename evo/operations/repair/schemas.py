from __future__ import annotations

from pathlib import PurePosixPath
from typing import Any

from ...artifacts import ArtifactRef

TRANSITION_FAILURE_KINDS = {
    'none', 'no_kb_search', 'retriever_miss', 'retriever_to_merge_drop', 'merge_to_rerank_input_drop',
    'rerank_drop', 'rerank_output_to_final_context_drop', 'tool_execution_error', 'tool_result_to_answer_drop',
    'generation_missed_available_context',
}
TRACE_DELTA_KINDS = {'fixed_transition_failure', 'moved_later', 'new_failure', 'none', 'partial', 'unknown'}
INSTRUCTION_MODES = {'explore_only', 'patch_once', 'no_patch'}
WORKER_PROTOCOL_STATUSES = {'valid', 'invalid', 'missing', 'not_run'}
PATCH_CORRECTNESS_VERDICTS = {'acceptable', 'needs_more_validation', 'reject'}
BRANCH_DECISIONS = {'accept_verified', 'promote_to_best', 'continue_current_branch', 'fix_current_patch',
                    'fork_from_best', 'abandon_direction', 'stop_failed'}
WORKER_REPORT_REQUIRED_FIELDS = ('attempt', 'mode', 'protocol_status', 'hypothesis_checked', 'confirmed_locations',
                                 'rejected_locations', 'edit_intent', 'files_changed', 'touched_symbols',
                                 'local_validation', 'stop_reason', 'remaining_uncertainty')
WORKER_REPORT_EDIT_INTENT_FIELDS = ('target_symbol', 'intended_behavior_change', 'risk_acknowledgement')
WORKER_REPORT_LOCATION_SCHEMA = {'path': 'string', 'symbol': 'string', 'line_start': 'integer',
                                 'line_end': 'integer', 'evidence': 'string'}


def validate_repair_evidence_packet(payload: dict[str, Any]) -> None:
    _require(payload, 'id', 'attempt')
    for obs in _list(payload.get('trace_observations')):
        _transition(obs.get('primary_transition_failure'))
        aggregate = obs.get('aggregate') if isinstance(obs.get('aggregate'), dict) else {}
        for key in ('retriever_doc', 'retriever_chunk', 'merge_doc', 'merge_chunk', 'rerank_input_doc',
                    'rerank_input_chunk', 'rerank_output_doc', 'rerank_output_chunk', 'final_doc', 'final_chunk'):
            _stage_hit(aggregate.get(key), f'aggregate.{key}')
    for obs in _list(payload.get('source_observations')):
        _source_observation(obs, _allowed_roots(payload))


def validate_fault_localization_report(payload: dict[str, Any]) -> None:
    _require(payload, 'id', 'attempt', 'ranked_locations')
    for index, item in enumerate(_list(payload.get('ranked_locations')), start=1):
        _require(item, 'rank', 'source_observation_ref', 'path', 'symbol', 'score', 'confidence')
        if int(item.get('rank') or 0) != index:
            raise ValueError('FaultLocalizationReport ranks must be contiguous')
        _relative_path(str(item.get('path') or ''))


def validate_diagnostic_probe_plan(payload: dict[str, Any]) -> None:
    _require(payload, 'id', 'attempt', 'mode', 'probes', 'no_edit')
    if payload.get('mode') != 'explore_only' or payload.get('no_edit') is not True:
        raise ValueError('DiagnosticProbePlan must be explore_only and no_edit')
    for probe in _list(payload.get('probes')):
        if {str(item) for item in _list(probe.get('allowed_actions'))} & {'edit', 'write'}:
            raise ValueError('DiagnosticProbePlan cannot allow edit/write')


def validate_diagnostic_probe_result(payload: dict[str, Any]) -> None:
    _require(payload, 'id', 'attempt', 'probe_results', 'protocol_status', 'origin')
    if payload.get('protocol_status') not in {'valid', 'invalid'}:
        raise ValueError('invalid DiagnosticProbeResult protocol_status')
    origin = str(payload.get('origin') or '')
    worker_ref, trace_ref = str(payload.get('worker_report_ref') or ''), str(payload.get('raw_trace_ref') or '')
    if origin == 'local_candidate':
        if worker_ref or trace_ref:
            raise ValueError('local_candidate DiagnosticProbeResult cannot carry worker refs')
    elif origin == 'opencode_probe_worker':
        _artifact_ref(worker_ref, 'worker_report_ref')
        _artifact_ref(trace_ref, 'raw_trace_ref')
        if not any(isinstance(it, dict) and 'candidate_path' not in it for it in _list(payload.get('probe_results'))):
            raise ValueError('opencode_probe_worker DiagnosticProbeResult requires worker probe evidence')
    else:
        raise ValueError('invalid DiagnosticProbeResult origin')
    for item in _list(payload.get('probe_results')):
        if item.get('status') not in {'confirmed', 'rejected', 'inconclusive', 'failed'}:
            raise ValueError('invalid probe result status')
        if origin == 'local_candidate' and item.get('status') == 'confirmed':
            raise ValueError('local_candidate DiagnosticProbeResult cannot confirm a location')
        if origin == 'local_candidate' and {'path', 'confirmed_symbol', 'line_start', 'line_end'} & set(item):
            raise ValueError('local_candidate DiagnosticProbeResult must use candidate location fields')
        if item.get('status') == 'confirmed':
            _require(item, 'source_observation_ref', 'path', 'confirmed_symbol', 'line_start', 'line_end')
            _relative_path(str(item.get('path') or ''))


def validate_repair_diagnosis(payload: dict[str, Any]) -> None:
    _require(payload, 'id', 'attempt', 'suspected_code_locations', 'root_cause_hypotheses')
    for location in _list(payload.get('suspected_code_locations')):
        _relative_path(str(location.get('path') or ''))
    for hypothesis in _list(payload.get('root_cause_hypotheses')):
        if not _list(hypothesis.get('evidence_refs')):
            raise ValueError('RepairDiagnosis hypothesis requires evidence_refs')


def validate_opencode_instruction(payload: dict[str, Any]) -> None:
    _require(payload, 'id', 'attempt', 'diagnosis_ref', 'mode', 'allowed_tools', 'no_edit')
    mode = str(payload.get('mode') or '')
    if mode not in INSTRUCTION_MODES:
        raise ValueError(f'invalid OpenCodeInstruction mode: {mode}')
    tools = {str(tool) for tool in _list(payload.get('allowed_tools'))}
    if mode == 'explore_only':
        if payload.get('no_edit') is not True or tools & {'edit', 'write'}:
            raise ValueError('explore_only instruction cannot edit')
    elif mode == 'patch_once':
        if payload.get('no_edit') is not False or not str(payload.get('primary_source_observation_ref') or ''):
            raise ValueError('patch_once instruction requires primary source observation and no_edit=false')
        refs = {str(item.get('source_observation_ref') or '') for item in _list(payload.get('start_points'))}
        if payload.get('primary_source_observation_ref') not in refs:
            raise ValueError('patch_once primary_source_observation_ref must match start_points')
        if not _list(payload.get('linked_probe_result_refs')):
            raise ValueError('patch_once instruction requires linked_probe_result_refs')
    elif mode == 'no_patch':
        if payload.get('no_edit') is not True or tools:
            raise ValueError('no_patch instruction cannot allow tools')
    scope = payload.get('patch_contract') if isinstance(payload.get('patch_contract'), dict) else {}
    for path in _list(scope.get('allowed_roots')) + _list(scope.get('blocked_roots')):
        _relative_path(str(path))


def validate_opencode_worker_report(payload: dict[str, Any]) -> None:
    _require(payload, 'id', 'attempt', 'mode', 'protocol_status')
    if payload.get('mode') not in INSTRUCTION_MODES:
        raise ValueError('invalid OpenCodeWorkerReport mode')
    if payload.get('protocol_status') not in WORKER_PROTOCOL_STATUSES:
        raise ValueError('invalid OpenCodeWorkerReport protocol_status')
    if payload.get('protocol_status') == 'not_run' and payload.get('mode') != 'no_patch':
        raise ValueError('protocol_status=not_run is only valid for mode=no_patch')
    validate_worker_report_protocol_shape(payload)


def validate_code_patch_candidate(payload: dict[str, Any]) -> None:
    _require(payload, 'id', 'attempt', 'workspace_ref', 'base_git_head', 'diff_ref', 'apply_status',
             'reverse_apply_status')
    if 'post_patch_git_head' not in payload or 'changed_hunks' not in payload:
        raise ValueError('CodePatchCandidate requires post_patch_git_head and changed_hunks fields')
    if payload.get('apply_status') not in {'not_applied', 'applied', 'failed'}:
        raise ValueError('invalid CodePatchCandidate apply_status')
    if payload.get('reverse_apply_status') not in {'verified', 'failed', 'not_checked'}:
        raise ValueError('invalid CodePatchCandidate reverse_apply_status')
    if payload.get('apply_status') == 'applied':
        _require(payload, 'post_patch_git_head')
    for item in _list(payload.get('changed_hunks')):
        _require(item, 'path', 'line_start', 'line_end', 'symbol', 'comment_only')
        _relative_path(str(item.get('path') or ''))
        if int(item.get('line_start') or 0) <= 0:
            raise ValueError('invalid CodePatchCandidate changed_hunks line_start')
        if int(item.get('line_end') or 0) < int(item.get('line_start') or 0):
            raise ValueError('invalid CodePatchCandidate changed_hunks line_end')
        if not isinstance(item.get('comment_only'), bool):
            raise ValueError('CodePatchCandidate changed_hunks.comment_only must be boolean')


def validate_patch_correctness_assessment(payload: dict[str, Any]) -> None:
    _require(payload, 'id', 'attempt', 'verdict')
    verdict = str(payload.get('verdict') or '')
    if verdict not in PATCH_CORRECTNESS_VERDICTS:
        raise ValueError(f'invalid PatchCorrectnessAssessment verdict: {verdict}')
    if verdict != 'acceptable':
        return
    if any(isinstance(r, dict) and r.get('severity') == 'high' for r in _list(payload.get('overfitting_risks'))):
        raise ValueError('PatchCorrectnessAssessment cannot accept high severity overfitting risk')
    if any(isinstance(i, dict) and i.get('passed') is False for i in _list(payload.get('behavior_invariants'))):
        raise ValueError('PatchCorrectnessAssessment cannot accept failed behavior invariant')
    heldout = payload.get('heldout_eval') if isinstance(payload.get('heldout_eval'), dict) else {}
    if 'heldout_eval' in payload:
        if not isinstance(payload.get('heldout_eval'), dict):
            raise ValueError('PatchCorrectnessAssessment heldout_eval must be an object')
        if not isinstance(heldout.get('enabled'), bool):
            raise ValueError('PatchCorrectnessAssessment heldout.enabled must be boolean')
        if not isinstance(heldout.get('passed'), bool):
            raise ValueError('PatchCorrectnessAssessment heldout.passed must be boolean')
    if heldout.get('enabled') and heldout.get('passed') is not True:
        raise ValueError('PatchCorrectnessAssessment cannot accept failed heldout validation')


def validate_repair_evaluation(payload: dict[str, Any]) -> None:
    _require(payload, 'id', 'attempt', 'status')
    if payload.get('status') not in {'passed', 'failed', 'incomplete'}:
        raise ValueError('invalid RepairEvaluation status')
    if payload.get('evaluation_confidence') not in {'high', 'medium', 'low'}:
        raise ValueError('invalid RepairEvaluation evaluation_confidence')
    for item in _list(payload.get('trace_delta_by_case')):
        if str(item.get('delta') or '') not in TRACE_DELTA_KINDS:
            raise ValueError(f"invalid RepairEvaluation trace delta: {item.get('delta')}")
        for key in ('before', 'after'):
            stage = item.get(key) if isinstance(item.get(key), dict) else {}
            if stage:
                _transition(stage.get('primary_transition_failure'))


def validate_patch_critique(payload: dict[str, Any]) -> None:
    _require(payload, 'id', 'attempt')
    diff = payload.get('diff_assessment') if isinstance(payload.get('diff_assessment'), dict) else {}
    if 'matched_instruction' in diff and not isinstance(diff.get('matched_instruction'), bool):
        raise ValueError('PatchCritique.diff_assessment.matched_instruction must be boolean')


def validate_branch_decision(payload: dict[str, Any]) -> None:
    _require(payload, 'id', 'attempt', 'decision')
    if payload.get('decision') not in BRANCH_DECISIONS:
        raise ValueError('invalid BranchDecision decision')


def validate_repair_branch_state(payload: dict[str, Any]) -> None:
    _require(payload, 'id', 'attempt', 'workspace_ref', 'status')
    active = payload.get('active_branch') if isinstance(payload.get('active_branch'), dict) else {}
    best = payload.get('best_baseline') if isinstance(payload.get('best_baseline'), dict) else {}
    _require(active, 'branch_id', 'workspace_ref', 'base_commit', 'working_tree_status', 'base_kind')
    _require(best, 'workspace_ref', 'baseline_commit', 'immutable_snapshot_ref', 'reason')
    if active.get('working_tree_status') not in {'clean', 'dirty', 'unknown'}:
        raise ValueError('invalid RepairBranchState active_branch.working_tree_status')
    if active.get('base_kind') not in {'original', 'verified_repair', 'best_intermediate'}:
        raise ValueError('invalid RepairBranchState active_branch.base_kind')
    status = payload.get('workspace_status') if isinstance(payload.get('workspace_status'), dict) else {}
    if status.get('working_tree_status') not in {None, 'clean', 'dirty', 'unknown'}:
        raise ValueError('invalid RepairBranchState workspace_status.working_tree_status')


def validate_repair_state_transition(payload: dict[str, Any]) -> None:
    _require(payload, 'id', 'attempt', 'state_before_ref', 'decision_ref', 'state_after_ref')
    attempt = int(payload.get('attempt') or 0)
    for key in ('state_before_ref', 'state_after_ref'):
        if f'_attempt_{attempt}@v' not in str(payload.get(key) or ''):
            raise ValueError(f'RepairStateTransition {key} attempt mismatch')
    if f'branch_decision_attempt_{attempt}@v' not in str(payload.get('decision_ref') or ''):
        raise ValueError('RepairStateTransition decision_ref attempt mismatch')


VALIDATORS = {
    'RepairEvidencePacket': validate_repair_evidence_packet,
    'FaultLocalizationReport': validate_fault_localization_report,
    'DiagnosticProbePlan': validate_diagnostic_probe_plan,
    'DiagnosticProbeResult': validate_diagnostic_probe_result,
    'RepairDiagnosis': validate_repair_diagnosis,
    'OpenCodeInstruction': validate_opencode_instruction,
    'OpenCodeWorkerReport': validate_opencode_worker_report,
    'CodePatchCandidate': validate_code_patch_candidate,
    'PatchCorrectnessAssessment': validate_patch_correctness_assessment,
    'RepairEvaluation': validate_repair_evaluation,
    'PatchCritique': validate_patch_critique,
    'BranchDecision': validate_branch_decision,
    'RepairBranchState': validate_repair_branch_state,
    'RepairStateTransition': validate_repair_state_transition,
}


def validate_repair_artifact(schema_name: str, payload: dict[str, Any]) -> None:
    validator = VALIDATORS.get(schema_name)
    if validator:
        validator(payload)


def validate_patch_gate_contract(instruction: dict[str, Any], fault_report: dict[str, Any],
                                 probe_result: dict[str, Any]) -> None:
    if instruction.get('mode') != 'patch_once':
        return
    status = probe_gate_status(fault_report, probe_result, instruction)
    if not status.get('allowed'):
        raise ValueError(f"patch_once probe gate closed: {status.get('reason')}")


def validate_worker_report_protocol_shape(payload: dict[str, Any], expected_mode: str = '') -> None:
    errors = worker_report_protocol_shape_errors(payload, expected_mode)
    if errors:
        raise ValueError(f'OpenCodeWorkerReport {errors[0]}')


def worker_report_contract(mode: str) -> dict[str, Any]:
    return {
        'required_fields': list(WORKER_REPORT_REQUIRED_FIELDS),
        'mode': mode,
        'protocol_status': '|'.join(sorted(WORKER_PROTOCOL_STATUSES)),
        'confirmed_locations_item': dict(WORKER_REPORT_LOCATION_SCHEMA),
        'rejected_locations_item': dict(WORKER_REPORT_LOCATION_SCHEMA),
        'edit_intent_fields': list(WORKER_REPORT_EDIT_INTENT_FIELDS),
        'files_changed': 'must match git diff; empty for explore_only/no_patch',
    }


def worker_report_protocol_shape_errors(payload: dict[str, Any], expected_mode: str = '') -> list[str]:
    missing = [key for key in WORKER_REPORT_REQUIRED_FIELDS if key not in payload]
    if missing:
        return [f'missing required field: {missing[0]}']
    mode, status = str(payload.get('mode') or ''), str(payload.get('protocol_status') or '')
    if expected_mode and mode != expected_mode:
        return [f'mode mismatch: expected {expected_mode}']
    if mode not in INSTRUCTION_MODES:
        return ['invalid mode']
    if status not in WORKER_PROTOCOL_STATUSES:
        return ['invalid protocol_status']
    if mode == 'no_patch' and status != 'not_run':
        return ['no_patch report must have protocol_status=not_run']
    if status == 'not_run' and mode != 'no_patch':
        return ['protocol_status=not_run is only valid for mode=no_patch']
    for key in ('confirmed_locations', 'rejected_locations', 'files_changed', 'touched_symbols', 'local_validation'):
        if not isinstance(payload.get(key), list):
            return [f'{key} must be a list']
    if not isinstance(payload.get('edit_intent'), dict):
        return ['edit_intent must be an object']
    edit = payload.get('edit_intent') if isinstance(payload.get('edit_intent'), dict) else {}
    missing_edit = [key for key in WORKER_REPORT_EDIT_INTENT_FIELDS if key not in edit]
    if missing_edit:
        return [f'edit_intent missing required field: {missing_edit[0]}']
    if not isinstance(edit.get('risk_acknowledgement'), list):
        return ['edit_intent.risk_acknowledgement must be a list']
    for key in ('hypothesis_checked', 'stop_reason', 'remaining_uncertainty'):
        if not isinstance(payload.get(key), str):
            return [f'{key} must be a string']
    if status == 'valid':
        if not _worker_locations_are_structured(payload.get('confirmed_locations')):
            return ['confirmed_locations must be structured']
        if not _worker_locations_are_structured(payload.get('rejected_locations')):
            return ['rejected_locations must be structured']
    if status == 'valid' and mode == 'explore_only':
        if [str(item) for item in _list(payload.get('files_changed')) if str(item)]:
            return ['explore_only valid report cannot change files']
    if status == 'valid' and mode == 'patch_once':
        if not str(edit.get('target_symbol') or '').strip():
            return ['patch_once valid report requires edit_intent.target_symbol']
    return []


def validate_patch_gate_artifacts(artifact_graph: Any, instruction: dict[str, Any],
                                  fault_report: dict[str, Any]) -> dict[str, Any]:
    if instruction.get('mode') != 'patch_once':
        return {'allowed': False, 'reason': 'not_patch_once'}
    linked_refs = _list(instruction.get('linked_probe_result_refs'))
    if len(linked_refs) != 1:
        raise ValueError('patch_once requires exactly one DiagnosticProbeResult ref')
    probe_ref = ArtifactRef.parse(str(linked_refs[0]))
    if artifact_graph.schema_name(probe_ref) != 'DiagnosticProbeResult':
        raise ValueError('patch_once linked ref is not DiagnosticProbeResult')
    probe_result = artifact_graph.get(probe_ref)
    validate_repair_artifact('DiagnosticProbeResult', probe_result)
    worker_ref = ArtifactRef.parse(str(probe_result.get('worker_report_ref') or ''))
    trace_ref = ArtifactRef.parse(str(probe_result.get('raw_trace_ref') or ''))
    if artifact_graph.schema_name(worker_ref) != 'OpenCodeWorkerReport':
        raise ValueError('patch_once worker ref is not OpenCodeWorkerReport')
    if artifact_graph.schema_name(trace_ref) != 'OpenCodeRunTrace':
        raise ValueError('patch_once trace ref is not OpenCodeRunTrace')
    worker_report, trace = artifact_graph.get(worker_ref), artifact_graph.get(trace_ref)
    validate_repair_artifact('OpenCodeWorkerReport', worker_report)
    if worker_report.get('mode') != 'explore_only' or worker_report.get('protocol_status') != 'valid':
        raise ValueError('patch_once requires valid explore_only worker report')
    if str(worker_report.get('id') or '') != worker_ref.artifact_id:
        raise ValueError('patch_once worker report id/ref mismatch')
    if int(worker_report.get('attempt') or 0) != int(probe_result.get('attempt') or 0):
        raise ValueError('patch_once worker report attempt mismatch')
    if str(trace.get('id') or '') != trace_ref.artifact_id:
        raise ValueError('patch_once probe trace id/ref mismatch')
    if int(trace.get('attempt') or 0) != int(probe_result.get('attempt') or 0):
        raise ValueError('patch_once probe trace attempt mismatch')
    primary = anchor_location(fault_report, probe_result)
    if not any(isinstance(item, dict) and location_within_primary(item, primary)
               for item in _list(worker_report.get('confirmed_locations'))):
        raise ValueError('patch_once worker report does not confirm primary location')
    validate_patch_gate_contract(instruction, fault_report, probe_result)
    return probe_gate_status(fault_report, probe_result, instruction)


def anchor_location(fault_report: dict[str, Any], probe_result: dict[str, Any] | None = None) -> dict[str, Any]:
    """AST ranking points at where the failure manifests, not necessarily where the fix belongs.
    The patch anchor prefers the ranked location the probe proposes to edit, then the highest-ranked
    confirmed location; without any confirmation we fall back to rank #1."""
    locations = [loc for loc in _list(fault_report.get('ranked_locations')) if isinstance(loc, dict)]
    if not locations:
        return {}
    confirmed = [item for item in _list((probe_result or {}).get('probe_results'))
                 if isinstance(item, dict) and item.get('status') == 'confirmed']
    for pick_edit_target in (True, False):
        refs = {str(item.get('source_observation_ref') or '')
                for item in confirmed if not pick_edit_target or item.get('edit_target')}
        for loc in locations:
            if str(loc.get('source_observation_ref') or '') in refs:
                return loc
    return locations[0]


def probe_gate_status(fault_report: dict[str, Any], probe_result: dict[str, Any],
                      instruction: dict[str, Any] | None = None) -> dict[str, Any]:
    locations = _list(fault_report.get('ranked_locations'))
    if not locations:
        return _gate(False, 'no_primary_location')
    if fault_report.get('stage_conflicts'):
        return _gate(False, 'stage_conflict_requires_reanalysis')
    primary = anchor_location(fault_report, probe_result)
    if not _list(probe_result.get('probe_results')):
        return _gate(False, 'probe_result_missing')
    instruction_attempt = _positive_int((instruction or {}).get('attempt')) if instruction else 0
    probe_attempt = _positive_int(probe_result.get('attempt'))
    if instruction is not None and not instruction_attempt:
        return _gate(False, 'instruction_attempt_missing')
    if not probe_attempt:
        return _gate(False, 'probe_attempt_missing')
    if instruction_attempt and instruction_attempt != probe_attempt:
        return _gate(False, 'probe_attempt_mismatch')
    if instruction is not None:
        linked = {str(ref) for ref in _list(instruction.get('linked_probe_result_refs'))}
        if f"{probe_result.get('id')}@v1" not in linked:
            return _gate(False, 'linked_probe_result_ref_mismatch')
        primary_ref = str(instruction.get('primary_source_observation_ref') or '')
        if primary_ref != str(primary.get('source_observation_ref') or ''):
            return _gate(False, 'primary_source_observation_mismatch')
    if probe_result.get('protocol_status') != 'valid':
        return _gate(False, 'probe_protocol_invalid')
    if probe_result.get('origin') != 'opencode_probe_worker':
        return _gate(False, 'local_candidate_requires_worker_confirmation')
    if not str(probe_result.get('worker_report_ref') or ''):
        return _gate(False, 'probe_worker_report_ref_missing')
    if not str(probe_result.get('raw_trace_ref') or ''):
        return _gate(False, 'probe_trace_ref_missing')
    confirmed = any(
        item.get('status') == 'confirmed'
        and str(item.get('source_observation_ref') or '') == str(primary.get('source_observation_ref') or '')
        and location_within_primary(item, primary)
        for item in _list(probe_result.get('probe_results'))
    )
    if not confirmed:
        return _gate(False, 'confirmed_probe_primary_mismatch')
    return _gate(True, '')


def location_within_primary(location: dict[str, Any], primary: dict[str, Any]) -> bool:
    """AST primary locations are often class scopes; accept worker locations contained in them
    (e.g. KBToolGroup.kb_search inside KBToolGroup) instead of demanding exact equality."""
    if str(location.get('path') or '') != str(primary.get('path') or ''):
        return False
    symbol = str(location.get('symbol') or location.get('confirmed_symbol') or '').rsplit(':', 1)[-1]
    if not symbol_within_primary(symbol, str(primary.get('symbol') or '')):
        return False
    start, end = int(location.get('line_start') or 0), int(location.get('line_end') or 0)
    p_start, p_end = int(primary.get('line_start') or 0), int(primary.get('line_end') or 0)
    if start and end and p_start and p_end:
        return p_start <= start and end <= p_end
    return True


def symbol_within_primary(symbol: str, primary_symbol: str) -> bool:
    if not primary_symbol:
        return True
    return symbol == primary_symbol or symbol.startswith(primary_symbol + '.')


def _gate(allowed: bool, reason: str) -> dict[str, Any]:
    return {'allowed': allowed, 'reason': reason}


def _worker_locations_are_structured(value: Any) -> bool:
    return all(_worker_location_is_structured(item) for item in _list(value))


def _worker_location_is_structured(value: Any) -> bool:
    if not isinstance(value, dict):
        return False
    if any(key not in value for key in ('path', 'symbol', 'line_start', 'line_end', 'evidence')):
        return False
    if not str(value.get('path') or '').strip() or not str(value.get('symbol') or '').strip():
        return False
    try:
        line_start, line_end = int(value.get('line_start')), int(value.get('line_end'))
    except (TypeError, ValueError):
        return False
    return line_start > 0 and line_end >= line_start


def _positive_int(value: Any) -> int:
    try:
        parsed = int(value)
    except (TypeError, ValueError):
        return 0
    return parsed if parsed > 0 else 0


def _source_observation(payload: dict[str, Any], allowed_roots: list[str]) -> None:
    _require(payload, 'path', 'exists', 'within_allowed_roots', 'symbol', 'symbol_type', 'line_start', 'line_end')
    path = str(payload.get('path') or '')
    _relative_path(path)
    if payload.get('exists') is not True or payload.get('within_allowed_roots') is not True:
        raise ValueError('SourceObservation must point to an existing allowed source path')
    if allowed_roots and not any(path == root or path.startswith(f'{root}/') for root in allowed_roots):
        raise ValueError(f'SourceObservation outside allowed roots: {path}')
    if payload.get('symbol_type') not in {'function', 'class', 'method', 'module_block'}:
        raise ValueError('invalid SourceObservation symbol_type')
    start, end = int(payload.get('line_start') or 0), int(payload.get('line_end') or 0)
    if start <= 0 or end < start:
        raise ValueError('invalid SourceObservation line range')


def _transition(value: Any) -> None:
    if str(value or '') not in TRANSITION_FAILURE_KINDS:
        raise ValueError(f'invalid transition failure kind: {value}')


def _stage_hit(value: Any, label: str) -> None:
    if not isinstance(value, dict):
        raise ValueError(f'missing stage hit: {label}')
    if value.get('status') not in {'hit', 'miss', 'partial', 'unknown'}:
        raise ValueError(f'invalid stage hit status: {label}')
    if not isinstance(value.get('hits'), list) or not isinstance(value.get('missing'), list):
        raise ValueError(f'invalid stage hit hits/missing: {label}')


def _allowed_roots(payload: dict[str, Any]) -> list[str]:
    scope = payload.get('repair_scope') if isinstance(payload.get('repair_scope'), dict) else {}
    return [str(path) for path in _list(scope.get('allowed_roots'))]


def _require(payload: dict[str, Any], *keys: str) -> None:
    missing = [key for key in keys if key not in payload or payload.get(key) in (None, '')]
    if missing:
        raise ValueError(f'missing required fields: {missing}')


def _artifact_ref(value: str, label: str) -> None:
    try:
        ArtifactRef.parse(value)
    except (AttributeError, TypeError, ValueError):
        raise ValueError(f'invalid {label}: {value}')


def _relative_path(path: str) -> None:
    pure = PurePosixPath(path)
    if pure.is_absolute() or not path or path == '.' or '..' in pure.parts:
        raise ValueError(f'invalid relative path: {path}')


def _aid(prefix: str, attempt: int) -> str:
    return f'{prefix}_attempt_{attempt}'


def _list(value: Any) -> list[Any]:
    return value if isinstance(value, list) else []
