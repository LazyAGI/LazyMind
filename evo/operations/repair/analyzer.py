from __future__ import annotations

import ast
import hashlib
from pathlib import Path
from typing import Any

from ...artifacts import ArtifactRef
from ...runtime import OperationContext
from ..analysis.trace import flatten_trace, kb_searches, load_trace_payload, search_stage_summary, stage_hit_status
from ..analysis.utils import clean_contexts, typed_payload, values
from .schemas import (_aid, anchor_location, location_within_primary, probe_gate_status, symbol_within_primary,
                      validate_patch_gate_contract, validate_repair_artifact, worker_report_contract)


def _dir_cfg(direction: str, roles: list[str], keywords: list[str], edit_hint: str,
             risks: list[str]) -> dict[str, Any]:
    return {'direction': direction, 'trace_roles': roles, 'source_keywords': keywords,
            'edit_hint': edit_hint, 'risk_patterns': risks}


REPAIR_DIRECTION_CONFIG = {
    'retrieval_doc_miss': _dir_cfg(
        'improve retrieval document recall', ['retriever', 'retrieval_merge', 'kb_search'],
        ['retriever', 'search', 'doc', 'rank', 'kb'],
        'Make one conservative retrieval recall improvement inside allowed roots.',
        ['unbounded recall growth', 'goodcase precision regression']),
    'retrieval_chunk_miss': _dir_cfg(
        'improve chunk recall and context selection', ['retriever', 'retrieval_merge', 'result_merge'],
        ['chunk', 'context', 'node', 'segment', 'merge'],
        'Make one conservative chunk recall improvement inside allowed roots.',
        ['context bloat', 'score inversion']),
    'topk_cutoff_issue': _dir_cfg(
        'fix final context cutoff after rerank', ['reranker', 'result_merge', 'pipeline'],
        ['topk', 'adaptive', 'rerank', 'context', 'merge'],
        'Make the smallest safe production code change inside allowed roots.',
        ['unbounded context growth', 'score inversion', 'goodcase precision regression']),
    'rrf_merge_drop': _dir_cfg(
        'fix retrieval merge dropping relevant results', ['retrieval_merge', 'reranker'],
        ['rrf', 'merge', 'rank', 'score', 'rerank'],
        'Make the smallest safe production code change inside allowed roots.',
        ['score inversion']),
    'rerank_drop': _dir_cfg(
        'fix reranker dropping reference evidence', ['reranker'], ['rerank', 'score', 'topk', 'context'],
        'Make the smallest safe production code change inside allowed roots.',
        ['ranking quality regression']),
    'tool_execution_issue': _dir_cfg(
        'preserve successful tool execution outputs', ['tool_call', 'tool_manager', 'kb_search'],
        ['tool', 'exception', 'trace', 'kb_search'],
        'Preserve successful tool_call_trace results when tool execution raises.',
        ['masking real tool errors']),
    'tool_result_integration_issue': _dir_cfg(
        'preserve successful tool outputs through final answer synthesis',
        ['tool_call_parser', 'tool_manager', 'agent'],
        ['tool_result', 'event', 'translator', 'answer', 'pipeline'],
        'Make one minimal tool-result integration fix inside allowed roots.',
        ['duplicated tool frames']),
}


