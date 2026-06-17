from __future__ import annotations

import hashlib
import json
import os
import re
import shlex
import shutil
import subprocess
import time
import urllib.error
import urllib.parse
import urllib.request
from collections import Counter
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Mapping

from evo.artifact_runtime import ArtifactPayload, ExternalCallRequest, ExternalCallResult
from evo.operations.repair.opencode import run_opencode_streaming, trace_payload

QUESTION_TYPES = ('single_hop', 'single_doc_multi_hop', 'multi_doc_multi_hop', 'table_list', 'formula')
DIFFICULTIES = ('easy', 'medium', 'hard')
METRICS = ('answer_correctness', 'faithfulness', 'doc_recall', 'context_recall')
DEFAULT_KB_GROUPS = ('block', 'line', 'doc-summary')
REPAIR_COPY_DIRS = ('lazymind', 'chat', 'common', 'vocab', 'parsing', 'processor')
REPAIR_COPY_FILES = ('.dockerignore', 'Dockerfile', 'config.py', 'requirements.txt')
OLD_CONFIG_POST_ACTION = """def _model_config_path_post_action(resolved_path):
    if not resolved_path: return
    lazyllm.config['auto_model_config_map_path'] = str(resolved_path)"""
NEW_CONFIG_POST_ACTION = """def _model_config_path_post_action(resolved_path):
    if not resolved_path: return
    value = str(resolved_path)
    lazyllm.config._impl['auto_model_config_map_path'] = value"""
_DOCUMENTS: dict[tuple[str, str], Any] = {}


def payload(schema: str, value: Mapping[str, Any] | list[Any]) -> ArtifactPayload:
    return ArtifactPayload(schema, value)


def load_corpus(source_config: Mapping[str, Any]) -> ArtifactPayload:
    dataset_id = _text(source_config.get('dataset_id') or source_config.get('kb_id') or 'algo')
    docs, load_mode, errors = _input_documents(source_config, dataset_id), 'inline', []
    if not docs:
        docs, load_mode = _kb_documents(source_config, dataset_id), 'lazyllm_document'
    if not docs:
        raise ValueError(f'dataset {dataset_id} has no usable source units')
    unique_docs = sorted({_text(doc.get('doc_id')) for doc in docs if _text(doc.get('doc_id'))})
    page_size = _int_between(source_config.get('document_page_size') or source_config.get('page_size'), 200, 1, 5000)
    pages = [{
        'source_id': dataset_id,
        'page_index': index,
        'documents': page,
    } for index, page in enumerate(_chunks(docs, page_size), 1)]
    return payload('CorpusLoadReport', {
        'dataset_id': dataset_id,
        'sources': [{'source_id': dataset_id, 'type': load_mode, 'document_count': len(unique_docs)}],
        'document_pages': pages,
        'stats': {
            'source_count': 1,
            'loaded_doc_count': len(unique_docs),
            'source_unit_count': len(docs),
            'document_page_count': len(pages),
        },
        'skipped': [],
        'errors': errors,
    })


def build_corpus_snapshot(report: Mapping[str, Any], source_config: Mapping[str, Any]) -> ArtifactPayload:
    raw_docs = [
        doc
        for page in report.get('document_pages', [])
        for doc in page.get('documents', [])
        if isinstance(doc, Mapping)
    ]
    units = [
        _unit(doc, index)
        for index, doc in enumerate(raw_docs, 1)
    ]
    if not units:
        raise ValueError('corpus load report has no loaded documents')
    by_type = Counter(unit['unit_type'] for unit in units)
    return payload('CorpusSnapshot', {
        'dataset_id': _text(
            report.get('dataset_id')
            or source_config.get('dataset_id')
            or source_config.get('kb_id')
            or 'algo',
        ),
        'source_units': units,
        'source_unit_count': len(units),
        'unit_type_counts': dict(by_type),
        'source_report': {'stats': dict(report.get('stats') or {})},
    })


def prepare_case(config: Mapping[str, Any], snapshot: Mapping[str, Any], case_id: str) -> ArtifactPayload:
    units = list(snapshot.get('source_units') or [])
    if not units:
        raise ValueError('corpus snapshot has no source units')
    index = _case_index(case_id)
    qtype = _choice(config.get('question_types'), QUESTION_TYPES, index)
    difficulty = _choice(config.get('difficulties'), DIFFICULTIES, index)
    selected = _select_units(units, qtype, index)
    refs = [{
        'chunk_id': unit['chunk_id'],
        'doc_id': unit['doc_id'],
        'filename': unit['filename'],
        'content_preview': _clip(unit['content'], 800),
        'unit_type': unit['unit_type'],
    } for unit in selected]
    return payload('CasePreparation', {
        'case_id': case_id,
        'question_type': qtype,
        'difficulty': difficulty,
        'doc_reference': _unique_docs(selected),
        'context_reference': refs,
        'instruction': f'Generate a {difficulty} {qtype} evaluation case grounded in the selected evidence.',
        'source_snapshot_dataset_id': _text(snapshot.get('dataset_id')),
        'source_message_id': _text(config.get('source_message_id')),
    })


def generate_case(preparation: Mapping[str, Any]) -> ArtifactPayload:
    case_id = _text(preparation['case_id'])
    contexts = [item for item in preparation.get('context_reference', []) if isinstance(item, Mapping)]
    first = contexts[0] if contexts else {}
    filename = _text(first.get('filename') or 'the selected source')
    evidence = _text(first.get('content_preview') or 'No evidence text was available.')
    question = _question_from_evidence(filename, evidence)
    answer = _answer_from_evidence(evidence)
    return payload('DatasetCase', {
        'id': case_id,
        'question': question,
        'answer': answer,
        'question_type': _text(preparation.get('question_type')),
        'difficulty': _text(preparation.get('difficulty')),
        'grading_guidance': 'The answer should match the grounded reference evidence and avoid unsupported facts.',
        'reference_context': [_text(item.get('content_preview')) for item in contexts],
        'reference_doc': [_text(item.get('filename')) for item in contexts],
        'reference_doc_ids': [_text(item.get('doc_id')) for item in contexts],
        'reference_chunk_ids': [_text(item.get('chunk_id')) for item in contexts],
        'source_preparation': preparation,
        'source_message_id': _text(preparation.get('source_message_id')),
    })


def assemble_dataset(cases: Mapping[str, ArtifactPayload]) -> ArtifactPayload:
    rows = [_case_payload(case_id, item.payload) for case_id, item in sorted(cases.items())]
    checks = _dataset_checks(rows)
    return payload('EvalDataset', {
        'id': 'eval.dataset',
        'size': len(rows),
        'case_ids': [row['id'] for row in rows],
        'stats': {
            'question_type_counts': dict(Counter(row['question_type'] for row in rows)),
            'difficulty_counts': dict(Counter(row['difficulty'] for row in rows)),
        },
        'checks': checks,
        'preview': [{key: row[key] for key in ('id', 'question', 'question_type', 'difficulty')} for row in rows],
        'cases': rows,
    })


def rag_answer(case: Mapping[str, Any], target_config: Mapping[str, Any], ctx: Any) -> ArtifactPayload:
    case_id = _text(case['id'])
    target_url = _text(target_config.get('target_chat_url'))
    dataset_id = _text(target_config.get('dataset_id') or target_config.get(
        'kb_id') or target_config.get('dataset_name'))
    question = _text(case.get('question'))
    request_payload = {
        'query': question,
        'history': [],
        'trace': bool(target_config.get('require_trace', True)),
        'dataset': dataset_id,
        'filters': {'kb_id': [dataset_id]} if dataset_id else {},
        'reasoning': False,
        'disabled_tools': [
            'temp_kb',
            'wikipedia',
            'web_search',
            'academic_search',
            'url_fetch',
            'multimodal',
            'vocab_learn',
            'memory_editor',
            'skill_editor',
            'feishu',
        ],
    }
    if algorithm_id := _text(target_config.get('algorithm_id')):
        request_payload['algorithm_id'] = algorithm_id
    model_config = getattr(ctx, 'model_config', None) or {}
    model_identity = _model_config_identity(model_config)
    call_payload = {**request_payload, 'llm_config': model_config or None}
    call_identity = {'target_chat_url': target_url, 'payload': request_payload, 'model_config': model_identity}
    result = (
        ctx.external.call(
            call_id=f'rag_answer:{case_id}',
            payload={
                'target_chat_url': target_url,
                'payload': call_payload},
            runner=HttpChatRunner(),
            idempotency_key=f'{case_id}:rag:{_stable_text(call_identity)}',
            payload_fingerprint=_stable_text(call_identity),
            metadata={
                'kind': 'rag_answer',
                'case_id': case_id},
        ) if target_url else ExternalCallResult(
            'failed_permanent',
            error_type='missing_target_chat_url',
            error_message='target_chat_url is empty'))
    value = result.value if result.status == 'completed' and isinstance(result.value, Mapping) else {}
    chat_error = None if result.status == 'completed' else {'type': result.error_type, 'message': result.error_message}
    answer = _text(value.get('answer') or value.get('text'))
    routed_algorithm_id = _text(value.get('routed_algorithm_id'))
    if algorithm_id and routed_algorithm_id and routed_algorithm_id != algorithm_id:
        chat_error = {
            'type': 'candidate_route_mismatch',
            'message': f'expected {algorithm_id}, got {routed_algorithm_id}',
        }
    contexts = [str(item) for item in value.get('contexts') or value.get('sources') or []]
    source_doc_ids, source_chunk_ids = _source_ids(
        [*(_as_list(value.get('sources'))), *(_as_list(value.get('contexts')))])
    doc_ids = _unique_texts([*_as_list(value.get('doc_ids') or value.get('document_ids')), *source_doc_ids])
    chunk_ids = _unique_texts([*_as_list(value.get('chunk_ids') or value.get('segment_ids')
                              or value.get('segement_ids')), *source_chunk_ids])
    return payload('RagAnswer', {
        'case_id': case_id,
        'case': case,
        'question': question,
        'answer': answer,
        'status': 'ok' if answer and chat_error is None else 'failed',
        'chat_error': chat_error,
        'contexts': contexts,
        'doc_ids': doc_ids,
        'chunk_ids': chunk_ids,
        'trace_id': _text(value.get('trace_id')),
        'evidence_status': 'found' if doc_ids or chunk_ids or contexts else 'no_evidence',
        'target': {
            'target_chat_url': target_url,
            'dataset_id': dataset_id,
            'require_trace': request_payload['trace'],
            **({'algorithm_id': algorithm_id} if algorithm_id else {}),
            **({'routed_algorithm_id': routed_algorithm_id} if routed_algorithm_id else {}),
        },
    })


