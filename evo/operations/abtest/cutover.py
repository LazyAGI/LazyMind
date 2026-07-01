from __future__ import annotations

import hashlib
import os
import re
import urllib.parse
from collections import Counter
from pathlib import Path
from typing import Any, Mapping

from evo.operations.common import (
    METRICS,
    as_list,
    avg,
    int_between,
    stable_text,
    text,
)
from evo.operations.eval import judge_answer, normalize_rag_answer
from evo.operations.eval.judge_answer import unscored_judge_result


OLD_CONFIG_POST_ACTION = """def _model_config_path_post_action(resolved_path):
    if not resolved_path: return
    lazyllm.config['auto_model_config_map_path'] = str(resolved_path)"""
NEW_CONFIG_POST_ACTION = """def _model_config_path_post_action(resolved_path):
    if not resolved_path: return
    value = str(resolved_path)
    lazyllm.config._impl['auto_model_config_map_path'] = value"""
OLD_KB_DOCUMENT_BINDING = (
    "_DEFAULT_KB_DOCUMENT = lazyllm.Document(url=f'{_DEFAULT_KB_URL}/_call', name=_cfg['algo_id'])"
)
NEW_KB_DOCUMENT_BINDING = (
    "_DEFAULT_KB_DOCUMENT = lazyllm.Document(url=f'{_DEFAULT_KB_URL}/_call', name=_cfg['agentic_kb_name'])"
)


def candidate_service(config: Mapping[str, Any], patch: Mapping[str, Any], services: Any) -> dict[str, Any]:
    skipped = patch.get('status') != 'verified'
    return {
        **({'status': 'skipped'} if skipped else _start_candidate_algorithm(config, patch, services)),
        'candidate_config': dict(config),
        'patch_status': text(patch.get('status')),
        **({'healthcheck': {'status': 'skipped'}} if skipped else {}),
    }


def normalize_candidate_sources(workspace: str | Path) -> None:
    _normalize_candidate_sources(Path(workspace))


def candidate_rag_answer(case: Mapping[str, Any], service: Mapping[str, Any], services: Any) -> dict[str, Any]:
    if service.get('status') == 'skipped':
        return {'case_id': text(case.get('id')), 'case': case, 'status': 'skipped',
                'answer': '', 'service_status': 'skipped'}
    _ensure_candidate_service_ready(service)
    result = normalize_rag_answer(case, services.answer_case(case, {
        'target_chat_url': text(service.get('service_url')),
        'dataset_id': _case_dataset_id(case) or text(service.get('dataset_id')),
        'require_trace': True,
        'algorithm_id': text(service.get('algorithm_id')),
    }))
    _validate_candidate_route(result, service)
    result['candidate_service'] = {
        'algorithm_id': text(service.get('algorithm_id')),
        'service_url': text(service.get('service_url')),
        'router_admin_url': text(service.get('router_admin_url')),
    }
    return result


def candidate_judge(answer: Mapping[str, Any], policy: Mapping[str, Any] | None = None,
                    services: Any | None = None) -> dict[str, Any]:
    if answer.get('status') != 'skipped':
        return judge_answer(answer, policy or {}, services)
    return unscored_judge_result(
        answer,
        policy or {},
        quality_label='skipped',
        failure_type='candidate_not_run',
        reason='candidate evaluation skipped',
    )


def candidate_summary(judges: Mapping[str, Any]) -> dict[str, Any]:
    rows = [dict(item) for _, item in sorted(judges.items())]
    metrics = _summary_metrics(rows)
    failures = _candidate_execution_failures(rows)
    return {
        'id': 'abtest.candidate_eval_summary',
        'case_ids': [text(row.get('case_id')) for row in rows],
        'total': len(rows),
        'metrics': metrics,
        'quality_counts': dict(Counter(text(row.get('quality_label')) for row in rows)),
        'failure_type_counts': dict(Counter(text(row.get('failure_type')) for row in rows)),
        'execution_failures': failures,
        'checks': {
            'ready': not failures and metrics['scored_count'] == len(rows) and bool(rows),
            'errors': [{'code': 'candidate_execution_failed', **item} for item in failures],
            'warnings': [],
        },
        'rows': rows,
    }