class RepairAnalyzer:
    def __init__(self, ctx: OperationContext, plan_ref: ArtifactRef, plan: dict[str, Any], workspace: Path):
        self.ctx, self.plan_ref, self.plan, self.workspace = ctx, plan_ref, plan, workspace
        self.scope = _repair_scope(ctx)

    def collect_evidence(self, attempt: int, memory: dict[str, Any] | None = None) -> dict[str, Any]:
        report = typed_payload(self.ctx, ArtifactRef.parse(str(self.plan.get('classification_report_ref') or '')),
                               'ClassificationReport')
        fine_refs = [ArtifactRef.parse(str(ref)) for ref in (self.plan.get('target') or {}).get('fine_refs') or []]
        selected, traces = [], []
        for fine_ref in fine_refs[:3]:
            fine = typed_payload(self.ctx, fine_ref, 'CaseFineClassification')
            case, rag, judge = self._case_bundle(fine)
            selected.append(_selected_case(case, rag, judge, fine))
            traces.append(extract_stage_hits(self.ctx, case, rag, judge, fine))
        sources = collect_source_observations(
            self.workspace, self.scope, str((self.plan.get('target') or {}).get('fine_category') or ''), memory or {}
        )
        payload = {
            'id': _aid('repair_evidence_packet', attempt), 'attempt': attempt,
            'repair_loop_plan_ref': str(self.plan_ref), 'repair_scope': self.scope,
            'selected_cases': selected, 'trace_observations': traces, 'source_observations': sources,
            'previous_attempt_observations': _previous_observations(memory or {}),
            'classification_report_summary': {'priority_count': len(report.get('priorities') or []),
                                              'case_count': len(report.get('cases') or [])},
            'budget': {'max_cases': 3, 'max_trace_steps_per_case': 6, 'max_source_snippets': 8,
                       'max_prompt_chars': 12000},
        }
        validate_repair_artifact('RepairEvidencePacket', payload)
        return payload

    def localize_fault(self, attempt: int, evidence: dict[str, Any],
                       memory: dict[str, Any] | None = None) -> dict[str, Any]:
        ranked = []
        trace_observations = [obs for obs in evidence.get('trace_observations') or [] if isinstance(obs, dict)]
        stage_conflicts = [
            unknown for obs in trace_observations for unknown in obs.get('unknowns') or []
            if isinstance(unknown, dict) and unknown.get('kind') == 'fine_stage_conflict'
        ]
        for index, obs in enumerate(evidence.get('source_observations') or [], start=1):
            total = int((obs.get('ranking_features') or {}).get('total') or 0)
            ranked.append({
                'rank': index, 'source_observation_ref': f"{evidence['id']}.source_observations[{index - 1}]",
                'path': obs['path'], 'symbol': obs['symbol'],
                'line_start': int(obs.get('line_start') or 0), 'line_end': int(obs.get('line_end') or 0),
                'score': total, 'dynamic_evidence': _location_dynamic_evidence(obs, trace_observations),
                'counter_evidence': [], 'confidence': 'high' if total >= 8 else 'medium' if total >= 5 else 'low',
                'trace_evidence_refs': _trace_evidence_refs(trace_observations),
                'fine_evidence_refs': _trace_fine_evidence_refs(trace_observations),
                'source_match_evidence': _source_match_evidence(obs),
            })
        payload = {
            'id': _aid('fault_localization', attempt), 'attempt': attempt,
            'repair_evidence_packet_ref': f"{evidence['id']}@v1", 'ranked_locations': ranked,
            'unlocalized_reasons': ([] if ranked else ['no_source_observation_total_gte_3'])
            + [item.get('reason', 'fine_stage_conflict') for item in stage_conflicts],
            'stage_conflicts': stage_conflicts, 'memory_refs': _memory_refs(memory or {}),
        }
        validate_repair_artifact('FaultLocalizationReport', payload)
        return payload

    def build_probe_plan(self, attempt: int, fault_report: dict[str, Any]) -> dict[str, Any]:
        top = (fault_report.get('ranked_locations') or [{}])[0]
        probes = [] if not top.get('path') else [{
            'id': 'probe_001', 'type': 'source_symbol_probe',
            'question': 'Confirm the exact production symbol that owns the suspected transition failure.',
            'allowed_actions': ['read', 'grep', 'python_ast_parse'],
            'target_paths': [top.get('path')] if top.get('path') else [],
            'expected_result_schema': {'confirmed_symbol': 'string', 'line_start': 'integer',
                                       'line_end': 'integer', 'rejected_symbols': 'array', 'evidence': 'string'},
        }]
        payload = {
            'id': _aid('diagnostic_probe_plan', attempt), 'attempt': attempt, 'mode': 'explore_only',
            'hypothesis_ids': [], 'probes': probes, 'no_edit': True,
            'stop_condition': 'Return DiagnosticProbeResult JSON; do not modify files.',
        }
        validate_repair_artifact('DiagnosticProbePlan', payload)
        return payload

    def run_local_probe(self, attempt: int, probe_plan: dict[str, Any], fault_report: dict[str, Any]) -> dict[str, Any]:
        results = []
        source_by_ref = {str(item.get('source_observation_ref') or ''): item
                         for item in fault_report.get('ranked_locations') or []}
        stage_conflicts = bool(fault_report.get('stage_conflicts'))
        for probe in probe_plan.get('probes') or []:
            ref = str((fault_report.get('ranked_locations') or [{}])[0].get('source_observation_ref') or '')
            loc = source_by_ref.get(ref) or {}
            results.append({
                'probe_id': probe.get('id'), 'status': 'inconclusive', 'source_observation_ref': ref,
                'candidate_path': loc.get('path', ''), 'candidate_symbol': loc.get('symbol', ''),
                'candidate_line_start': int(loc.get('line_start') or 0),
                'candidate_line_end': int(loc.get('line_end') or 0),
                'evidence': _local_probe_evidence('inconclusive', str(loc.get('confidence') or ''), stage_conflicts),
                'rejected_symbols': [],
            })
        payload = {
            'id': _aid('diagnostic_probe_result', attempt), 'attempt': attempt, 'probe_results': results,
            'protocol_status': 'valid', 'raw_trace_ref': '', 'origin': 'local_candidate', 'worker_report_ref': '',
        }
        validate_repair_artifact('DiagnosticProbeResult', payload)
        return payload

    def needs_opencode_explore(self, probe_plan: dict[str, Any], probe_result: dict[str, Any],
                               fault_report: dict[str, Any]) -> bool:
        return bool(probe_plan.get('probes')) and not patch_gate_allows(fault_report, probe_result)

    def build_explore_instruction(self, attempt: int, diagnosis: dict[str, Any], fault_report: dict[str, Any],
                                  probe_plan: dict[str, Any], probe_result: dict[str, Any]) -> dict[str, Any]:
        questions = [str(item.get('question') or '') for item in probe_plan.get('probes') or []]
        payload = self._instruction(
            attempt, diagnosis, fault_report, 'explore_only',
            tools=['read', 'grep', 'glob', 'list', 'bash'], no_edit=True, primary_ref='',
            linked_refs=[f"{probe_result['id']}@v1"] if probe_result.get('probe_results') else [],
            questions=[item for item in [
                *questions,
                'Confirm or reject the primary source location with exact symbol and line range.',
                'Return only an OpenCodeWorkerReport JSON object at the end.',
            ] if item],
            contract={
                'change_type': 'no_edit_probe',
                'must_do': ['inspect only; do not modify files'],
                'must_not_do': ['do not edit files', 'do not write files',
                                'do not touch blocked_roots or outside allowed_roots',
                                'do not propose a patch without confirmed source evidence'],
                'allowed_roots': self.scope['allowed_roots'], 'blocked_roots': self.scope['blocked_roots'],
                'allow_new_files': False,
            },
            local_validation={
                'required_if_feasible': ['read the suspected symbol and nearby callers'],
                'do_not_decide_success': 'This phase only confirms localization; patch gate is evaluated by Analyzer.',
            },
            stop='Stop after OpenCodeWorkerReport JSON with mode=explore_only; do not edit files.',
        )
        validate_repair_artifact('OpenCodeInstruction', payload)
        return payload

    def merge_probe_result_from_worker(self, attempt: int, local_probe: dict[str, Any], fault_report: dict[str, Any],
                                       worker_report: dict[str, Any], trace: dict[str, Any]) -> dict[str, Any]:
        ranked = [item for item in fault_report.get('ranked_locations') or [] if isinstance(item, dict)]
        worker_status = str(worker_report.get('protocol_status') or '')
        payload = {
            'id': local_probe.get('id') or _aid('diagnostic_probe_result', attempt),
            'attempt': attempt,
            'probe_results': [
                *[item for item in local_probe.get('probe_results') or [] if isinstance(item, dict)],
                *_worker_probe_results(local_probe, ranked, worker_report),
            ],
            'protocol_status': 'valid' if worker_status == 'valid' else 'invalid',
            'raw_trace_ref': f"{trace.get('id')}@v1" if trace.get('id') else '',
            'origin': 'opencode_probe_worker',
            'worker_report_ref': f"{worker_report.get('id')}@v1" if worker_report.get('id') else '',
        }
        validate_repair_artifact('DiagnosticProbeResult', payload)
        return payload

    def diagnose(self, attempt: int, evidence: dict[str, Any], fault_report: dict[str, Any],
                 probe_result: dict[str, Any], memory: dict[str, Any] | None = None) -> dict[str, Any]:
        category = str((self.plan.get('target') or {}).get('fine_category') or '')
        cfg = direction_config(category)
        locations = [_diagnosis_location(item) for item in fault_report.get('ranked_locations') or []]
        primary_failure = _primary_failure(evidence)
        hypothesis = {
            'id': f'hyp_{attempt:03d}', 'claim': _claim(category, primary_failure),
            'mechanism': _mechanism(primary_failure),
            'evidence_refs': [{'type': 'trace_observation', 'field': 'primary_transition_failure'}],
            'counter_evidence': [], 'confidence': 'medium' if locations else 'low',
            'expected_patch_effect':
                'target trace should move later or primary metric should improve without guard regression',
        }
        payload = {
            'id': _aid('repair_diagnosis', attempt), 'attempt': attempt, 'branch_id': 'branch_active',
            'repair_evidence_packet_ref': f"{evidence['id']}@v1",
            'fault_localization_report_ref': f"{fault_report['id']}@v1",
            'target_signature': {
                'fine_category': category, 'case_count': len(evidence.get('selected_cases') or []),
                'primary_metric': str((self.plan.get('policy') or {}).get('primary_metric') or 'answer_correctness'),
                'shared_failure': primary_failure,
            },
            'suspected_code_locations': locations,
            'root_cause_hypotheses': [hypothesis],
            'rejected_causes': _rejected_causes(primary_failure),
            'analysis_limitations': _analysis_limitations(evidence, locations),
            'next_experiment': {
                'goal': cfg['direction'],
                'success_signal': 'target metric improves or trace delta fixes the transition failure',
                'failure_signal': 'same transition failure remains or goodcase guard regresses',
            },
            'memory_usage': _memory_usage(memory or {}, attempt),
            'probe_result_ref': f"{probe_result['id']}@v1",
        }
        validate_repair_artifact('RepairDiagnosis', payload)
        return payload

    def build_instruction(self, attempt: int, diagnosis: dict[str, Any], fault_report: dict[str, Any],
                          probe_result: dict[str, Any], memory: dict[str, Any] | None = None) -> dict[str, Any]:
        mode = 'patch_once' if patch_gate_allows(fault_report, probe_result) else 'no_patch'
        top = anchor_location(fault_report, probe_result)
        lock = (memory or {}).get('anchor_lock') if isinstance((memory or {}).get('anchor_lock'), dict) else {}
        lock_rules = [f"keep the edit at the locked anchor {lock['path']}::{lock['symbol']}; "
                      'change the repair idea, not the location'] if lock.get('path') else []
        avoid = [str(item) for item in ((memory or {}).get('failed_patch_summaries') or [])[-3:]
                 if isinstance(item, (str, dict))]
        payload = self._instruction(
            attempt, diagnosis, fault_report, mode,
            tools=['read', 'grep', 'glob', 'list', 'bash', 'edit', 'write'] if mode == 'patch_once' else [],
            no_edit=mode != 'patch_once',
            primary_ref=top.get('source_observation_ref', '') if mode == 'patch_once' else '',
            linked_refs=[f"{probe_result['id']}@v1"]
            if (probe_result.get('probe_results') and mode == 'patch_once') else [],
            questions=[
                'Confirm where the failing trace transition is implemented.',
                'Check whether the target symbol already has a narrower guard before editing.',
            ],
            contract={
                'change_type': 'minimal_behavior_change',
                'must_do': (['make exactly one focused production code patch'] + lock_rules)
                if mode == 'patch_once' else [],
                'must_not_do': ['do not edit tests', 'do not touch blocked_roots or outside allowed_roots',
                                'do not add broad fallback behavior',
                                *(
                                    [
                                        'do not re-submit a previously failed patch; failed attempts: '
                                        + '; '.join(str(item)[:120] for item in avoid),
                                    ]
                                    if avoid else []
                                )],
                'allowed_roots': self.scope['allowed_roots'], 'blocked_roots': self.scope['blocked_roots'],
                'allow_new_files': self.scope['allow_new_files'],
            },
            local_validation={
                'required_if_feasible': ['run the narrowest existing test or compile touched files'],
                'do_not_decide_success': 'Evaluator will run real RAG/Judge.',
            },
            stop='Stop after one minimal diff and worker report JSON.'
            if mode == 'patch_once' else 'Do not edit files.',
        )
        validate_repair_artifact('OpenCodeInstruction', payload)
        validate_patch_gate_contract(payload, fault_report, probe_result)
        return payload

    def _instruction(self, attempt: int, diagnosis: dict[str, Any], fault_report: dict[str, Any], mode: str, *,
                     tools: list[str], no_edit: bool, primary_ref: str, linked_refs: list[str],
                     questions: list[str], contract: dict[str, Any], local_validation: dict[str, Any],
                     stop: str) -> dict[str, Any]:
        primary_hyp = (diagnosis.get('root_cause_hypotheses') or [{}])[0]
        return {
            'id': _instruction_id(mode, attempt),
            'attempt': attempt,
            'diagnosis_ref': f"{diagnosis['id']}@v1",
            'mode': mode,
            'objective': _objective(mode),
            'allowed_tools': tools,
            'no_edit': no_edit,
            'primary_source_observation_ref': primary_ref,
            'linked_probe_result_refs': linked_refs,
            'branch_context': {'branch_id': 'branch_active', 'base_kind': 'best_baseline', 'patch_lineage': []},
            'diagnosis_summary': {
                'shared_failure': (diagnosis.get('target_signature') or {}).get('shared_failure', ''),
                'primary_hypothesis': primary_hyp.get('claim', ''),
                'confidence': primary_hyp.get('confidence', 'low'),
            },
            'start_points': [_start_point(item) for item in _anchored_first(fault_report, primary_ref)][:3],
            'exploration_questions': questions,
            'patch_contract': contract,
            'local_validation': local_validation,
            'worker_report_contract': worker_report_contract(mode),
            'stop_condition': stop,
        }

    def _case_bundle(self, fine: dict[str, Any]) -> tuple[dict[str, Any], dict[str, Any], dict[str, Any]]:
        case = typed_payload(self.ctx, ArtifactRef.parse(str(fine.get('case_ref') or '')), 'DatasetCase')
        rag = typed_payload(self.ctx, ArtifactRef.parse(str(fine.get('rag_answer_ref') or '')), 'RagAnswer')
        judge = typed_payload(self.ctx, ArtifactRef.parse(str(fine.get('judge_result_ref') or '')), 'JudgeResult')
        return case, rag, judge