def judge_answer(answer: Mapping[str, Any], policy: Mapping[str, Any]) -> ArtifactPayload:
    case = answer.get('case') if isinstance(answer.get('case'), Mapping) else {}
    case_id = _text(answer.get('case_id') or case.get('id'))
    if answer.get('status') == 'failed' or answer.get('chat_error'):
        err = answer.get('chat_error') if isinstance(answer.get('chat_error'), Mapping) else {}
        reason = f"{_text(err.get('type') or 'ChatError')}: {_text(err.get('message') or 'RAG call failed')}"
        scores = dict.fromkeys(('answer_correctness', 'faithfulness', 'doc_recall', 'context_recall'), 0.0)
        quality, failure = 'bad', 'infra_failure'
    else:
        reference = _norm(_text(case.get('answer')))
        actual = _norm(_text(answer.get('answer')))
        exact = bool(reference and actual and (reference in actual or actual in reference))
        doc_recall = _recall(case.get('reference_doc_ids'), answer.get('doc_ids'))
        chunk_recall = _recall(case.get('reference_chunk_ids'), answer.get('chunk_ids'))
        correctness = 1.0 if exact else 0.4 if actual else 0.0
        faithfulness = max(doc_recall, chunk_recall, 0.5 if answer.get('contexts') else 0.0)
        scores = {
            'answer_correctness': correctness,
            'faithfulness': round(faithfulness, 4),
            'doc_recall': doc_recall,
            'context_recall': chunk_recall,
        }
        quality, failure, reason = _quality(scores, policy), 'none', 'deterministic quality check completed'
        if quality != 'good':
            failure = 'retrieval_or_generation_issue'
    return payload('JudgeResult', {
        'case_id': case_id,
        'case': case,
        'rag_answer': answer,
        **scores,
        'is_correct': scores['answer_correctness'] >= 0.8 and scores['faithfulness'] >= 0.8,
        'quality_label': quality,
        'failure_type': failure,
        'reason': reason[:200],
        'defect': '' if quality == 'good' else failure,
        'trace_id': _text(answer.get('trace_id')),
        'target': dict(answer.get('target') or {}) if isinstance(answer.get('target'), Mapping) else {},
        'evaluation_policy': dict(policy),
        'judge_contexts': list(answer.get('contexts') or []),
    })


def eval_summary(judges: Mapping[str, ArtifactPayload]) -> ArtifactPayload:
    rows = [_judge_row(case_id, item.payload) for case_id, item in sorted(judges.items())]
    scored = [row for row in rows if row['failure_type'] != 'infra_failure']
    metrics = {
        'scored_count': len(scored),
        'correct_count': sum(row['is_correct'] for row in scored),
        'correct_rate': _avg(1.0 if row['is_correct'] else 0.0 for row in scored),
        **{f'{key}_avg': _avg(row[key] for row in scored) for key in METRICS},
    }
    return payload('EvalSummary', {
        'id': 'eval.summary',
        'total': len(rows),
        'case_ids': [row['case_id'] for row in rows],
        'metrics': metrics,
        'quality_counts': dict(Counter(row['quality_label'] for row in rows)),
        'failure_type_counts': dict(Counter(row['failure_type'] for row in rows)),
        'bad_cases': [
            {key: row[key] for key in ('case_id', 'quality_label', 'failure_type', 'reason', 'trace_id')}
            for row in rows
            if row['quality_label'] != 'good'
        ],
        'execution_failures': [
            {'case_id': row['case_id'], 'reason': row['reason']}
            for row in rows
            if row['failure_type'] == 'infra_failure'
        ],
        'checks': {
            'ready': not any(row['failure_type'] == 'infra_failure' for row in rows),
            'errors': [],
            'warnings': [],
        },
        'rows': rows,
    })


def classify_case(case: Mapping[str, Any], answer: Mapping[str, Any], judge: Mapping[str, Any]) -> ArtifactPayload:
    case_id = _text(case.get('id') or judge.get('case_id') or answer.get('case_id'))
    failure = _text(judge.get('failure_type') or 'unknown')
    quality = _text(judge.get('quality_label') or 'bad')
    if failure == 'infra_failure':
        category, repairable = 'infra_failure', False
    elif quality == 'good':
        category, repairable = 'none', False
    elif float(judge.get('doc_recall') or 0) == 0 or float(judge.get('context_recall') or 0) == 0:
        category, repairable = 'retrieval_issue', True
    else:
        category, repairable = 'generation_issue', True
    return payload('CaseClassification', {
        'case_id': case_id,
        'coarse_category': category,
        'fine_category': category,
        'repairable': repairable,
        'confidence': 'high' if category in {'none', 'infra_failure'} else 'medium',
        'reason': _text(judge.get('reason') or failure),
        'case': case,
        'rag_answer': answer,
        'judge': judge,
    })


def analysis_summary(classifications: Mapping[str, ArtifactPayload]) -> ArtifactPayload:
    rows = [dict(item.payload) for _, item in sorted(classifications.items())]
    return payload('AnalysisSummary', {
        'id': 'analysis.summary',
        'case_ids': [_text(row.get('case_id')) for row in rows],
        'total': len(rows),
        'category_counts': dict(Counter(_text(row.get('coarse_category')) for row in rows)),
        'repairable_cases': [
            {'case_id': row['case_id'], 'category': row['coarse_category'], 'reason': row.get('reason', '')}
            for row in rows
            if row.get('repairable')
        ],
        'infra_failures': [row['case_id'] for row in rows if row.get('coarse_category') == 'infra_failure'],
        'rows': rows,
    })


def repair_plan(analysis: Mapping[str, Any], policy: Mapping[str, Any]) -> ArtifactPayload:
    repairable = list(analysis.get('repairable_cases') or [])
    status = 'planned' if repairable else 'skipped_no_repairable_case'
    rows = [row for row in _as_list(analysis.get('rows')) if isinstance(row, Mapping)]
    return payload('RepairPlan', {
        'status': status,
        'target_cases': repairable,
        'policy': dict(policy),
        'analysis_summary': {'category_counts': dict(analysis.get('category_counts') or {})},
        'evidence_cases': [_repair_case_evidence(row) for row in rows if _repair_case_selected(row, repairable)],
    })


def candidate_workspace(plan: Mapping[str, Any], ctx: Any | None = None) -> ArtifactPayload:
    if plan.get('status') != 'planned':
        return payload('CandidateWorkspace', {'status': 'skipped',
                       'repair_plan': plan, 'workspace_kind': 'artifact_runtime'})
    policy = plan.get('policy') if isinstance(plan.get('policy'), Mapping) else {}
    source = _algorithm_source_root(policy.get('candidate_source_dir')
                                    or os.getenv('LAZYMIND_EVO_CHAT_SOURCE') or '/app/algorithm')
    workspace = _repair_workspace(policy, ctx, plan)
    _prepare_repair_workspace(source, workspace)
    return payload('CandidateWorkspace', {
        'status': 'ready',
        'repair_plan': plan,
        'workspace_kind': 'artifact_runtime',
        'workspace_ref': str(workspace),
        'source_dir': str(source),
        'git_head': _git(workspace, 'rev-parse', '--verify', 'HEAD'),
    })


def repair_loop(workspace: Mapping[str, Any], ctx: Any | None = None) -> ArtifactPayload:
    planned = ((workspace.get('repair_plan') or {}).get('status') == 'planned')
    if not planned:
        return payload('RepairLoopResult', {'status': 'skipped', 'attempts': [], 'diagnostics': [], 'message': ''})
    ctx.raise_if_cancelled() if ctx is not None else None
    root = Path(_text(workspace.get('workspace_ref'))).resolve()
    plan = workspace.get('repair_plan') if isinstance(workspace.get('repair_plan'), Mapping) else {}
    policy = plan.get('policy') if isinstance(plan.get('policy'), Mapping) else {}
    attempts = []
    session_id = ''
    max_attempts = _int_between(policy.get('repair_attempt_budget') or os.getenv('EVO_REPAIR_ATTEMPT_BUDGET'), 1, 1, 5)
    for attempt in range(1, max_attempts + 1):
        ctx.raise_if_cancelled() if ctx is not None else None
        diagnosis = _repair_diagnosis(plan, policy, ctx, attempt)
        task = _opencode_task(plan, workspace, diagnosis, attempt)
        result = run_opencode_streaming(
            container='',
            workdir=str(root),
            prompt=json.dumps(task, ensure_ascii=False, indent=2),
            artifact_dir=root / '.evo_repair_logs' / 'opencode' / f'attempt_{attempt}',
            session_id=session_id,
            env=_opencode_env_from_context(ctx),
            timeout_s=_int_between(policy.get('opencode_timeout_s') or os.getenv(
                'LAZYMIND_EVO_CODE_TIMEOUT_S'), 900, 30, 7200),
            first_response_timeout_s=_int_between(
                policy.get('opencode_first_response_timeout_s') or os.getenv(
                    'LAZYMIND_EVO_CODE_FIRST_RESPONSE_TIMEOUT_S'),
                300,
                10,
                1800,
            ),
        )
        session_id = result.session_id or session_id
        trace = trace_payload(result, 'repair.plan', attempt)
        diff = _git(root, 'diff', '--')
        files = _git(root, 'diff', '--name-only').splitlines()
        verification = _verify_repair_workspace(root, policy)
        diff_scope = _diff_scope(files, policy)
        status = 'patched' if diff.strip(
        ) and verification['status'] == 'passed' and diff_scope['status'] == 'passed' else 'failed'
        attempts.append({
            'attempt': attempt,
            'status': status,
            'diagnosis': diagnosis,
            'opencode_trace': trace,
            'files_changed': files,
            'diff': diff,
            'verification': verification,
            'diff_scope': diff_scope,
            'failure': '' if status == 'patched' else _repair_failure(result, diff, verification, diff_scope),
        })
        if status == 'patched':
            break
    return payload('RepairLoopResult', {
        'status': 'patched' if attempts and attempts[-1]['status'] == 'patched' else 'no_patch_generated',
        'attempts': attempts,
        'diagnostics': list(plan.get('target_cases') or []),
        'workspace_ref': str(root),
        'message': (
            'Repair loop produced a candidate patch.'
            if attempts and attempts[-1]['status'] == 'patched'
            else 'Repair loop ran, but no verified patch was produced.'
        ),
    })