def compare_abtest(baseline: Mapping[str, Any], candidate: Mapping[str, Any]) -> dict[str, Any]:
    total = int(candidate.get('total') or 0)
    skipped = total > 0 and candidate.get('quality_counts', {}).get('skipped', 0) == total
    candidate_failed = _candidate_summary_failed(candidate)
    baseline_rows, baseline_duplicates = _rows_by_case_id(baseline.get('rows'))
    candidate_rows, candidate_duplicates = _rows_by_case_id(candidate.get('rows'))
    case_ids = list(dict.fromkeys([*baseline_rows, *candidate_rows, *as_list(baseline.get('case_ids')),
                                   *as_list(candidate.get('case_ids'))]))
    baseline_metrics = _ab_metrics(baseline.get('metrics') or {})
    candidate_metrics = baseline_metrics if skipped else _ab_metrics(candidate.get('metrics') or {})
    delta = {key: round(candidate_metrics[key] - baseline_metrics[key], 4) for key in baseline_metrics}
    case_deltas = [
        _case_delta(case_id, baseline_rows.get(case_id), candidate_rows.get(case_id), skipped=skipped)
        for case_id in case_ids
    ]
    regressions = [row for row in case_deltas if row['outcome'] == 'regressed']
    improvements = [row for row in case_deltas if row['outcome'] == 'improved']
    missing = [row for row in case_deltas if row['outcome'] in {'missing_baseline', 'missing_candidate'}]
    guard = _goodcase_guard(case_deltas, baseline_rows)
    reasons = (
        ['candidate evaluation was skipped because no verified repair patch is available']
        if skipped
        else ['candidate evaluation produced no scored cases; inspect candidate execution_failures']
        if candidate_failed else []
    )
    if regressions:
        reasons.append(f'{len(regressions)} case(s) regressed on answer_correctness')
    duplicates = baseline_duplicates + candidate_duplicates
    if duplicates:
        reasons.append(f'{len(duplicates)} duplicate case row(s) ignored')
    decision = {
        'status': 'skipped' if skipped else 'candidate_eval_failed' if candidate_failed else 'review_candidate',
        'primary_metric': 'answer_correctness',
        'reasons': reasons,
    }
    return {
        'id': 'abtest.comparison',
        'status': 'skipped' if skipped else 'failed' if candidate_failed else 'completed',
        'verdict': decision['status'],
        'case_ids': case_ids,
        'case_count': len(case_ids),
        'metrics': {'baseline': baseline_metrics, 'candidate': candidate_metrics, 'delta': delta},
        'case_deltas': case_deltas,
        'goodcase_guard': {'status': 'skipped' if skipped else guard['status'], 'violations': guard['violations']},
        'policy': {'primary_metric': 'answer_correctness', 'guard_metrics': ['answer_correctness', 'chunk_recall']},
        'decision': decision,
        'reasons': reasons,
        'missing_metrics': [{'case_id': row['case_id'], 'outcome': row['outcome']} for row in missing],
        'data_warnings': duplicates,
        'baseline': {'total': baseline.get('total', 0), 'quality_counts': dict(baseline.get('quality_counts') or {})},
        'candidate': {'total': candidate.get('total', 0),
                      'quality_counts': dict(candidate.get('quality_counts') or {})},
        'summary': {
            'metrics': {'baseline': baseline_metrics, 'candidate': candidate_metrics, 'delta': delta},
            'case_deltas': case_deltas,
            'goodcase_guard': {'status': 'skipped' if skipped else guard['status'], 'violations': guard['violations']},
            'decision': decision,
            'policy': {'primary_metric': 'answer_correctness', 'guard_metrics': ['answer_correctness', 'chunk_recall']},
            'case_count': len(case_ids),
            'reasons': reasons,
            'missing_metrics': [{'case_id': row['case_id'], 'outcome': row['outcome']} for row in missing],
            'data_warnings': duplicates,
            'improved_count': len(improvements),
            'regressed_count': len(regressions),
        },
    }