def extract_stage_hits(ctx: OperationContext, case: dict[str, Any], rag: dict[str, Any], judge: dict[str, Any],
                       fine: dict[str, Any]) -> dict[str, Any]:
    trace_id = str(rag.get('trace_id') or judge.get('trace_id') or fine.get('trace_id') or '')
    unknowns = []
    try:
        trace = load_trace_payload(ctx, trace_id, rag)
        nodes = flatten_trace(trace, trace_id)
        trace_error = ''
    except Exception as exc:
        nodes, trace_error = [], str(exc)
        unknowns.append({'kind': 'trace_read_error', 'reason': trace_error})
    ref_docs, ref_chunks, ref_names = (values(case.get('reference_doc_ids')),
                                       values(case.get('reference_chunk_ids')), values(case.get('reference_doc')))
    searches = kb_searches(nodes, ref_docs, ref_chunks, ref_names) if nodes else []
    aggregate = _aggregate_hits(searches, ref_docs, ref_chunks)
    final = {
        'doc_ids': sorted(values(rag.get('doc_ids'))), 'chunk_ids': sorted(values(rag.get('chunk_ids'))),
        'doc': _hit_status(ref_docs, values(rag.get('doc_ids'))),
        'chunk': _hit_status(ref_chunks, values(rag.get('chunk_ids'))),
        'name': _hit_status(ref_names, _context_names(rag)),
        'context_text': _context_text_status(case, rag),
    }
    aggregate['final_doc'], aggregate['final_chunk'] = final['doc'], final['chunk']
    primary = _transition_failure(searches, aggregate, final, judge, fine, bool(nodes))
    evidence_conflict = _fine_stage_conflict(fine, primary)
    if evidence_conflict:
        unknowns.append(evidence_conflict)
    confidence = 'low' if evidence_conflict else _stage_confidence(ref_docs, ref_chunks, nodes, fine, final)
    return {
        'case_id': str(case.get('id') or fine.get('case_id') or ''), 'trace_id': trace_id,
        'confidence': confidence,
        'confidence_reasons': ([] if not trace_error else [trace_error])
        + ([evidence_conflict['reason']] if evidence_conflict else []),
        'reference': {'doc_ids': sorted(ref_docs), 'chunk_ids': sorted(ref_chunks), 'doc_names': sorted(ref_names)},
        'final': final,
        'searches': [search_stage_summary(index, item) for index, item in enumerate(searches)],
        'aggregate': aggregate,
        'primary_transition_failure': primary,
        'secondary_failure_candidates': _secondary_failures(primary, final, judge),
        'unknowns': unknowns,
        'evidence_refs': _fine_evidence_refs(fine),
    }