def verified_patch(loop: Mapping[str, Any]) -> ArtifactPayload:
    attempts = [item for item in _as_list(loop.get('attempts')) if isinstance(item, Mapping)]
    winner = next((item for item in reversed(attempts) if item.get('status') == 'patched'), {})
    status = 'verified' if winner else 'skipped' if loop.get('status') == 'skipped' else 'no_patch'
    diff = _text(winner.get('diff')) if winner else ''
    return payload('VerifiedRepair', {
        'status': status,
        'diff': diff,
        'patch': diff,
        'content': diff or 'No verified code changes were produced for this repair step.\n',
        'repair_loop': loop,
        'workspace_ref': _text(loop.get('workspace_ref')),
        'files': list(winner.get('files_changed') or []),
        'winning_attempt': winner.get('attempt') if winner else None,
    })


def candidate_service(config: Mapping[str, Any], patch: Mapping[str, Any], ctx: Any | None = None) -> ArtifactPayload:
    skipped = patch.get('status') != 'verified'
    return payload('CandidateService', {
        **({'status': 'skipped'} if skipped else _start_candidate_algorithm(config, patch, ctx)),
        'candidate_config': dict(config),
        'patch_status': _text(patch.get('status')),
        **({'healthcheck': {'status': 'skipped'}} if skipped else {}),
    })


def candidate_rag_answer(
    case: Mapping[str, Any],
    service: Mapping[str, Any],
    ctx: Any | None = None,
) -> ArtifactPayload:
    if service.get('status') == 'skipped':
        return payload('CandidateRagAnswer', {
            'case_id': _text(case.get('id')),
            'case': case,
            'status': 'skipped',
            'answer': '',
            'service_status': 'skipped',
        })
    _ensure_candidate_service_ready(service)
    target = {
        'target_chat_url': _text(service.get('service_url')),
        'dataset_id': _case_dataset_id(case) or _text(service.get('dataset_id')),
        'require_trace': True,
        'algorithm_id': _text(service.get('algorithm_id')),
    }
    answer = dict(rag_answer(case, target, ctx).payload)
    answer['candidate_service'] = {
        'algorithm_id': target['algorithm_id'],
        'service_url': target['target_chat_url'],
        'router_admin_url': _text(service.get('router_admin_url')),
    }
    return payload('CandidateRagAnswer', answer)


def candidate_judge(answer: Mapping[str, Any], policy: Mapping[str, Any] | None = None) -> ArtifactPayload:
    skipped = answer.get('status') == 'skipped'
    if not skipped:
        return payload('CandidateJudgeResult', judge_answer(answer, policy or {}).payload)
    return payload('CandidateJudgeResult', {
        'case_id': _text(answer.get('case_id')),
        'answer_correctness': 0.0,
        'faithfulness': 0.0,
        'doc_recall': 0.0,
        'context_recall': 0.0,
        'quality_label': 'skipped' if skipped else 'bad',
        'failure_type': 'candidate_not_run' if skipped else 'candidate_failed',
        'is_correct': False,
        'reason': 'candidate evaluation skipped' if skipped else 'candidate evaluation did not produce an answer',
    })


def candidate_summary(judges: Mapping[str, ArtifactPayload]) -> ArtifactPayload:
    rows = [dict(item.payload) for _, item in sorted(judges.items())]
    metrics = _summary_metrics(rows)
    failures = _candidate_execution_failures(rows)
    return payload('CandidateEvalSummary', {
        'id': 'abtest.candidate_eval_summary',
        'case_ids': [_text(row.get('case_id')) for row in rows],
        'total': len(rows),
        'metrics': metrics,
        'quality_counts': dict(Counter(_text(row.get('quality_label')) for row in rows)),
        'failure_type_counts': dict(Counter(_text(row.get('failure_type')) for row in rows)),
        'execution_failures': failures,
        'checks': {
            'ready': not failures and metrics['scored_count'] == len(rows) and bool(rows),
            'errors': [{'code': 'candidate_execution_failed', **item} for item in failures],
            'warnings': [],
        },
        'rows': rows,
    })


def compare_abtest(baseline: Mapping[str, Any], candidate: Mapping[str, Any]) -> ArtifactPayload:
    skipped = candidate.get('quality_counts', {}).get('skipped', 0) == candidate.get('total', 0)
    candidate_failed = _candidate_summary_failed(candidate)
    case_ids = list(dict.fromkeys([*_as_list(baseline.get('case_ids')), *_as_list(candidate.get('case_ids'))]))
    baseline_metrics = _ab_metrics(baseline.get('metrics') or {})
    candidate_metrics = _ab_metrics(candidate.get('metrics') or {})
    delta = {key: round(candidate_metrics[key] - baseline_metrics[key], 4) for key in baseline_metrics}
    reasons = (
        ['candidate evaluation was skipped because no verified repair patch is available']
        if skipped
        else [
            'candidate evaluation produced no scored cases; inspect candidate execution_failures'
        ] if candidate_failed else []
    )
    decision = {
        'status': 'skipped' if skipped else 'candidate_eval_failed' if candidate_failed else 'review_candidate',
        'primary_metric': 'answer_correctness',
        'reasons': reasons,
    }
    return payload('ABTestComparison', {
        'id': 'abtest.comparison',
        'status': 'skipped' if skipped else 'failed' if candidate_failed else 'completed',
        'verdict': decision['status'],
        'case_ids': case_ids,
        'case_count': len(case_ids),
        'metrics': {'baseline': baseline_metrics, 'candidate': candidate_metrics, 'delta': delta},
        'case_deltas': [
            {
                'case_id': case_id,
                'outcome': 'unchanged',
                'before': baseline_metrics,
                'after': candidate_metrics,
                'delta': delta,
            }
            for case_id in case_ids
        ],
        'goodcase_guard': {'status': 'skipped' if skipped else 'not_evaluated', 'violations': []},
        'policy': {'primary_metric': 'answer_correctness', 'guard_metrics': ['faithfulness', 'context_recall']},
        'decision': decision,
        'reasons': reasons,
        'missing_metrics': [],
        'baseline': {
            'total': baseline.get('total', 0),
            'quality_counts': dict(baseline.get('quality_counts') or {}),
        },
        'candidate': {
            'total': candidate.get('total', 0),
            'quality_counts': dict(candidate.get('quality_counts') or {}),
        },
        'summary': {
            'metrics': {'baseline': baseline_metrics, 'candidate': candidate_metrics, 'delta': delta},
            'case_deltas': [],
            'goodcase_guard': {'status': 'skipped' if skipped else 'not_evaluated', 'violations': []},
            'decision': decision,
            'policy': {'primary_metric': 'answer_correctness', 'guard_metrics': ['faithfulness', 'context_recall']},
            'case_count': len(case_ids),
            'reasons': reasons,
            'missing_metrics': [],
        },
    })


def _start_candidate_algorithm(config: Mapping[str, Any], patch: Mapping[str, Any], ctx: Any | None) -> dict[str, Any]:
    workspace = Path(_text(patch.get('workspace_ref'))).resolve()
    chat_path = workspace / 'lazymind' / 'chat'
    if not (chat_path / 'app.py').exists():
        raise RuntimeError(f'candidate chat app not found in verified patch workspace: {chat_path}')
    _normalize_candidate_config(workspace / 'lazymind' / 'config.py')
    target_url = _text(config.get('target_chat_url') or os.getenv(
        'LAZYMIND_EVO_TARGET_CHAT_URL') or 'http://chat:8046/api/chat/stream')
    router_admin_url = _text(config.get('router_admin_url') or os.getenv(
        'LAZYMIND_EVO_ROUTER_ADMIN_URL') or _origin(target_url))
    if not router_admin_url:
        raise RuntimeError('router_admin_url is required to start candidate service')
    algorithm_id = _candidate_algorithm_id(config, patch, ctx)
    env = _candidate_algorithm_env(config, algorithm_id)
    request_body = {
        'id': algorithm_id,
        'name': algorithm_id,
        'code_path': str(chat_path),
        'instance_count': _int_between(config.get('instance_count'), 1, 1, 4),
        'config': env,
    }
    result = _external_or_direct(
        ctx,
        call_id='candidate_service:register',
        payload={'router_admin_url': router_admin_url, 'algorithm_id': algorithm_id, 'body': request_body},
        runner=RouterCandidateRegisterRunner(timeout_s=_int_between(config.get('startup_timeout_s'), 180, 10, 900)),
        idempotency_key=(
            f'candidate-service:{algorithm_id}:'
            f'{_stable_text({"workspace": str(workspace), "diff": patch.get("diff")})}'
        ),
        metadata={'kind': 'candidate_service', 'algorithm_id': algorithm_id},
    )
    if result.status != 'completed' or not isinstance(result.value, Mapping):
        raise RuntimeError(f'candidate service startup failed: {result.error_type}: {result.error_message}')
    ports = list(result.value.get('ports') or [])
    if not ports:
        raise RuntimeError(f'candidate service registered without ports: {result.value}')
    return {
        'status': 'ready',
        'service_kind': 'router_algorithm',
        'algorithm_id': algorithm_id,
        'router_admin_url': router_admin_url,
        'service_url': target_url,
        'workspace_ref': str(workspace),
        'code_path': str(chat_path),
        'register_response': dict(result.value),
        'process': {'ports': ports},
        'healthcheck': {'status': 'passed', 'ports': ports},
    }


def _ensure_candidate_service_ready(service: Mapping[str, Any]) -> None:
    if service.get('status') != 'ready':
        raise RuntimeError(f"candidate service is not ready: {service.get('status')}")
    if (service.get('healthcheck') or {}).get('status') != 'passed':
        raise RuntimeError(f"candidate service healthcheck failed: {service.get('healthcheck')}")
    if not _text(service.get('algorithm_id')):
        raise RuntimeError('candidate service missing algorithm_id')
    if not _text(service.get('service_url')):
        raise RuntimeError('candidate service missing service_url')