def _ab_metrics(metrics: Mapping[str, Any]) -> dict[str, float]:
    return {
        'answer_score': round(float(metrics.get('answer_score_avg') or 0.0), 4),
        'answer_correctness': round(float(metrics.get('answer_correctness_avg')
                                          or metrics.get('correct_rate') or 0.0), 4),
        'answer_relevance': round(float(metrics.get('answer_relevance_avg') or 0.0), 4),
        'completeness': round(float(metrics.get('completeness_avg') or 0.0), 4),
        'format_compliance': round(float(metrics.get('format_compliance_avg') or 0.0), 4),
        'chunk_recall': round(float(metrics.get('chunk_recall_avg') or 0.0), 4),
        'chunk_precision': round(float(metrics.get('chunk_precision_avg') or 0.0), 4),
        'doc_recall': round(float(metrics.get('doc_recall_avg') or 0.0), 4),
        'doc_precision': round(float(metrics.get('doc_precision_avg') or 0.0), 4),
        'retrieval_score': round(float(metrics.get('retrieval_score_avg') or 0.0), 4),
        'correct_rate': round(float(metrics.get('correct_rate') or 0.0), 4),
    }


def _rows_by_case_id(rows: Any) -> tuple[dict[str, Mapping[str, Any]], list[dict[str, str]]]:
    indexed: dict[str, Mapping[str, Any]] = {}
    duplicates = []
    for row in as_list(rows):
        if not isinstance(row, Mapping):
            continue
        case_id = text(row.get('case_id') or row.get('id'))
        if not case_id:
            continue
        if case_id in indexed:
            duplicates.append({'case_id': case_id, 'warning': 'duplicate_case_row'})
            continue
        indexed[case_id] = row
    return indexed, duplicates


def _case_delta(case_id: str, baseline: Mapping[str, Any] | None,
                candidate: Mapping[str, Any] | None, *, skipped: bool = False) -> dict[str, Any]:
    before = _row_metrics(baseline or {})
    after = before if skipped else _row_metrics(candidate or {})
    delta = {key: round(after.get(key, 0.0) - before.get(key, 0.0), 4) for key in METRICS}
    baseline_trace_id = _row_trace_id(baseline or {})
    candidate_trace_id = baseline_trace_id if skipped else _row_trace_id(candidate or {})
    if skipped:
        outcome = 'skipped'
    elif not baseline:
        outcome = 'missing_baseline'
    elif not candidate:
        outcome = 'missing_candidate'
    elif text(candidate.get('failure_type')) in {'infra_failure', 'candidate_not_run'}:
        outcome = 'regressed'
    elif delta['answer_correctness'] > 0.0001:
        outcome = 'improved'
    elif delta['answer_correctness'] < -0.0001:
        outcome = 'regressed'
    else:
        outcome = 'unchanged'
    return {
        'case_id': case_id,
        'outcome': outcome,
        'before': before,
        'after': after,
        'delta': delta,
        'trace_id': candidate_trace_id or baseline_trace_id,
        'baseline_trace_id': baseline_trace_id,
        'candidate_trace_id': candidate_trace_id,
        'baseline_quality': text((baseline or {}).get('quality_label')),
        'candidate_quality': text((candidate or {}).get('quality_label')),
        'baseline_failure_type': text((baseline or {}).get('failure_type')),
        'candidate_failure_type': text((candidate or {}).get('failure_type')),
    }


def _row_metrics(row: Mapping[str, Any]) -> dict[str, float]:
    return {key: round(float(row.get(key) or 0.0), 4) for key in METRICS}


def _row_trace_id(row: Mapping[str, Any]) -> str:
    answer = row.get('rag_answer') if isinstance(row.get('rag_answer'), Mapping) else {}
    return text(row.get('trace_id') or answer.get('trace_id'))