def collect_source_observations(workspace: Path, scope: dict[str, Any], fine_category: str,
                                memory: dict[str, Any] | None = None) -> list[dict[str, Any]]:
    cfg = direction_config(fine_category)
    roots = list(scope.get('allowed_roots') or [])
    observations = []
    for rel, file_relevance in _candidate_source_paths(workspace, roots, cfg):
        path = workspace / rel
        if not path.is_file():
            continue
        for symbol in _symbols(path, rel):
            features = _ranking_features(rel, symbol, cfg, memory or {}, file_relevance)
            if features['total'] < 3:
                continue
            observations.append(_source_observation(path, rel, symbol, features, roots, cfg))
    observations.sort(key=lambda item: (-(item.get('ranking_features') or {}).get('total', 0),
                                        item['path'], item['line_start']))
    return observations[:8]


def direction_config(category: str) -> dict[str, Any]:
    return REPAIR_DIRECTION_CONFIG.get(category, _dir_cfg(
        'improve the shared failure mode with a minimal production change', [],
        ['trace', 'context', 'answer', 'tool'],
        'Make the smallest safe production code change inside allowed roots.',
        ['unrelated behavior change']))


def patch_gate_allows(fault_report: dict[str, Any], probe_result: dict[str, Any]) -> bool:
    return bool(probe_gate_status(fault_report, probe_result).get('allowed'))


def _location_dynamic_evidence(obs: dict[str, Any], trace_observations: list[dict[str, Any]]) -> list[str]:
    failures = [str(item.get('primary_transition_failure') or '') for item in trace_observations
                if str(item.get('primary_transition_failure') or '') not in {'', 'none'}]
    evidence = [f'badcase stage failure is {failure}' for failure in sorted(set(failures))]
    roles = sorted(str(role) for role in obs.get('matched_trace_roles') or [] if str(role))
    if roles:
        evidence.append(f'source symbol matches trace roles: {", ".join(roles[:4])}')
    keywords = sorted(str(keyword) for keyword in obs.get('matched_keywords') or [] if str(keyword))
    if keywords:
        evidence.append(f'source symbol matches repair keywords: {", ".join(keywords[:4])}')
    return evidence


def _trace_evidence_refs(trace_observations: list[dict[str, Any]]) -> list[dict[str, str]]:
    return [{'case_id': str(obs.get('case_id') or ''), 'field': 'primary_transition_failure'}
            for obs in trace_observations
            if str(obs.get('primary_transition_failure') or '') not in {'', 'none'}][:3]


def _trace_fine_evidence_refs(trace_observations: list[dict[str, Any]]) -> list[dict[str, str]]:
    refs: list[dict[str, str]] = []
    for obs in trace_observations:
        for ref in obs.get('evidence_refs') or []:
            if isinstance(ref, dict):
                refs.append({'type': str(ref.get('type') or ''), 'field': str(ref.get('field') or '')})
    return refs[:6]


def _analysis_limitations(evidence: dict[str, Any], locations: list[dict[str, Any]]) -> list[str]:
    limitations = [] if locations else ['source localization did not find a valid primary symbol']
    for obs in evidence.get('trace_observations') or []:
        if not isinstance(obs, dict):
            continue
        for unknown in obs.get('unknowns') or []:
            if isinstance(unknown, dict) and unknown.get('kind') == 'trace_read_error':
                limitations.append(f"trace read error: {str(unknown.get('reason') or 'trace read error')}")
    return list(dict.fromkeys(limitations))


def _source_match_evidence(obs: dict[str, Any]) -> list[dict[str, Any]]:
    features = obs.get('ranking_features') if isinstance(obs.get('ranking_features'), dict) else {}
    return [
        {'kind': 'keyword', 'values': list(obs.get('matched_keywords') or [])[:6]},
        {'kind': 'trace_role', 'values': list(obs.get('matched_trace_roles') or [])[:6]},
        {'kind': 'call_context', 'values': list((obs.get('call_context') or {}).get('calls') or [])[:8]},
        {'kind': 'ranking_score', 'value': int(features.get('total') or 0)},
    ]