def _candidate_execution_failures(rows: list[Mapping[str, Any]]) -> list[dict[str, str]]:
    bad_types = {'infra_failure', 'candidate_not_run', 'candidate_failed'}
    failures = []
    for row in rows:
        failure_type = _text(row.get('failure_type'))
        target = row.get('target') if isinstance(row.get('target'), Mapping) else {}
        expected, actual = _text(target.get('algorithm_id')), _text(target.get('routed_algorithm_id'))
        if failure_type in bad_types or (expected and actual and expected != actual):
            failures.append({
                'case_id': _text(row.get('case_id')),
                'failure_type': failure_type or 'candidate_failed',
                'reason': _text(row.get('reason') or 'candidate evaluation failed'),
            })
    return failures


def _candidate_summary_failed(candidate: Mapping[str, Any]) -> bool:
    total = int(candidate.get('total') or 0)
    metrics = candidate.get('metrics') if isinstance(candidate.get('metrics'), Mapping) else {}
    scored = int(metrics.get('scored_count') or 0)
    checks = candidate.get('checks') if isinstance(candidate.get('checks'), Mapping) else {}
    return bool(candidate.get('execution_failures')) or not checks.get('ready') or scored == 0 or scored != total


def _candidate_algorithm_env(config: Mapping[str, Any], algorithm_id: str) -> dict[str, str]:
    env = {
        'LAZYMIND_ALGO_ID': algorithm_id,
        'LAZYMIND_AGENTIC_KB_NAME': _text(
            config.get('agentic_kb_name')
            or os.getenv('LAZYMIND_AGENTIC_KB_NAME')
            or 'general_algo',
        ),
        'LAZYMIND_ENABLE_ROUTER': 'false',
        'LAZYMIND_ROUTER_CHILD_PROXIED_ONLY': 'true',
    }
    for key in (
        'LAZYMIND_DOCUMENT_SERVER_URL',
        'LAZYMIND_DOCUMENT_PROCESSOR_URL',
        'LAZYMIND_SEGMENT_STORE_TYPE',
        'LAZYMIND_SEGMENT_STORE_URI_OR_PATH',
        'LAZYMIND_SHARED_UPLOAD_DIR',
        'LAZYMIND_MOUNT_BASE_DIR',
        'LAZYMIND_AGENTIC_WORKSPACE',
        'LAZYMIND_CORE_API_URL',
        'LAZYMIND_CORE_SERVICE_URL',
        'LAZYMIND_CORE_DATABASE_URL',
        'LAZYMIND_DATABASE_URL',
        'LAZYMIND_MODEL_CONFIG_PATH',
        'LAZYLLM_INIT_DOC',
        'LAZYLLM_TRACE_ENABLED',
        'LAZYLLM_TRACE_BACKEND',
        'LAZYLLM_TRACE_LOCAL_STORAGE_DIR',
        'LAZYLLM_TRACE_CONSUME_BACKEND',
    ):
        if value := _text(os.getenv(key)):
            env[key] = value
    extra = config.get('env') if isinstance(config.get('env'), Mapping) else {}
    return {**env, **{_text(key): _text(value) for key, value in extra.items() if _text(key) and _text(value)}}


def _candidate_algorithm_id(config: Mapping[str, Any], patch: Mapping[str, Any], ctx: Any | None) -> str:
    explicit = _text(config.get('algorithm_id'))
    if explicit:
        return _safe_id(explicit, 'evo_candidate')
    run_part = _safe_id(_text(getattr(ctx, 'output_partition', '')), 'run')
    digest = hashlib.sha1(_stable_text({'workspace': patch.get('workspace_ref'),
                          'diff': patch.get('diff')}).encode('utf-8')).hexdigest()[:10]
    return f'evo_{run_part}_{digest}'[:64]


def _external_or_direct(
    ctx: Any | None,
    *,
    call_id: str,
    payload: Mapping[str, Any],
    runner: Any,
    idempotency_key: str,
    metadata: Mapping[str, Any],
) -> ExternalCallResult:
    if ctx is not None and getattr(ctx, 'external', None) is not None:
        return ctx.external.call(
            call_id=call_id,
            payload=payload,
            runner=runner,
            idempotency_key=idempotency_key,
            payload_fingerprint=_stable_text(payload),
            metadata=metadata,
        )
    request = type('DirectExternalCallRequest', (), {'payload': payload})()
    return runner.invoke(request, _NoopToken())


def _case_dataset_id(case: Mapping[str, Any]) -> str:
    prep = case.get('source_preparation') if isinstance(case.get('source_preparation'), Mapping) else {}
    return _text(
        prep.get('source_snapshot_dataset_id')
        or case.get('dataset_id')
        or case.get('kb_id')
        or case.get('dataset_name')
    )


def _origin(url: str) -> str:
    parsed = urllib.parse.urlparse(url)
    if not parsed.scheme or not parsed.netloc:
        return ''
    return urllib.parse.urlunparse((parsed.scheme, parsed.netloc, '', '', '', ''))


def _safe_id(value: str, fallback: str) -> str:
    safe = re.sub(r'[^A-Za-z0-9_.-]+', '_', value).strip('._-')
    return safe or fallback


def _normalize_candidate_config(path: Path) -> None:
    if path.exists():
        text = path.read_text(encoding='utf-8')
        updated = text.replace(OLD_CONFIG_POST_ACTION, NEW_CONFIG_POST_ACTION)
        if updated != text:
            path.write_text(updated, encoding='utf-8')


def _router_get(router_admin_url: str, algorithm_id: str, token: Any, *, timeout_s: float) -> dict[str, Any]:
    try:
        return _request_json(
            'GET',
            f'{router_admin_url}/inner/algorithm/{urllib.parse.quote(algorithm_id)}',
            token,
            timeout_s=timeout_s,
        )
    except urllib.error.HTTPError as exc:
        if exc.code == 404:
            return {}
        raise


def _router_post_register(
    router_admin_url: str,
    body: Mapping[str, Any],
    token: Any,
    *,
    timeout_s: float,
) -> dict[str, Any]:
    return _request_json('POST', f'{router_admin_url}/inner/algorithm/register', token, body=body, timeout_s=timeout_s)


def _wait_router_algorithm_ready(
    router_admin_url: str,
    algorithm_id: str,
    token: Any,
    *,
    timeout_s: float,
) -> dict[str, Any]:
    deadline = time.time() + timeout_s
    last: dict[str, Any] = {}
    while time.time() < deadline:
        token.raise_if_cancelled()
        last = _router_get(router_admin_url, algorithm_id, token, timeout_s=10)
        instances = [item for item in _as_list(last.get('instances')) if isinstance(item, Mapping)]
        healthy = [item for item in instances if item.get('status') == 'healthy']
        if last.get('status') == 'active' and healthy:
            return {
                'status': 'ready',
                'algorithm_id': algorithm_id,
                'instances': healthy,
                'ports': [item.get('port') for item in healthy if item.get('port')],
            }
        time.sleep(1)
    raise TimeoutError(f'candidate algorithm did not become healthy: {algorithm_id}; last={last}')


def _ensure_existing_candidate_matches(existing: Mapping[str, Any], body: Mapping[str, Any]) -> None:
    expected_path = _text(body.get('code_path'))
    actual_path = _text(existing.get('code_path'))
    if expected_path and actual_path and expected_path != actual_path:
        raise RuntimeError(f'candidate algorithm_id already points to different code_path: {actual_path}')
    expected_config = body.get('config') if isinstance(body.get('config'), Mapping) else {}
    actual_config = existing.get('config') if isinstance(existing.get('config'), Mapping) else {}
    for key in ('LAZYMIND_ALGO_ID', 'LAZYMIND_ENABLE_ROUTER', 'LAZYMIND_ROUTER_CHILD_PROXIED_ONLY'):
        if _text(expected_config.get(key)) != _text(actual_config.get(key)):
            raise RuntimeError(f'candidate algorithm_id already has different config for {key}')


def _request_json(
    method: str,
    url: str,
    token: Any,
    *,
    body: Mapping[str, Any] | None = None,
    timeout_s: float = 30.0,
) -> dict[str, Any]:
    token.raise_if_cancelled()
    data = None if body is None else json.dumps(body, ensure_ascii=False).encode('utf-8')
    request = urllib.request.Request(url, data=data, method=method, headers={'content-type': 'application/json'})
    with urllib.request.urlopen(request, timeout=timeout_s) as response:
        raw = response.read().decode('utf-8', 'replace')
    value = json.loads(raw) if raw else {}
    if not isinstance(value, Mapping):
        raise RuntimeError(f'{method} {url} returned non-object JSON')
    return dict(value)


@dataclass(frozen=True)
class HttpChatRunner:
    timeout_s: float = 20.0
    max_attempts: int = 6
    backoff_s: float = 1.0

    def invoke(self, request: ExternalCallRequest, token: Any) -> ExternalCallResult:
        target_url = _text(request.payload.get('target_chat_url'))
        body = json.dumps(request.payload.get('payload') or {}, ensure_ascii=False).encode('utf-8')
        last_error: BaseException | None = None
        for attempt in range(1, max(1, self.max_attempts) + 1):
            try:
                token.raise_if_cancelled()
                req = urllib.request.Request(target_url, data=body, method='POST',
                                             headers={'content-type': 'application/json'})
                with urllib.request.urlopen(req, timeout=self.timeout_s) as response:
                    raw = response.read().decode('utf-8', 'replace')
                    routed_algorithm = response.headers.get('X-Algorithm-Id') or ''
                    routed_instance = response.headers.get('X-Instance-Host') or ''
                parsed = _parse_chat_response(raw)
                if routed_algorithm:
                    parsed['routed_algorithm_id'] = routed_algorithm
                if routed_instance:
                    parsed['routed_instance_host'] = routed_instance
                return ExternalCallResult('completed', parsed, metadata={'target_url': target_url, 'attempt': attempt})
            except urllib.error.HTTPError as exc:
                last_error = exc
                if exc.code not in {429, 502, 503, 504} or attempt >= self.max_attempts:
                    break
            except (urllib.error.URLError, OSError) as exc:
                last_error = exc
                if attempt >= self.max_attempts:
                    break
            token.raise_if_cancelled()
            time.sleep(min(self.backoff_s * (2 ** (attempt - 1)), 8.0))
        exc = last_error or RuntimeError('chat call failed')
        if isinstance(exc, urllib.error.HTTPError) and exc.code == 429:
            return ExternalCallResult('rate_limited', error_type=type(exc).__name__, error_message=str(exc))
        if isinstance(exc, TimeoutError):
            return ExternalCallResult('timeout', error_type=type(exc).__name__, error_message=str(exc))
        return ExternalCallResult('failed_transient', error_type=type(exc).__name__, error_message=str(exc))