def _goodcase_guard(
    case_deltas: list[dict[str, Any]],
    baseline_rows: Mapping[str, Mapping[str, Any]],
) -> dict[str, Any]:
    violations = []
    checked = 0
    for row in case_deltas:
        baseline = baseline_rows.get(text(row.get('case_id')), {})
        if not (baseline.get('is_correct') or text(baseline.get('quality_label')) == 'good'):
            continue
        checked += 1
        delta = row.get('delta') if isinstance(row.get('delta'), Mapping) else {}
        answer_drop = float(delta.get('answer_correctness') or 0.0)
        recall_drop = float(delta.get('chunk_recall') or 0.0)
        if row.get('outcome') == 'regressed' or answer_drop < -0.05 or recall_drop < -0.05:
            violations.append({
                'case_id': text(row.get('case_id')),
                'answer_correctness_delta': answer_drop,
                'chunk_recall_delta': recall_drop,
                'outcome': text(row.get('outcome')),
            })
    if checked == 0:
        return {'status': 'skipped', 'violations': [], 'reason': 'no_baseline_goodcase'}
    return {'status': 'passed' if not violations else 'failed', 'violations': violations, 'checked': checked}


def _candidate_execution_failures(rows: list[Mapping[str, Any]]) -> list[dict[str, str]]:
    bad_types = {'infra_failure', 'candidate_not_run', 'candidate_failed', 'tool_execution_issue'}
    failures = []
    for row in rows:
        failure_type = text(row.get('failure_type'))
        target = row.get('target') if isinstance(row.get('target'), Mapping) else {}
        expected, actual = text(target.get('algorithm_id')), text(target.get('routed_algorithm_id'))
        if failure_type in bad_types or (expected and actual and expected != actual):
            failures.append({
                'case_id': text(row.get('case_id')),
                'failure_type': failure_type or 'candidate_failed',
                'reason': text(row.get('reason') or 'candidate evaluation failed'),
            })
    return failures


def _candidate_summary_failed(candidate: Mapping[str, Any]) -> bool:
    total = int(candidate.get('total') or 0)
    metrics = candidate.get('metrics') if isinstance(candidate.get('metrics'), Mapping) else {}
    scored = int(metrics.get('scored_count') or 0)
    checks = candidate.get('checks') if isinstance(candidate.get('checks'), Mapping) else {}
    return bool(candidate.get('execution_failures')) or not checks.get('ready') or scored == 0 or scored != total


def _summary_metrics(rows: list[Mapping[str, Any]]) -> dict[str, float | int]:
    scored = [row for row in rows
              if text(row.get('quality_label')) != 'skipped' and text(row.get('failure_type')) != 'infra_failure']
    return {
        'scored_count': len(scored),
        'correct_count': sum(bool(row.get('is_correct')) for row in scored),
        'correct_rate': avg(1.0 if row.get('is_correct') else 0.0 for row in scored),
        **{f'{key}_avg': avg(float(row.get(key) or 0.0) for row in scored) for key in METRICS},
    }