def _anchored_first(fault_report: dict[str, Any], primary_ref: str) -> list[dict[str, Any]]:
    ranked = [item for item in fault_report.get('ranked_locations') or [] if isinstance(item, dict)]
    if not primary_ref:
        return ranked
    return sorted(ranked, key=lambda item: str(item.get('source_observation_ref') or '') != primary_ref)


def _local_probe_evidence(status: str, confidence: str, stage_conflicts: bool) -> str:
    if status == 'confirmed':
        return 'local AST source observation confirms ranked symbol'
    if stage_conflicts:
        return 'local AST found a symbol, but trace/fine stage evidence conflicts'
    if confidence == 'low':
        return 'local AST found a low-confidence symbol; opencode explore should verify before patch'
    return 'local AST candidate only; opencode explore must confirm before patch'


def _probe_entry(probe_id: str, status: str, primary: dict[str, Any], evidence: str, *,
                 confirmed: bool = False, rejected: list[str] | None = None) -> dict[str, Any]:
    return {
        'probe_id': probe_id, 'status': status,
        'source_observation_ref': str(primary.get('source_observation_ref') or ''),
        'path': str(primary.get('path') or ''),
        'confirmed_symbol': str(primary.get('symbol') or '') if confirmed else '',
        'line_start': int(primary.get('line_start') or 0) if confirmed else 0,
        'line_end': int(primary.get('line_end') or 0) if confirmed else 0,
        'evidence': evidence, 'rejected_symbols': rejected or [],
    }


def _worker_probe_results(local_probe: dict[str, Any], ranked: list[dict[str, Any]],
                          worker_report: dict[str, Any]) -> list[dict[str, Any]]:
    """Map worker confirmations onto every ranked location so the patch anchor can re-anchor to the
    location the probe actually confirmed instead of demanding rank #1."""
    probe_id = str(((local_probe.get('probe_results') or [{}])[0]).get('probe_id') or 'opencode_probe')
    status = str(worker_report.get('protocol_status') or '')
    primary = (ranked or [{}])[0]
    if status != 'valid':
        return [_probe_entry(probe_id, 'failed', primary,
                             f'opencode explore worker report protocol_status={status or "missing"}')]
    confirmed = [loc for loc in worker_report.get('confirmed_locations') or [] if isinstance(loc, dict)]
    rejected = [loc for loc in worker_report.get('rejected_locations') or [] if isinstance(loc, dict)]
    edit = worker_report.get('edit_intent') if isinstance(worker_report.get('edit_intent'), dict) else {}
    edit_symbol = str(edit.get('target_symbol') or '').rsplit(':', 1)[-1]
    entries = []
    for location in ranked:
        hit = next((loc for loc in confirmed if location_within_primary(loc, location)), None)
        if hit is not None:
            entry = _probe_entry(probe_id, 'confirmed', location,
                                 str(hit.get('evidence') or 'opencode explore confirmed source location'),
                                 confirmed=True)
            entry['edit_target'] = bool(edit_symbol) and symbol_within_primary(edit_symbol,
                                                                               str(location.get('symbol') or ''))
            entries.append(entry)
            continue
        miss = next((loc for loc in rejected if location_within_primary(loc, location)), None)
        if miss is not None:
            entries.append(_probe_entry(probe_id, 'rejected', location,
                                        str(miss.get('evidence') or 'opencode explore rejected source location'),
                                        rejected=[str(location.get('symbol') or '')]))
    return entries or [_probe_entry(probe_id, 'inconclusive', primary,
                                    'opencode explore did not confirm any ranked source location')]


def _aggregate_hits(searches: list[dict[str, Any]], ref_docs: set[str], ref_chunks: set[str]) -> dict[str, Any]:
    keys = {
        'retriever_doc': ('retrievers', 'doc', ref_docs), 'retriever_chunk': ('retrievers', 'chunk', ref_chunks),
        'merge_doc': ('merge', 'doc', ref_docs), 'merge_chunk': ('merge', 'chunk', ref_chunks),
        'rerank_input_doc': ('reranker_input', 'doc', ref_docs),
        'rerank_input_chunk': ('reranker_input', 'chunk', ref_chunks),
        'rerank_output_doc': ('reranker_output', 'doc', ref_docs),
        'rerank_output_chunk': ('reranker_output', 'chunk', ref_chunks),
    }
    out = {name: stage_hit_status(searches, stage, kind, expected)
           for name, (stage, kind, expected) in keys.items()}
    out['final_doc'] = {'status': 'unknown', 'hits': [], 'missing': [], 'unknown_reason': 'filled_from_final'}
    out['final_chunk'] = {'status': 'unknown', 'hits': [], 'missing': [], 'unknown_reason': 'filled_from_final'}
    return out


def _transition_failure(searches: list[dict[str, Any]], aggregate: dict[str, Any], final: dict[str, Any],
                        judge: dict[str, Any], fine: dict[str, Any], trace_available: bool) -> str:
    coarse_hits = (fine.get('evidence') or {}).get('coarse_rule_hits') or []
    if any('tool_error' in str(hit.get('rule_id') or hit) for hit in coarse_hits if isinstance(hit, (dict, str))):
        return 'tool_execution_error'
    if not trace_available:
        return 'none'
    if not searches and final['doc']['status'] in {'miss', 'unknown'} \
            and final['chunk']['status'] in {'miss', 'unknown'}:
        return 'no_kb_search'
    if aggregate['retriever_doc']['status'] == 'miss' or aggregate['retriever_chunk']['status'] == 'miss':
        return 'retriever_miss'
    if _hitish(aggregate['retriever_doc']) and aggregate['merge_doc']['status'] == 'miss':
        return 'retriever_to_merge_drop'
    if _hitish(aggregate['retriever_chunk']) and aggregate['merge_chunk']['status'] == 'miss':
        return 'retriever_to_merge_drop'
    if _hitish(aggregate['retriever_chunk']) and aggregate['rerank_input_chunk']['status'] == 'miss':
        return 'merge_to_rerank_input_drop'
    if _hitish(aggregate['rerank_input_chunk']) and aggregate['rerank_output_chunk']['status'] == 'miss':
        return 'rerank_drop'
    if _hitish(aggregate['rerank_output_chunk']) and final['chunk']['status'] == 'miss':
        return 'rerank_output_to_final_context_drop'
    if _tool_result_available(fine) and final['chunk']['status'] in {'hit', 'partial'} \
            and float(judge.get('faithfulness') or 0.0) < 0.8:
        return 'tool_result_to_answer_drop'
    if final['chunk']['status'] in {'hit', 'partial'} and float(judge.get('answer_correctness') or 0.0) < 0.8:
        return 'generation_missed_available_context'
    return 'none'