@dataclass(frozen=True)
class RouterCandidateRegisterRunner:
    timeout_s: float = 180.0

    def invoke(self, request: ExternalCallRequest, token: Any) -> ExternalCallResult:
        router_admin_url = _text(request.payload.get('router_admin_url')).rstrip('/')
        algorithm_id = _text(request.payload.get('algorithm_id'))
        body = request.payload.get('body') if isinstance(request.payload.get('body'), Mapping) else {}
        try:
            existing = _router_get(router_admin_url, algorithm_id, token, timeout_s=10)
            if existing:
                _ensure_existing_candidate_matches(existing, body)
            registered = existing if existing.get('status') == 'active' and existing.get('instances') else None
            if registered is None:
                registered = _router_post_register(router_admin_url, body, token, timeout_s=self.timeout_s)
            ready = _wait_router_algorithm_ready(router_admin_url, algorithm_id, token, timeout_s=self.timeout_s)
            ports = ready.get('ports') or registered.get('ports') or [
                instance.get('port') for instance in _as_list(ready.get('instances')) if isinstance(instance, Mapping)
            ]
            return ExternalCallResult('completed', {
                'algorithm_id': algorithm_id,
                'ports': [port for port in ports if port],
                'registration': registered,
                'ready': ready,
            })
        except urllib.error.HTTPError as exc:
            return ExternalCallResult(
                'failed_permanent',
                error_type='HTTPError',
                error_message=f'{exc.code}: {exc.reason}',
            )
        except TimeoutError as exc:
            return ExternalCallResult('timeout', error_type='TimeoutError', error_message=str(exc))
        except (urllib.error.URLError, OSError, ValueError, RuntimeError) as exc:
            return ExternalCallResult('failed_transient', error_type=type(exc).__name__, error_message=str(exc))


class _NoopToken:
    def raise_if_cancelled(self) -> None:
        return None


def _input_documents(config: Mapping[str, Any], dataset_id: str) -> list[dict[str, str]]:
    docs = []
    for index, item in enumerate(_as_list(config.get('documents') or config.get('docs')), 1):
        if isinstance(item, Mapping):
            content = _text(item.get('content') or item.get('text'))
            if content:
                docs.append({
                    'doc_id': _text(item.get('doc_id') or item.get('id') or f'{dataset_id}_doc_{index}'),
                    'filename': _text(item.get('filename') or item.get('file_name') or f'{dataset_id}_{index}.txt'),
                    'content': content,
                })
    for index, source in enumerate(_as_list(config.get('sources')), len(docs) + 1):
        if isinstance(source, Mapping):
            content = _text(source.get('content') or source.get('text') or source.get('summary'))
            if content:
                docs.append({
                    'doc_id': _text(
                        source.get('doc_id') or source.get('source_id') or f'{dataset_id}_source_{index}',
                    ),
                    'filename': _text(
                        source.get('filename') or source.get('file_name') or f'{dataset_id}_source_{index}.txt',
                    ),
                    'content': content,
                })
    return docs


def _kb_documents(config: Mapping[str, Any], dataset_id: str) -> list[dict[str, Any]]:
    rows = _kb_document_rows(config, dataset_id)
    doc = _document_client()
    groups = tuple(_unique_texts(config.get('segment_groups') or config.get('groups'))) or DEFAULT_KB_GROUPS
    max_units = _int_between(config.get('max_source_units') or config.get('max_units'), 200, 1, 10000)
    page_size = _int_between(config.get('kb_page_size') or config.get('node_page_size'), 100, 1, 1000)
    min_chars = _int_between(config.get('min_segment_chars'), 80, 1, 100000)
    units, seen = [], set()
    for row in rows:
        for group in groups:
            offset = 0
            while len(units) < max_units:
                nodes, total = doc.get_nodes(
                    doc_ids=[row['doc_id']],
                    kb_id=dataset_id,
                    group=group,
                    limit=min(page_size, max_units - len(units)),
                    offset=offset,
                    return_total=True,
                    sort_by_number=True,
                )
                if not nodes:
                    break
                for node in nodes:
                    unit = _node_unit(dataset_id, group, node, row)
                    content = _text(unit.get('content'))
                    if len(content) < min_chars:
                        continue
                    key = _text(unit.get('chunk_id')) or hashlib.sha256(content.encode('utf-8')).hexdigest()
                    if key in seen:
                        continue
                    seen.add(key)
                    units.append(unit)
                    if len(units) >= max_units:
                        break
                offset += len(nodes)
                if offset >= int(total or offset):
                    break
            if len(units) >= max_units:
                break
        if len(units) >= max_units:
            break
    return units


def _kb_document_rows(config: Mapping[str, Any], dataset_id: str) -> list[dict[str, str]]:
    try:
        import psycopg
        from psycopg.rows import dict_row
    except ImportError as exc:
        raise RuntimeError('psycopg is required for LazyRAG dataset loading') from exc

    schema = _text(config.get('db_schema') or os.getenv('LAZYMIND_READONLY_SCHEMA') or 'public')
    max_docs = _int_between(config.get('max_docs'), 1000, 1, 100000)
    quoted_schema = schema.replace(chr(34), chr(34) + chr(34))
    table = (
        f'from "{quoted_schema}".lazyllm_kb_documents kb '
        f'join "{quoted_schema}".lazyllm_documents d on d.doc_id = kb.doc_id'
    )
    sql = f'select d.doc_id, d.filename, d.file_type {table} where kb.kb_id = %s order by kb.id limit %s'
    with psycopg.connect(_db_dsn(), row_factory=dict_row) as conn, conn.cursor() as cursor:
        cursor.execute(sql, (dataset_id, max_docs))
        rows = [
            {
                'doc_id': _text(row.get('doc_id')),
                'filename': _text(row.get('filename') or row.get('doc_id')),
                'file_type': _text(row.get('file_type')),
            }
            for row in cursor.fetchall()
            if _text(row.get('doc_id'))
        ]
    if not rows:
        raise ValueError(f'dataset {dataset_id} has no registered documents')
    return rows


def _db_dsn() -> str:
    raw = _text(os.getenv('LAZYMIND_READONLY_DB_DSN') or os.getenv('LAZYMIND_DATABASE_URL'))
    if raw.startswith('postgresql+psycopg://'):
        return 'postgresql://' + raw.removeprefix('postgresql+psycopg://')
    if raw.startswith('postgres+psycopg://'):
        return 'postgres://' + raw.removeprefix('postgres+psycopg://')
    return raw or 'host=db user=app password=app dbname=app port=5432 sslmode=disable connect_timeout=5'


def _document_client() -> Any:
    from lazyllm import Document
    from lazymind.config import config

    url = _config_value(config, 'agentic_kb_url').rstrip('/')
    name = _config_value(config, 'agentic_kb_name')
    if not url or not name:
        raise RuntimeError('LazyRAG document service config is missing')
    key = (url, name)
    if key not in _DOCUMENTS:
        _DOCUMENTS[key] = Document(url=f'{url}/_call', name=name)
    return _DOCUMENTS[key]


def _node_unit(dataset_id: str, group: str, node: Any, doc_row: Mapping[str, Any]) -> dict[str, Any]:
    metadata = getattr(node, 'metadata', {}) or {}
    global_metadata = getattr(node, 'global_metadata', {}) or {}
    if not isinstance(metadata, Mapping):
        metadata = {}
    if not isinstance(global_metadata, Mapping):
        global_metadata = {}
    doc_id = _text(doc_row.get('doc_id'))
    filename = _text(doc_row.get('filename')) or _first_text(
        global_metadata, 'file_name', 'display_name', 'filename') or f'{doc_id}.txt'
    chunk_id = _text(getattr(node, 'uid', '')) or _text(metadata.get('uid')) or hashlib.sha256(
        _text(getattr(node, 'text', '')).encode('utf-8')).hexdigest()
    content = _text(getattr(node, 'text', ''))
    return {
        'source_unit_ref': f'{dataset_id}:{doc_id}:segment:{chunk_id}',
        'doc_ref': f'{dataset_id}:{doc_id}',
        'doc_id': doc_id,
        'filename': filename,
        'chunk_id': chunk_id,
        'group': _text(getattr(node, 'group', '')) or group,
        'unit_type': _unit_type(content, metadata),
        'content': content,
        'metadata': _json_safe({
            'node': metadata,
            'document': global_metadata,
            'number': getattr(node, 'number', None),
        }),
    }


def _unit(doc: Mapping[str, Any], index: int) -> dict[str, str]:
    content = _text(doc.get('content'))
    doc_id = _text(doc.get('doc_id') or f'doc_{index}')
    filename = _text(doc.get('filename') or f'{doc_id}.txt')
    return {
        'source_unit_ref': _text(doc.get('source_unit_ref')) or f'source_unit:{doc_id}:{index}',
        'doc_ref': _text(doc.get('doc_ref')) or f'doc:{doc_id}',
        'doc_id': doc_id,
        'filename': filename,
        'chunk_id': _text(doc.get('chunk_id') or f'{doc_id}:chunk:{index}'),
        'unit_type': _text(doc.get('unit_type')) or _unit_type(content),
        'content': content,
    }


def _select_units(units: list[Mapping[str, Any]], qtype: str, index: int) -> list[Mapping[str, Any]]:
    if qtype == 'multi_doc_multi_hop':
        docs = {}
        for unit in units:
            docs.setdefault(_text(unit.get('doc_id')), unit)
        selected = list(docs.values())[:2]
        return selected or [units[index % len(units)]]
    if qtype == 'single_doc_multi_hop' and len(units) > 1:
        by_doc: dict[str, list[Mapping[str, Any]]] = {}
        for unit in units:
            by_doc.setdefault(_text(unit.get('doc_id')), []).append(unit)
        same_doc = [items for items in by_doc.values() if len(items) > 1]
        if same_doc:
            selected = same_doc[index % len(same_doc)]
            return selected[:2]
    if qtype == 'table_list' and len(units) > 1:
        start = index % len(units)
        return [units[start], units[(start + 1) % len(units)]]
    return [units[index % len(units)]]