def _start_candidate_algorithm(config: Mapping[str, Any], patch: Mapping[str, Any], services: Any) -> dict[str, Any]:
    workspace = _patch_workspace(patch)
    chat_path = workspace / 'lazymind' / 'chat'
    if not (chat_path / 'app.py').exists():
        raise RuntimeError(f'candidate chat app not found in verified patch workspace: {chat_path}')
    _normalize_candidate_sources(workspace)
    target_url = text(config.get('target_chat_url') or os.getenv(
        'LAZYMIND_EVO_TARGET_CHAT_URL') or 'http://chat:8046/api/chat/stream')
    router_admin_url = text(config.get('router_admin_url') or os.getenv(
        'LAZYMIND_EVO_ROUTER_ADMIN_URL') or _origin(target_url))
    if not router_admin_url:
        raise RuntimeError('router_admin_url is required to start candidate service')
    algorithm_id = _candidate_algorithm_id(config, patch)
    request_body = {
        'id': algorithm_id,
        'name': algorithm_id,
        'code_path': str(chat_path),
        'instance_count': int_between(config.get('instance_count'), 1, 1, 4),
        'config': _candidate_algorithm_env(config, algorithm_id),
    }
    result = services.register_candidate_algorithm(
        router_admin_url=router_admin_url,
        algorithm_id=algorithm_id,
        body=request_body,
        timeout_s=int_between(config.get('startup_timeout_s'), 180, 10, 900),
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
    if not text(service.get('algorithm_id')):
        raise RuntimeError('candidate service missing algorithm_id')
    if not text(service.get('service_url')):
        raise RuntimeError('candidate service missing service_url')


def _validate_candidate_route(answer: dict[str, Any], service: Mapping[str, Any]) -> None:
    target = answer.get('target') if isinstance(answer.get('target'), Mapping) else {}
    expected, actual = text(service.get('algorithm_id')), text(target.get('routed_algorithm_id'))
    if expected and actual != expected:
        answer['status'] = 'failed'
        error_type = 'candidate_route_mismatch' if actual else 'candidate_route_missing'
        answer['chat_error'] = {
            'type': error_type,
            'message': f'expected {expected}, got {actual or "<missing>"}',
        }


def _case_dataset_id(case: Mapping[str, Any]) -> str:
    prep = case.get('source_preparation') if isinstance(case.get('source_preparation'), Mapping) else {}
    return text(prep.get('source_snapshot_dataset_id') or case.get('dataset_id') or case.get('kb_id')
                or case.get('dataset_name'))


def _candidate_algorithm_env(config: Mapping[str, Any], algorithm_id: str) -> dict[str, str]:
    kb_name = text(
        config.get('agentic_kb_name')
        or os.getenv('LAZYMIND_AGENTIC_KB_NAME')
        or 'general_algo',
    )
    env = {
        'LAZYMIND_ALGO_ID': kb_name,
        'LAZYMIND_AGENTIC_KB_NAME': kb_name,
        'LAZYMIND_ROUTER_ALGORITHM_ID': algorithm_id,
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
        if value := text(os.getenv(key)):
            env[key] = value
    extra = config.get('env') if isinstance(config.get('env'), Mapping) else {}
    return {**env, **{text(key): text(value) for key, value in extra.items() if text(key) and text(value)}}


def _candidate_algorithm_id(config: Mapping[str, Any], patch: Mapping[str, Any]) -> str:
    explicit = text(config.get('algorithm_id'))
    if explicit:
        return _safe_id(explicit, 'evo_candidate')
    run_part = _safe_id(text(config.get('run_id') or config.get('thread_id')), 'run')
    digest = hashlib.sha1(stable_text({
        'workspace': patch.get('workspace_ref'),
        'diff': patch.get('diff'),
    }).encode('utf-8')).hexdigest()[:10]
    return f'evo_{run_part}_{digest}'[:64]


def _patch_workspace(patch: Mapping[str, Any]) -> Path:
    return Path(text(patch.get('workspace_ref'))).resolve()


def _origin(url: str) -> str:
    parsed = urllib.parse.urlparse(url)
    if not parsed.scheme or not parsed.netloc:
        return ''
    return urllib.parse.urlunparse((parsed.scheme, parsed.netloc, '', '', '', ''))


def _safe_id(value: str, fallback: str) -> str:
    safe = re.sub(r'[^A-Za-z0-9_.-]+', '_', value).strip('._-')
    return safe or fallback


def _normalize_candidate_sources(workspace: Path) -> None:
    _replace_text(workspace / 'lazymind' / 'config.py', OLD_CONFIG_POST_ACTION, NEW_CONFIG_POST_ACTION)
    _replace_text(
        workspace / 'lazymind' / 'chat' / 'engine' / 'tools' / 'kb.py',
        OLD_KB_DOCUMENT_BINDING,
        NEW_KB_DOCUMENT_BINDING,
    )


def _replace_text(path: Path, old: str, new: str) -> None:
    if not path.exists():
        return
    value = path.read_text(encoding='utf-8')
    updated = value.replace(old, new)
    if updated != value:
        path.write_text(updated, encoding='utf-8')