def _tool_result_available(fine: dict[str, Any]) -> bool:
    hits = (fine.get('evidence') or {}).get('coarse_rule_hits') or []
    return any('tool' in str(hit.get('rule_id') or hit) for hit in hits if isinstance(hit, (dict, str)))


def _fine_stage_conflict(fine: dict[str, Any], primary: str) -> dict[str, str] | None:
    fine_hits = (fine.get('evidence') or {}).get('fine_rule_hits') or []
    text = ' '.join(str(hit.get('rule_id') or hit) for hit in fine_hits if isinstance(hit, (dict, str)))
    expected = {
        'topk_cutoff_issue': 'rerank_output_to_final_context_drop',
        'rerank_drop': 'rerank_drop',
        'rrf_merge_drop': 'retriever_to_merge_drop',
        'retrieval_doc_miss': 'retriever_miss',
        'retrieval_chunk_miss': 'retriever_miss',
    }.get(str(fine.get('fine_category') or ''))
    if expected and primary not in {expected, 'none'}:
        return {'kind': 'fine_stage_conflict',
                'reason': f'fine_category expects {expected} but stage extraction found {primary}'}
    if fine_hits and text and primary == 'none':
        return {'kind': 'fine_stage_conflict', 'reason': 'fine_rule_hits exist but stage extraction found none'}
    return None


def _hit_status(expected: set[str], actual: set[str]) -> dict[str, Any]:
    if not expected:
        return {'status': 'unknown', 'hits': [], 'missing': [], 'unknown_reason': 'no_reference_ids'}
    hits, missing = sorted(expected & actual), sorted(expected - actual)
    status = 'hit' if hits and not missing else 'partial' if hits else 'miss'
    return {'status': status, 'hits': hits, 'missing': missing, 'unknown_reason': ''}


def _candidate_source_paths(workspace: Path, roots: list[str], cfg: dict[str, Any]) -> list[tuple[str, int]]:
    """Discover and rank candidate files from the workspace itself: path and content hits against
    the direction's semantic tokens decide priority, so no algorithm file path is hardcoded."""
    tokens = _direction_tokens(cfg)
    scored: list[tuple[int, int, str]] = []
    for root in roots:
        base = workspace / root
        if not base.exists():
            continue
        for path in sorted(base.rglob('*.py')):
            rel = path.relative_to(workspace).as_posix()
            try:
                text = path.read_text(encoding='utf-8', errors='ignore').lower()
            except OSError:
                continue
            path_hits = sum(1 for token in tokens if token in rel.lower())
            content_hits = sum(1 for token in tokens if token in text)
            scored.append((path_hits, content_hits, rel))
    scored.sort(key=lambda item: (-item[0], -item[1], item[2]))
    return [(rel, _file_relevance(path_hits, content_hits))
            for path_hits, content_hits, rel in scored[:80]]


def _file_relevance(path_hits: int, content_hits: int) -> int:
    if path_hits >= 2:
        return 2
    if path_hits or content_hits >= 3:
        return 1
    return 0


def _direction_tokens(cfg: dict[str, Any]) -> list[str]:
    return [token for token in dict.fromkeys(
        item.lower() for item in [*(cfg.get('source_keywords') or []), *(cfg.get('trace_roles') or [])]
    ) if token]


def _symbols(path: Path, rel: str) -> list[dict[str, Any]]:
    try:
        text = path.read_text(encoding='utf-8')
        tree = ast.parse(text)
    except (OSError, SyntaxError, UnicodeDecodeError):
        return []
    lines = text.splitlines()
    symbols = [_symbol_payload(node, rel, lines, 'function') for node in tree.body
               if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef))]
    for cls in [node for node in tree.body if isinstance(node, ast.ClassDef)]:
        symbols.append(_symbol_payload(cls, rel, lines, 'class'))
        for child in cls.body:
            if isinstance(child, (ast.FunctionDef, ast.AsyncFunctionDef)):
                symbols.append(_symbol_payload(child, rel, lines, 'method', owner=cls.name))
    called_by = _called_by(symbols)
    for symbol in symbols:
        symbol['called_by'] = called_by.get(symbol['symbol'], [])
    if not symbols and lines:
        symbols.append({'path': rel, 'symbol': '__module__', 'symbol_type': 'module_block',
                        'line_start': 1, 'line_end': len(lines), 'text': text, 'called_by': []})
    return symbols


def _symbol_payload(node: ast.AST, rel: str, lines: list[str], symbol_type: str, owner: str = '') -> dict[str, Any]:
    start = int(getattr(node, 'lineno', 1))
    end = int(getattr(node, 'end_lineno', start))
    name = str(getattr(node, 'name', ''))
    return {'path': rel, 'symbol': f'{owner}.{name}' if owner and symbol_type == 'method' else name,
            'symbol_type': symbol_type, 'line_start': start, 'line_end': end,
            'text': '\n'.join(lines[start - 1:end])}


def _called_by(symbols: list[dict[str, Any]]) -> dict[str, list[str]]:
    names = {str(symbol.get('symbol') or '').split('.')[-1]: str(symbol.get('symbol') or '') for symbol in symbols}
    out = {str(symbol.get('symbol') or ''): [] for symbol in symbols}
    for symbol in symbols:
        caller = str(symbol.get('symbol') or '')
        for call in _call_context(str(symbol.get('text') or '')).get('calls', []):
            callee = names.get(call)
            if callee and caller not in out[callee]:
                out[callee].append(caller)
    return out