def _case_payload(case_id: str, value: Any) -> dict[str, Any]:
    if not isinstance(value, Mapping):
        raise ValueError(f'DatasetCase payload for {case_id} must be an object')
    row = dict(value)
    if row.get('id') != case_id:
        raise ValueError(f"case partition mismatch: {case_id} != {row.get('id')}")
    return row


def _dataset_checks(rows: list[Mapping[str, Any]]) -> dict[str, Any]:
    duplicates = [question for question, count in Counter(
        _norm(row.get('question')) for row in rows).items() if question and count > 1]
    return {
        'ready': not duplicates and bool(rows),
        'errors': [{'code': 'duplicate_question', 'message': question} for question in duplicates],
        'warnings': [
            {'code': 'missing_reference', 'case_id': row['id']}
            for row in rows
            if not row.get('reference_chunk_ids')
        ],
    }


def _judge_row(case_id: str, value: Any) -> dict[str, Any]:
    if not isinstance(value, Mapping):
        raise ValueError(f'JudgeResult payload for {case_id} must be an object')
    return {
        'case_id': _text(value.get('case_id') or case_id),
        'quality_label': _text(value.get('quality_label') or 'bad'),
        'failure_type': _text(value.get('failure_type') or 'unknown'),
        'is_correct': bool(value.get('is_correct')),
        'reason': _text(value.get('reason')),
        'trace_id': _text(value.get('trace_id')),
        'target': dict(value.get('target') or {}) if isinstance(value.get('target'), Mapping) else {},
        **{key: round(float(value.get(key) or 0.0), 4) for key in METRICS},
    }


def _quality(scores: Mapping[str, float], policy: Mapping[str, Any]) -> str:
    threshold = float(policy.get('quality_threshold') or 0.8)
    return 'good' if scores['answer_correctness'] >= threshold and scores['faithfulness'] >= threshold else 'bad'


def _summary_metrics(rows: list[Mapping[str, Any]]) -> dict[str, float | int]:
    scored = [
        row for row in rows
        if _text(row.get('quality_label')) != 'skipped' and _text(row.get('failure_type')) != 'infra_failure'
    ]
    return {
        'scored_count': len(scored),
        'correct_count': sum(bool(row.get('is_correct')) for row in scored),
        'correct_rate': _avg(1.0 if row.get('is_correct') else 0.0 for row in scored),
        **{f'{key}_avg': _avg(float(row.get(key) or 0.0) for row in scored) for key in METRICS},
    }


def _ab_metrics(metrics: Mapping[str, Any]) -> dict[str, float]:
    return {
        'answer_correctness': round(
            float(
                metrics.get('answer_correctness_avg') or metrics.get('correct_rate') or 0.0), 4), 'faithfulness': round(
            float(
                metrics.get('faithfulness_avg') or 0.0), 4), 'doc_recall': round(
            float(
                metrics.get('doc_recall_avg') or 0.0), 4), 'context_recall': round(
            float(
                metrics.get('context_recall_avg') or 0.0), 4), 'correct_rate': round(
            float(
                metrics.get('correct_rate') or 0.0), 4), }


def _repair_case_selected(row: Mapping[str, Any], repairable: list[Any]) -> bool:
    selected = {_text(item.get('case_id')) for item in repairable if isinstance(item, Mapping)}
    return not selected or _text(row.get('case_id')) in selected


def _repair_case_evidence(row: Mapping[str, Any]) -> dict[str, Any]:
    case = row.get('case') if isinstance(row.get('case'), Mapping) else {}
    answer = row.get('rag_answer') if isinstance(row.get('rag_answer'), Mapping) else {}
    judge = row.get('judge') if isinstance(row.get('judge'), Mapping) else {}
    return {
        'case_id': _text(row.get('case_id')),
        'category': _text(row.get('fine_category') or row.get('coarse_category')),
        'reason': _text(row.get('reason')),
        'question': _text(case.get('question')),
        'reference_answer': _clip(case.get('answer'), 900),
        'actual_answer': _clip(answer.get('answer'), 900),
        'reference_doc_ids': _as_list(case.get('reference_doc_ids')),
        'reference_chunk_ids': _as_list(case.get('reference_chunk_ids')),
        'actual_doc_ids': _as_list(answer.get('doc_ids')),
        'actual_chunk_ids': _as_list(answer.get('chunk_ids')),
        'metrics': {key: judge.get(key) for key in METRICS},
        'contexts': [_clip(item, 700) for item in _as_list(answer.get('contexts'))[:3]],
    }


def _repair_workspace(policy: Mapping[str, Any], ctx: Any | None, plan: Mapping[str, Any]) -> Path:
    base = _repair_base_dir()
    configured = _text(policy.get('candidate_workdir'))
    if configured:
        workspace = Path(configured).resolve()
        if not _path_within(workspace, base):
            raise RuntimeError(f'candidate workspace must be under managed repair dir: {base}')
        return workspace
    roots = [key.partition for key in getattr(ctx, 'output_keys', ()) if getattr(key, 'partition', '')]
    identity = {'roots': roots, 'target_cases': plan.get('target_cases'), 'evidence': plan.get('evidence_cases')}
    suffix = hashlib.sha1(_stable_text(identity).encode('utf-8')).hexdigest()[:12]
    return base / suffix / 'candidate'


def _repair_base_dir() -> Path:
    return (Path(os.getenv('LAZYMIND_EVO_BASE_DIR') or '/var/lib/lazymind/evo') / 'work' / 'repair').resolve()


def _prepare_repair_workspace(source: Path, workspace: Path) -> None:
    if not _is_algorithm_source(source):
        raise RuntimeError(f'candidate source is not LazyRAG algorithm dir: {source}')
    if _path_overlaps(source, workspace):
        raise RuntimeError(f'candidate workspace must be outside source tree: source={source}, workspace={workspace}')
    if not workspace.exists():
        _copy_algorithm_source(source, workspace)
    if not _is_algorithm_source(workspace):
        raise RuntimeError(f'candidate workspace is not LazyRAG algorithm dir: {workspace}')
    _ensure_git_baseline(workspace)
    _git(workspace, 'reset', '--hard', 'HEAD')
    _git(workspace, 'clean', '-fd', '--', '.')


def _algorithm_source_root(value: Any) -> Path:
    path = Path(_text(value)).resolve()
    for candidate in (path, *path.parents):
        if _is_algorithm_source(candidate):
            return candidate
    return path


def _copy_algorithm_source(source: Path, target: Path) -> None:
    target.mkdir(parents=True, exist_ok=True)
    ignore = shutil.ignore_patterns('.git', '.evo_repair_logs', '__pycache__', '*.pyc')
    for name in REPAIR_COPY_DIRS:
        if (source / name).exists():
            shutil.copytree(source / name, target / name, ignore=ignore, dirs_exist_ok=True)
    for name in REPAIR_COPY_FILES:
        if (source / name).exists():
            shutil.copy2(source / name, target / name)


def _is_algorithm_source(path: Path) -> bool:
    return (path / 'lazymind' / 'chat' / 'app.py').exists()


def _path_overlaps(left: Path, right: Path) -> bool:
    left_resolved, right_resolved = left.resolve(), right.resolve()
    return (
        left_resolved == right_resolved
        or left_resolved in right_resolved.parents
        or right_resolved in left_resolved.parents
    )


def _path_within(path: Path, root: Path) -> bool:
    resolved, resolved_root = path.resolve(), root.resolve()
    return resolved == resolved_root or resolved_root in resolved.parents


def _ensure_git_baseline(workspace: Path) -> None:
    if not (workspace / '.git').exists():
        _git(workspace, 'init')
    if _git_status_code(workspace, 'rev-parse', '--verify', 'HEAD'):
        _git(workspace, 'add', '.')
        _git(workspace, '-c', 'user.email=evo@example.local', '-c', 'user.name=evo', 'commit', '-m', 'baseline')


def _repair_diagnosis(plan: Mapping[str, Any], policy: Mapping[str, Any],
                      ctx: Any | None, attempt: int) -> dict[str, Any]:
    evidence = list(plan.get('evidence_cases') or [])
    prompt = (
        '你是 LazyRAG evo 修复规划器。基于失败 case 证据，分析最可能的代码修复方向。'
        '不要编造外部信息。输出简洁中文，包含 root_cause、target_files、patch_strategy、risk。\n\n'
        f'Attempt: {attempt}\n'
        f'Repair policy: {json.dumps(_json_safe(dict(policy)), ensure_ascii=False, sort_keys=True)}\n'
        f'Evidence cases: {json.dumps(_json_safe(evidence), ensure_ascii=False, sort_keys=True)}'
    )
    model_text = _call_repair_llm(prompt, ctx)
    return {
        'attempt': attempt,
        'model_analysis': _clip(model_text, 4000),
        'target_cases': list(plan.get('target_cases') or []),
        'analysis_summary': dict(plan.get('analysis_summary') or {}),
    }


def _call_repair_llm(prompt: str, ctx: Any | None) -> str:
    model_config = getattr(ctx, 'model_config', None) or {}
    try:
        from evo.message_intent.planner import LazyLLMPlannerClient

        return _text(LazyLLMPlannerClient(model_config=model_config)(prompt, stream=False))
    except Exception as exc:
        return f'LLM repair analysis unavailable: {type(exc).__name__}: {exc}'


def _opencode_task(plan: Mapping[str, Any], workspace: Mapping[str, Any],
                   diagnosis: Mapping[str, Any], attempt: int) -> dict[str, Any]:
    policy = plan.get('policy') if isinstance(plan.get('policy'), Mapping) else {}
    seed_files = _as_list(policy.get('seed_files')) or [
        'lazymind/chat/engine/prompts/guidance.py',
        'lazymind/chat/engine/prompts/system_prompt.py',
        'lazymind/chat/engine/agent_core.py',
        'lazymind/chat/service/chat_service.py',
        'lazymind/chat/engine/tools/kb.py',
    ]
    return {
        'mode': 'lazyrag_evo_repair_patch_once',
        'attempt': attempt,
        'objective': 'Patch the LazyRAG chat/RAG implementation so the failing evo evaluation cases improve.',
        'workspace': {'path': workspace.get('workspace_ref'), 'source_dir': workspace.get('source_dir')},
        'allowed_roots': _as_list(policy.get('allowed_roots')) or ['lazymind/chat'],
        'blocked_roots': _as_list(policy.get('blocked_roots')) or ['tests', '.git', 'lazyllm'],
        'seed_files': seed_files,
        'evidence_cases': plan.get('evidence_cases') or [],
        'diagnosis': diagnosis,
        'instructions': [
            'Read only the seed_files first. Do not inspect vendored lazyllm sources unless a seed file directly '
            'points to an allowed-root wrapper that must be changed.',
            'You must make one smallest code change in allowed_roots that addresses the observed '
            'RAG/tool/generation failure, unless the seed files prove no safe code patch exists.',
            'Do not edit tests, vendored lazyllm code, secrets, or unrelated modules.',
            'If the evidence points to bad source/OCR data rather than code, still inspect retrieval/chat handling '
            'and only patch when a code-level improvement is justified.',
            'After editing, run: python -m compileall -q lazymind/chat.',
            'Stop immediately after the first minimal patch and leave the git diff in the workspace.',
        ],
        'stop_condition': (
            'A git diff exists in allowed_roots. If no safe patch exists, write a final note explaining the exact '
            'seed file evidence; do not continue broad exploration.'
        ),
    }


def _opencode_env_from_context(ctx: Any | None) -> dict[str, str]:
    env = {key: value for key, value in os.environ.items() if value and (
        key.startswith(('ANTHROPIC_', 'AZURE_', 'COHERE_', 'GOOGLE_', 'GROQ_', 'LAZYLLM_',
                       'LAZYMIND_EVO_CODE_', 'MISTRAL_', 'OPENAI_', 'OPENCODE_', 'QWEN_', 'DEEPSEEK_'))
    )}
    cfg = getattr(ctx, 'model_config', None) or {}
    role = cfg.get('evo_llm') or cfg.get('llm') if isinstance(cfg, Mapping) else {}
    if isinstance(role, Mapping):
        for key in (
            'LAZYMIND_EVO_CODE_MODEL',
            'LAZYMIND_EVO_CODE_PROVIDER',
            'LAZYMIND_EVO_CODE_LABEL',
            'LAZYMIND_EVO_CODE_API_KEY',
            'LAZYMIND_EVO_CODE_BASE_URL',
            'OPENCODE_MODEL',
            'OPENCODE_PROVIDER',
            'OPENCODE_PROVIDER_MODEL',
            'OPENCODE_PROVIDER_BASE_URL',
            'OPENCODE_PROVIDER_KEY_ENV',
        ):
            env.pop(key, None)
        model = _text(role.get('model'))
        base_url = _text(role.get('base_url') or role.get('url'))
        api_key = _text(role.get('api_key'))
        provider = _text(role.get('provider') or _provider_from_url(base_url) or _provider_from_model(model))
        if model:
            env['LAZYMIND_EVO_CODE_MODEL'] = model
        if provider:
            env['LAZYMIND_EVO_CODE_PROVIDER'] = provider
            env['LAZYMIND_EVO_CODE_LABEL'] = provider
        if api_key:
            env['LAZYMIND_EVO_CODE_API_KEY'] = api_key
        if base_url:
            env['LAZYMIND_EVO_CODE_BASE_URL'] = base_url
    return {key: value for key, value in env.items() if value}


def _provider_from_url(url: str) -> str:
    lowered = url.lower()
    if 'deepseek' in lowered:
        return 'deepseek'
    if 'dashscope' in lowered or 'qwen' in lowered or 'aliyun' in lowered:
        return 'qwen'
    if 'openai' in lowered:
        return 'openai'
    if 'siliconflow' in lowered:
        return 'siliconflow'
    return ''


def _provider_from_model(model: str) -> str:
    lowered = model.lower()
    if lowered.startswith('deepseek'):
        return 'deepseek'
    if lowered.startswith('qwen') or lowered.startswith('qwen/'):
        return 'qwen'
    if lowered.startswith('gpt-') or lowered.startswith('openai/'):
        return 'openai'
    return ''


def _verify_repair_workspace(workspace: Path, policy: Mapping[str, Any]) -> dict[str, Any]:
    commands = _verification_commands(policy)
    results = []
    for command in commands:
        if not command:
            continue
        completed = subprocess.run(command, cwd=str(workspace), capture_output=True,
                                   text=True, timeout=120, check=False)
        results.append({
            'command': command,
            'returncode': completed.returncode,
            'stdout': _clip(completed.stdout, 2000),
            'stderr': _clip(completed.stderr, 2000),
        })
        if completed.returncode:
            return {'status': 'failed', 'results': results}
    return {'status': 'passed', 'results': results}


def _verification_commands(policy: Mapping[str, Any]) -> list[list[str]]:
    raw = _as_list(policy.get('verification_commands')) or [['python', '-m', 'compileall', '-q', 'lazymind/chat']]
    commands = []
    for item in raw:
        if isinstance(item, str):
            commands.append(shlex.split(item))
        elif isinstance(item, (list, tuple)):
            commands.append([_text(part) for part in item if _text(part)])
    return [command for command in commands if command]


def _diff_scope(files: list[str], policy: Mapping[str, Any]) -> dict[str, Any]:
    allowed = [_norm_root(item) for item in (_as_list(policy.get('allowed_roots')) or ['lazymind/chat'])]
    blocked = [_norm_root(item) for item in (_as_list(policy.get('blocked_roots')) or ['tests', '.git', 'lazyllm'])]
    violations = [
        path for path in files
        if not _path_allowed(path, allowed) or _path_allowed(path, blocked)
    ]
    return {
        'status': 'passed' if not violations else 'failed',
        'allowed_roots': allowed,
        'blocked_roots': blocked,
        'violations': violations,
    }


def _norm_root(value: Any) -> str:
    return _text(value).strip().strip('/').rstrip('/')


def _path_allowed(path: str, roots: list[str]) -> bool:
    normalized = _norm_root(path)
    return any(normalized == root or normalized.startswith(f'{root}/') for root in roots if root)


def _repair_failure(result: Any, diff: str, verification: Mapping[str, Any], diff_scope: Mapping[str, Any]) -> str:
    if getattr(result, 'last_error', None):
        return _text((result.last_error or {}).get('type') or (
            result.last_error or {}).get('message') or 'opencode_failed')
    if not diff.strip():
        return 'no_diff'
    if verification.get('status') != 'passed':
        return 'verification_failed'
    if diff_scope.get('status') != 'passed':
        return 'diff_scope_violation'
    return 'unknown'


def _git(workspace: Path, *args: str) -> str:
    result = subprocess.run(
        ['git', '-c', f'safe.directory={workspace}', '-C', str(workspace), *args],
        capture_output=True,
        text=True,
        timeout=60,
        check=False,
    )
    if result.returncode:
        raise RuntimeError((result.stderr or result.stdout).strip())
    return result.stdout.strip()


def _git_status_code(workspace: Path, *args: str) -> int:
    return subprocess.run(
        ['git', '-c', f'safe.directory={workspace}', '-C', str(workspace), *args],
        capture_output=True,
        text=True,
        timeout=60,
        check=False,
    ).returncode


def _source_ids(items: Any) -> tuple[list[str], list[str]]:
    doc_ids, chunk_ids = [], []
    for item in _as_list(items):
        if isinstance(item, Mapping):
            doc = _first_text(item, 'doc_id', 'document_id', 'file_id', 'docid')
            chunk = _first_text(item, 'chunk_id', 'segment_id', 'segement_id', 'node_id', 'uid', 'source_unit_ref')
            if doc:
                doc_ids.append(doc)
            if chunk:
                chunk_ids.append(chunk)
    return list(dict.fromkeys(doc_ids)), list(dict.fromkeys(chunk_ids))


def _parse_chat_response(raw: str) -> dict[str, Any]:
    raw = raw.strip()
    if not raw:
        return {}
    try:
        body = json.loads(raw)
    except json.JSONDecodeError:
        body = None
    if isinstance(body, Mapping):
        parsed = _chat_payload_from_events([body])
        if not parsed.get('kb_errors'):
            parsed['kb_errors'] = _extract_tool_errors_from_text(raw)
        _merge_tool_sources(parsed, raw)
        return parsed
    if isinstance(body, list):
        parsed = _chat_payload_from_events([item for item in body if isinstance(item, Mapping)])
        if not parsed.get('kb_errors'):
            parsed['kb_errors'] = _extract_tool_errors_from_text(raw)
        _merge_tool_sources(parsed, raw)
        return parsed

    events, text_fragments = [], []
    for line in raw.splitlines():
        text = line.removeprefix('data:').strip() if line.startswith('data:') else line.strip()
        if not text or text == '[DONE]' or text.startswith(('event:', 'id:')):
            continue
        try:
            data = json.loads(text)
        except json.JSONDecodeError:
            text_fragments.append(text)
            continue
        if isinstance(data, Mapping):
            events.append(data)
        elif isinstance(data, list):
            events.extend(item for item in data if isinstance(item, Mapping))
    parsed = _chat_payload_from_events(events)
    if not parsed.get('answer') and text_fragments:
        parsed['answer'] = _clean_answer(''.join(text_fragments))
    if not parsed.get('kb_errors'):
        parsed['kb_errors'] = _extract_tool_errors_from_text(raw)
    _merge_tool_sources(parsed, raw)
    return parsed


def _chat_payload_from_events(events: list[Mapping[str, Any]]) -> dict[str, Any]:
    answer, sources, contexts, doc_ids, chunk_ids, trace_id, kb_errors = [], [], [], [], [], '', []
    for event in events:
        piece = _unwrap_chat_event(event)
        piece_sources = [item for item in _as_list(piece.get('sources')) if isinstance(item, Mapping)]
        piece_contexts = _as_list(piece.get('contexts'))
        piece_context_sources = [item for item in piece_contexts if isinstance(item, Mapping)]
        piece_text = _chat_text(piece)
        tool_sources = _tool_sources_from_text(piece_text)
        answer.append(piece_text)
        sources.extend([*piece_sources, *tool_sources])
        contexts.extend([
            *(_source_text(item) for item in piece_contexts),
            *(_source_text(item) for item in tool_sources),
        ])
        doc_ids.extend(_as_list(piece.get('doc_ids') or piece.get('document_ids')))
        chunk_ids.extend(_as_list(piece.get('chunk_ids') or piece.get('segment_ids') or piece.get('segement_ids')))
        source_doc_ids, source_chunk_ids = _source_ids([*piece_sources, *piece_context_sources, *tool_sources])
        doc_ids.extend(source_doc_ids)
        chunk_ids.extend(source_chunk_ids)
        kb_errors.extend(_tool_errors(piece))
        kb_errors.extend(_extract_tool_errors_from_text(piece_text))
        trace_id = trace_id or _text(piece.get('trace_id') or piece.get('traceId'))
    return {
        'answer': _clean_answer(''.join(answer)),
        'sources': _unique_sources(sources),
        'contexts': list(dict.fromkeys(item for item in contexts if item)),
        'doc_ids': list(dict.fromkeys(_text(item) for item in doc_ids if _text(item))),
        'chunk_ids': list(dict.fromkeys(_text(item) for item in chunk_ids if _text(item))),
        'trace_id': trace_id,
        'kb_errors': list(dict.fromkeys(err for err in kb_errors if err)),
    }