def _ranking_features(rel: str, symbol: dict[str, Any], cfg: dict[str, Any], memory: dict[str, Any],
                      file_relevance: int = 0) -> dict[str, int]:
    text = f"{rel}\n{symbol.get('symbol')}\n{symbol.get('text')}".lower()
    keywords = [item.lower() for item in cfg.get('source_keywords') or []]
    name = str(symbol.get('symbol') or '').lower()
    direction_file_score = file_relevance
    keyword_score = min(4, sum(1 for item in keywords if item and item in text))
    symbol_score = 2 if any(token in name for token in _direction_tokens(cfg)) else 0
    trace_role_score = 2 if keyword_score and any(role in text for role in cfg.get('trace_roles') or []) else 0
    previous_failure_penalty = _previous_failure_penalty(rel, symbol, memory)
    risk_penalty = 1 if symbol.get('symbol_type') == 'module_block' else 0
    worker_probe_score = _worker_probe_score(rel, symbol, memory)
    anchor_lock_score = _anchor_lock_score(rel, symbol, memory)
    return {
        'direction_file_score': direction_file_score, 'keyword_score': keyword_score,
        'symbol_score': symbol_score, 'trace_role_score': trace_role_score,
        'worker_probe_score': worker_probe_score, 'anchor_lock_score': anchor_lock_score,
        'previous_failure_penalty': previous_failure_penalty,
        'risk_penalty': risk_penalty,
        'total': (direction_file_score + keyword_score + symbol_score + trace_role_score
                  + worker_probe_score + anchor_lock_score - previous_failure_penalty - risk_penalty),
    }


def _anchor_lock_score(rel: str, symbol: dict[str, Any], memory: dict[str, Any]) -> int:
    """Keep attempts pinned to the locked anchor (only the edit idea may change) and demote
    anchors that were released after repeated no-progress attempts."""
    name = str(symbol.get('symbol') or '')
    lock = memory.get('anchor_lock') if isinstance(memory.get('anchor_lock'), dict) else {}
    score = 8 if lock and _location_matches(lock, rel, name) else 0
    if any(_location_matches(item, rel, name) for item in memory.get('released_anchors') or []):
        score -= 4
    return score


def _worker_probe_score(rel: str, symbol: dict[str, Any], memory: dict[str, Any]) -> int:
    """Rank previous-attempt worker probe evidence above static heuristics: confirmed
    locations become the next primary, rejected ones drop out of the top ranks."""
    evidence = memory.get('worker_probe_evidence') if isinstance(memory.get('worker_probe_evidence'), dict) else {}
    name = str(symbol.get('symbol') or '')
    score = 0
    if any(_location_matches(loc, rel, name) for loc in evidence.get('confirmed_locations') or []):
        score += 6
    if any(_location_matches(loc, rel, name) for loc in evidence.get('rejected_locations') or []):
        score -= 4
    return score


def _location_matches(location: Any, rel: str, name: str) -> bool:
    if not isinstance(location, dict) or str(location.get('path') or '') != rel:
        return False
    loc_symbol = str(location.get('symbol') or location.get('confirmed_symbol') or '').rsplit(':', 1)[-1]
    return bool(name) and bool(loc_symbol) and (
        symbol_within_primary(loc_symbol, name) or symbol_within_primary(name, loc_symbol)
    )


def _source_observation(path: Path, rel: str, symbol: dict[str, Any], features: dict[str, int],
                        roots: list[str], cfg: dict[str, Any]) -> dict[str, Any]:
    text = str(symbol.get('text') or '')
    return {
        'path': rel, 'exists': True, 'relative_path_valid': True, 'within_allowed_roots': _path_in(rel, roots),
        'symbol': str(symbol.get('symbol') or ''), 'symbol_type': str(symbol.get('symbol_type') or 'module_block'),
        'line_start': int(symbol.get('line_start') or 1), 'line_end': int(symbol.get('line_end') or 1),
        'snippet_hash': f"sha256:{hashlib.sha256(text.encode('utf-8')).hexdigest()}",
        'matched_keywords': [kw for kw in cfg.get('source_keywords') or []
                             if kw.lower() in text.lower() or kw.lower() in rel.lower()],
        'matched_trace_roles': [role for role in cfg.get('trace_roles') or [] if role.lower() in text.lower()],
        'call_context': _call_context(text) | {'called_by': list(symbol.get('called_by') or [])},
        'ranking_features': features,
        'snippet_summary': text.strip().splitlines()[0][:200] if text.strip() else '',
        'risk_notes': list(cfg.get('risk_patterns') or [])[:3],
    }


def _call_context(text: str) -> dict[str, list[str]]:
    try:
        tree = ast.parse(text)
    except SyntaxError:
        return {'calls': [], 'called_by': [], 'imports': []}
    calls = sorted({
        node.func.id if isinstance(node.func, ast.Name) else node.func.attr
        for node in ast.walk(tree)
        if isinstance(node, ast.Call) and isinstance(node.func, (ast.Name, ast.Attribute))
    })
    imports = sorted({alias.name for node in ast.walk(tree) if isinstance(node, ast.Import) for alias in node.names})
    imports += sorted({node.module for node in ast.walk(tree) if isinstance(node, ast.ImportFrom) and node.module})
    return {'calls': calls[:20], 'called_by': [], 'imports': imports[:20]}


def _repair_scope(ctx: OperationContext) -> dict[str, Any]:
    from .candidate import default_repair_scope

    raw = ctx.params.get('repair_scope') if isinstance(ctx.params.get('repair_scope'), dict) else {}
    scope = default_repair_scope() | raw
    return {'allowed_roots': _norm_paths(scope.get('allowed_roots')),
            'seed_files': _norm_paths(scope.get('seed_files')),
            'blocked_roots': _norm_paths(scope.get('blocked_roots')),
            'allow_new_files': bool(scope.get('allow_new_files', True))}


def _norm_paths(items: Any) -> list[str]:
    out = []
    for item in items or []:
        rel = Path(str(item).strip()).as_posix()
        if rel and not rel.startswith('/') and '..' not in Path(rel).parts and rel not in out:
            out.append(rel.rstrip('/'))
    return out


def _path_in(path: str, roots: list[str]) -> bool:
    return any(path == root or path.startswith(f'{root}/') for root in roots)


def _selected_case(case: dict[str, Any], rag: dict[str, Any], judge: dict[str, Any],
                   fine: dict[str, Any]) -> dict[str, Any]:
    return {
        'case_id': str(case.get('id') or fine.get('case_id') or ''),
        'selection_reason': 'target badcase from RepairLoopPlan',
        'quality': {'answer_correctness': float(judge.get('answer_correctness') or 0.0),
                    'faithfulness': float(judge.get('faithfulness') or 0.0),
                    'doc_recall': float(judge.get('doc_recall') or 0.0),
                    'context_recall': float(judge.get('context_recall') or 0.0)},
        'fine_category': str(fine.get('fine_category') or ''),
        'judge_reason': str(judge.get('reason') or ''),
        'expected_refs': {'doc_ids': sorted(values(case.get('reference_doc_ids'))),
                          'chunk_ids': sorted(values(case.get('reference_chunk_ids')))},
        'observed_refs': {'rag_doc_ids': sorted(values(rag.get('doc_ids'))),
                          'rag_chunk_ids': sorted(values(rag.get('chunk_ids')))},
    }