def _unwrap_chat_event(data: Mapping[str, Any]) -> Mapping[str, Any]:
    current: Any = data
    for key in ('data', 'result', 'output', 'message'):
        if isinstance(current, Mapping) and isinstance(current.get(key), Mapping):
            current = current[key]
    return current if isinstance(current, Mapping) else {}


def _chat_text(data: Mapping[str, Any]) -> str:
    for key in ('answer', 'delta', 'text', 'content', 'response'):
        value = data.get(key)
        if isinstance(value, str) and value:
            return value
    message = data.get('message')
    if isinstance(message, str):
        return message
    if isinstance(message, Mapping):
        return _chat_text(message)
    return ''


def _tool_errors(data: Mapping[str, Any]) -> list[str]:
    errors: list[str] = []
    for key in ('tool_error', 'tool_errors', 'error', 'errors'):
        value = data.get(key)
        if isinstance(value, str):
            errors.append(value)
        elif isinstance(value, Mapping):
            errors.extend(_tool_errors(value))
        elif isinstance(value, list):
            for item in value:
                if isinstance(item, str):
                    errors.append(item)
                elif isinstance(item, Mapping):
                    errors.extend(_tool_errors(item))
    return errors


def _extract_tool_errors_from_text(raw: str) -> list[str]:
    errors: list[str] = []
    for raw_item in re.findall(r'<tool_result>(.*?)</tool_result>', raw, flags=re.S):
        try:
            payload = json.loads(raw_item)
        except json.JSONDecodeError:
            continue
        if not isinstance(payload, Mapping):
            continue
        result = payload.get('result')
        if isinstance(result, Mapping):
            if result.get('success') is False:
                errors.append(_text(result.get('reason') or result.get('error') or 'kb_search failed'))
            nested = result.get('result')
            if isinstance(nested, Mapping) and nested.get('success') is False:
                errors.append(_text(nested.get('reason') or nested.get('error') or 'kb_search failed'))
        elif isinstance(result, str) and result:
            errors.append(result)
    return errors


def _clean_answer(text: str) -> str:
    cleaned = re.sub(r'<(?P<tag>tp|trp|tool_call|tool_result)(?:\s[^>]*)?>.*?</(?P=tag)>', '', text, flags=re.S)
    cleaned = _strip_tool_status_text(cleaned)
    cleaned = re.sub(r'\n{3,}', '\n\n', cleaned)
    return cleaned.strip()


def _strip_tool_status_text(text: str) -> str:
    patterns = (
        (
            r'(?im)^\s*I will (?:first )?(?:activate|call|use|search|now search|look|retrieve|query)\b'
            r'.*(?:knowledge base|KBToolGroup|kb_search|tool group).*$'
        ),
        r'(?im)^\s*I will now search\b.*$',
        (
            r"(?im)^\s*I(?:'ll| am going to) (?:first )?(?:activate|call|use|search|look|retrieve|query)\b"
            r'.*(?:knowledge base|KBToolGroup|kb_search|tool group).*$'
        ),
    )
    for pattern in patterns:
        text = re.sub(pattern, '', text)
    return text


def _answer_from_evidence(text: str) -> str:
    sentence = re.split(r'(?<=[。.!?])\s+', text.strip(), maxsplit=1)[0]
    return _clip(sentence or text, 240)


def _unique_docs(units: list[Mapping[str, Any]]) -> list[dict[str, str]]:
    by_id = {}
    for unit in units:
        by_id.setdefault(_text(unit.get('doc_id')), {
            'doc_id': _text(unit.get('doc_id')),
            'filename': _text(unit.get('filename')),
            'doc_ref': _text(unit.get('doc_ref')),
        })
    return list(by_id.values())


def _tool_sources_from_text(raw: str) -> list[Mapping[str, Any]]:
    sources: list[Mapping[str, Any]] = []
    for raw_item in re.findall(r'<tool_result>(.*?)</tool_result>', raw, flags=re.S):
        try:
            payload = json.loads(raw_item)
        except json.JSONDecodeError:
            continue
        result = payload.get('result') if isinstance(payload, Mapping) else None
        nested = result.get('result') if isinstance(result, Mapping) else None
        for key in ('items', 'sources', 'contexts'):
            value = nested.get(key) if isinstance(nested, Mapping) else None
            if isinstance(value, list):
                sources.extend(item for item in value if isinstance(item, Mapping))
    return sources


def _merge_tool_sources(parsed: dict[str, Any], raw: str) -> None:
    sources = _tool_sources_from_text(raw)
    if not sources:
        return
    parsed['sources'] = _unique_sources([*parsed.get('sources', []), *sources])
    doc_ids, chunk_ids = _source_ids(sources)
    parsed['doc_ids'] = list(dict.fromkeys([*parsed.get('doc_ids', []), *doc_ids]))
    parsed['chunk_ids'] = list(dict.fromkeys([*parsed.get('chunk_ids', []), *chunk_ids]))
    parsed['contexts'] = list(dict.fromkeys([
        *(_source_text(item) for item in _as_list(parsed.get('contexts'))),
        *(_source_text(item) for item in sources),
    ]))


def _source_text(item: Any) -> str:
    if isinstance(item, Mapping):
        return _text(item.get('context') or item.get('content') or item.get('text'))
    return _text(item)


def _unique_sources(items: Any) -> list[Mapping[str, Any]]:
    unique: dict[str, Mapping[str, Any]] = {}
    for item in _as_list(items):
        if not isinstance(item, Mapping):
            continue
        key = _first_text(item, 'uid', 'chunk_id', 'segment_id', 'segement_id', 'node_id',
                          'doc_id', 'document_id', 'file_id', 'docid', 'ref') or _stable_text(item)
        unique.setdefault(key, item)
    return list(unique.values())


def _question_from_evidence(filename: str, evidence: str) -> str:
    topic = _clip(re.split(r'[。.!?\n]', evidence.strip(), maxsplit=1)[0], 80)
    if not topic:
        return f'What verifiable fact is stated in {filename}?'
    return f'What does {filename} state about {topic}?'


def _unit_type(content: str, metadata: Mapping[str, Any] | None = None) -> str:
    node_type = _text((metadata or {}).get('type') or (metadata or {}).get('node_type')).lower()
    if node_type in {'table', 'list', 'ordered_list', 'unordered_list', 'formula', 'equation'}:
        return {'ordered_list': 'list', 'unordered_list': 'list', 'equation': 'formula'}.get(node_type, node_type)
    if '|' in content and '\n' in content:
        return 'table'
    if re.search(r'\b(sum|average|formula|equation|=)\b', content, re.I):
        return 'formula'
    return 'paragraph'


def _choice(raw: Any, default: tuple[str, ...], index: int) -> str:
    values = [item for item in (_text(v) for v in _as_list(raw)) if item in default]
    pool = tuple(values) or default
    return pool[index % len(pool)]


def _recall(expected: Any, actual: Any) -> float:
    expected_set = {_text(item) for item in _as_list(expected) if _text(item)}
    actual_set = {_text(item) for item in _as_list(actual) if _text(item)}
    return round(len(expected_set & actual_set) / len(expected_set), 4) if expected_set else 0.0


def _avg(values: Any) -> float:
    rows = list(values)
    return round(sum(rows) / len(rows), 4) if rows else 0.0


def _case_index(case_id: str) -> int:
    match = re.search(r'(\d+)$', case_id)
    return max(0, int(match.group(1)) - 1) if match else sum(map(ord, case_id))


def _stable_text(value: Any) -> str:
    return json.dumps(value, ensure_ascii=False, sort_keys=True, default=str)


def _config_value(config: Any, key: str) -> str:
    try:
        return _text(config[key])
    except Exception:
        return _text(getattr(config, key, ''))


def _int_between(value: Any, default: int, low: int, high: int) -> int:
    try:
        number = int(value)
    except (TypeError, ValueError):
        number = default
    return min(high, max(low, number))


def _chunks(items: list[Any], size: int) -> list[list[Any]]:
    return [items[index:index + size] for index in range(0, len(items), size)]


def _json_safe(value: Any) -> Any:
    if isinstance(value, Mapping):
        return {str(key): _json_safe(item) for key, item in value.items()}
    if isinstance(value, (list, tuple, set)):
        return [_json_safe(item) for item in value]
    return value if value is None or isinstance(value, (str, int, float, bool)) else str(value)


def _first_text(item: Mapping[str, Any], *keys: str) -> str:
    return next((_text(item.get(key)) for key in keys if _text(item.get(key))), '')


def _unique_texts(items: Any) -> list[str]:
    return list(dict.fromkeys(text for text in (_text(item) for item in _as_list(items)) if text))


def _model_config_identity(value: Any) -> dict[str, dict[str, Any]]:
    if not isinstance(value, Mapping):
        return {}
    safe_fields = ('source', 'model', 'base_url', 'url', 'type', 'skip_auth')
    return {
        role: {field: config[field] for field in safe_fields if field in config and config[field] not in (None, '')}
        for role, config in sorted((_text(role), item) for role, item in value.items())
        if isinstance(config, Mapping)
    }


def _clip(value: Any, limit: int) -> str:
    text = _text(value)
    return text if len(text) <= limit else text[: max(0, limit - 15)] + '\n...[truncated]'


def _norm(value: Any) -> str:
    return re.sub(r'\s+', '', _text(value).lower())


def _text(value: Any) -> str:
    return '' if value is None else str(value).strip()


def _as_list(value: Any) -> list[Any]:
    if value is None:
        return []
    if isinstance(value, list):
        return value
    if isinstance(value, tuple | set):
        return list(value)
    return [value]