def _previous_observations(memory: dict[str, Any]) -> list[dict[str, Any]]:
    return [{'key': item.get('key', ''), 'verdict': item.get('verdict', ''),
             'observed_effect': item.get('observed_effect', '')}
            for item in memory.get('failure_library') or [] if isinstance(item, dict)][:5]


def _memory_refs(memory: dict[str, Any]) -> list[str]:
    return [str(item.get('key') or '') for item in memory.get('failure_library') or [] if isinstance(item, dict)][:5]


def _memory_usage(memory: dict[str, Any], attempt: int) -> dict[str, Any]:
    if attempt <= 1:
        return {'used_memory_keys': [], 'rejected_memory_keys': []}
    return {'used_memory_keys': _memory_refs(memory), 'rejected_memory_keys': []}


def _primary_failure(evidence: dict[str, Any]) -> str:
    failures = [obs.get('primary_transition_failure') for obs in evidence.get('trace_observations') or []]
    return next((str(item) for item in failures if item and item != 'none'), 'none')


def _claim(category: str, failure: str) -> str:
    if failure == 'generation_missed_available_context':
        return 'available context reaches final answer stage but synthesis misses the evidence'
    if failure == 'rerank_output_to_final_context_drop':
        return 'final context cutoff drops evidence after rerank output'
    if failure == 'retriever_miss':
        return 'retriever does not recall required reference evidence'
    return f'{category or "repair target"} is explained by {failure}'


def _mechanism(failure: str) -> str:
    return {
        'rerank_output_to_final_context_drop':
            'reference chunks are present after rerank but lost while building final contexts',
        'retriever_miss': 'reference docs or chunks never enter the retriever output',
        'rerank_drop': 'reference chunks reach rerank input but not rerank output',
        'generation_missed_available_context': 'answer generation ignores available supporting context',
    }.get(failure, 'the observed trace transition does not preserve required evidence')


def _rejected_causes(failure: str) -> list[dict[str, str]]:
    if failure == 'rerank_output_to_final_context_drop':
        return [{'claim': 'initial retrieval cannot find reference chunks',
                 'reason': 'rerank output already contains reference hits'}]
    return []


def _diagnosis_location(item: dict[str, Any]) -> dict[str, Any]:
    return {'rank': item.get('rank'), 'path': item.get('path'), 'symbol': item.get('symbol'),
            'location_type': 'function_or_block',
            'why_suspected': '; '.join(item.get('dynamic_evidence') or []) or 'highest deterministic source ranking',
            'confidence': item.get('confidence'), 'risk': 'shared retrieval behavior'}


def _start_point(item: dict[str, Any]) -> dict[str, Any]:
    return {'path': item.get('path', ''), 'symbol_hint': item.get('symbol', ''),
            'source_observation_ref': item.get('source_observation_ref', ''),
            'line_start': int(item.get('line_start') or 0), 'line_end': int(item.get('line_end') or 0),
            'why_here': '; '.join(item.get('dynamic_evidence') or []) or 'ranked source observation'}


def _objective(mode: str) -> str:
    return {
        'patch_once': 'Apply one minimal production patch to test the selected root-cause hypothesis.',
        'explore_only': 'Explore the suspected production location without editing files.',
        'no_patch': 'Patch gate is closed; record why no patch worker should run.',
    }[mode]


def _instruction_id(mode: str, attempt: int) -> str:
    return {'explore_only': _aid('opencode_explore_instruction', attempt),
            'patch_once': _aid('opencode_patch_instruction', attempt),
            'no_patch': _aid('opencode_no_patch_instruction', attempt)}[mode]


def _previous_failure_penalty(rel: str, symbol: dict[str, Any], memory: dict[str, Any]) -> int:
    key = f"{rel}:{symbol.get('symbol')}"
    return 3 if any(key in str(item) for item in memory.get('failed_patch_summaries') or []) else 0


def _context_names(rag: dict[str, Any]) -> set[str]:
    names = set()
    for context in rag.get('contexts') if isinstance(rag.get('contexts'), list) else []:
        if isinstance(context, dict):
            names.update(str(context.get(key)).strip() for key in ('file_name', 'filename', 'display_name')
                         if context.get(key))
    return {item for item in names if item}


def _context_text_status(case: dict[str, Any], rag: dict[str, Any]) -> dict[str, Any]:
    refs = clean_contexts(case.get('reference_context'))
    contexts = '\n'.join(clean_contexts(rag.get('contexts')))
    if not refs:
        return {'status': 'unknown', 'hits': [], 'missing': [], 'unknown_reason': 'no_reference_context'}
    hits = [text[:80] for text in refs if text and text[:80] in contexts]
    missing = [text[:80] for text in refs if text and text[:80] not in contexts]
    status = 'hit' if hits and not missing else 'partial' if hits else 'miss'
    return {'status': status, 'hits': hits, 'missing': missing, 'unknown_reason': ''}


def _hitish(status: dict[str, Any]) -> bool:
    return status.get('status') in {'hit', 'partial'}


def _stage_confidence(ref_docs: set[str], ref_chunks: set[str], nodes: list[dict[str, Any]],
                      fine: dict[str, Any], final: dict[str, Any]) -> str:
    if ref_docs and ref_chunks and nodes and (fine.get('evidence') or {}).get('fine_rule_hits') is not None:
        return 'high'
    if nodes and (_hitish(final['name']) or _hitish(final['context_text'])):
        return 'medium'
    return 'low' if nodes else 'insufficient'


def _secondary_failures(primary: str, final: dict[str, Any], judge: dict[str, Any]) -> list[dict[str, Any]]:
    if primary == 'tool_execution_error':
        return [{'kind': 'tool_result_integration_candidate',
                 'reason': 'tool error may coexist with successful output', 'priority': 2}]
    if final['chunk']['status'] in {'hit', 'partial'} and float(judge.get('faithfulness') or 0.0) < 0.8:
        return [{'kind': 'generation_candidate', 'reason': 'context available but answer not faithful', 'priority': 2}]
    return []


def _fine_evidence_refs(fine: dict[str, Any]) -> list[dict[str, Any]]:
    evidence = fine.get('evidence') if isinstance(fine.get('evidence'), dict) else {}
    return [{'type': 'fine_evidence', 'field': key} for key in ('fine_rule_hits', 'coarse_rule_hits')
            if evidence.get(key)]
