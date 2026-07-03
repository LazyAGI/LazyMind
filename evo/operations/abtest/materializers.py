from __future__ import annotations

import hashlib
import json
import os
import re
from collections.abc import Mapping
from pathlib import Path
from typing import Any, Callable

from evo.operations.eval.answer import answer_case, case_kb_id, failed_rag_answer
from evo.operations.eval.judge import judge_case
from evo.operations.public_contracts import build_abtest_comparison_root, build_eval_summary_root
from evo.operations.router_ledger import RouterAlgorithmLedger, json_hash
from evo.operations.router_manager import (
    RouterAlgorithmSpec,
    RouterManager,
    RouterManagerError,
    normalize_chat_url,
)

ENV_PASSTHROUGH = (
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
)
COMPARE_METRICS = (
    'overall_score',
    'answer_quality_score',
    'retrieval_quality_score',
    'answer_correctness',
    'groundedness',
    'correct_rate',
)
SAFE_ID = re.compile(r'[^A-Za-z0-9_.-]+')


def abtest_materializers() -> dict[str, Callable[[Any, Mapping[str, object]], Mapping[str, object]]]:
    def service(ctx: Any, inputs: Mapping[str, object]) -> Mapping[str, object]:
        patch = _service_patch(_mapping(inputs['patch'], 'patch'), _mapping(inputs['workspace'], 'workspace'))
        return {'service': candidate_service(_mapping(inputs['config'], 'config'), patch, ctx)}

    def answer(ctx: Any, inputs: Mapping[str, object]) -> Mapping[str, object]:
        return {'answer': candidate_rag_answer(_mapping(inputs['case'], 'case'),
                                               _mapping(inputs['service'], 'service'))}

    def judge(ctx: Any, inputs: Mapping[str, object]) -> Mapping[str, object]:
        return {'judge': judge_case(_mapping(inputs['case'], 'case'),
                                    _mapping(inputs['answer'], 'answer'),
                                    _mapping(inputs.get('policy') or {}, 'policy'))}

    def summary(ctx: Any, inputs: Mapping[str, object]) -> Mapping[str, object]:
        judges = inputs.get('judges')
        if not isinstance(judges, tuple):
            raise ValueError('abtest.candidate_eval_summary judges input must be a partitioned tuple')
        return {'summary': build_eval_summary_root(ctx.run_id, judges)}

    def compare(ctx: Any, inputs: Mapping[str, object]) -> Mapping[str, object]:
        return {'comparison': build_abtest_comparison_root(
            ctx.run_id,
            _mapping(inputs['baseline'], 'baseline'),
            _mapping(inputs['candidate'], 'candidate'),
            _mapping(inputs['service'], 'service'),
        )}

    return {
        'abtest.candidate_service': service,
        'abtest.candidate_rag_answer': answer,
        'abtest.candidate_judge': judge,
        'abtest.candidate_eval_summary': summary,
        'abtest.compare': compare,
    }


def candidate_service(config: Mapping[str, Any], patch: Mapping[str, Any], ctx: Any | None = None) -> dict[str, Any]:
    base = {'candidate_config': dict(config), 'patch_status': _text(patch.get('status'))}
    if patch.get('status') != 'verified':
        return base | {'status': 'skipped', 'healthcheck': {'status': 'skipped'}}
    try:
        algorithm_id = _candidate_algorithm_id(config, patch, getattr(ctx, 'run_id', 'run'))
        if not _text(config.get('router_chat_url')):
            raise ValueError('candidate_config.router_chat_url is required')
        router_chat_url = normalize_chat_url(config.get('router_chat_url'))
        admin_url = _text(config.get('router_admin_url'))
        if not admin_url:
            raise ValueError('candidate_config.router_admin_url is required')
        manager = RouterManager(admin_url, router_chat_url)
        code_path = _code_path(config, patch)
        spec = RouterAlgorithmSpec(
            id=algorithm_id,
            name=_text(config.get('name')) or algorithm_id,
            code_path=code_path,
            instance_count=_int_between(config.get('instance_count'), 1, 1, 4),
            config=_candidate_env(config, algorithm_id),
        )
        body = _register_request(spec)
        timeout_s = _int_between(config.get('startup_timeout_s') or config.get('startup_timeout_seconds'),
                                 180, 10, 900)
        existing = manager.get_algorithm(algorithm_id)
        if existing and not _same_registration(existing, body):
            return base | _failed_service(
                algorithm_id,
                router_chat_url,
                manager.router_admin_url,
                code_path,
                'candidate_id_conflict',
                'algorithm_id already exists with different code_path/config',
            )
        registered = {'reused': True} if existing else manager.register_algorithm(spec, timeout_s=timeout_s)
        ready = manager.wait_ready(algorithm_id, timeout_s=timeout_s)
        _write_candidate_ledger(ctx, spec, router_chat_url, manager.router_admin_url, body, patch)
        return base | {
            'status': 'ready',
            'service_kind': 'router_algorithm',
            'algorithm_id': algorithm_id,
            'router_chat_url': router_chat_url,
            'router_admin_url': manager.router_admin_url,
            'workspace_ref': _text(patch.get('workspace_ref')),
            'code_path': code_path,
            'register_request': body,
            'register_response': registered,
            'healthcheck': manager.healthcheck_from_detail(ready),
        }
    except RouterManagerError as exc:
        return base | _failed_service('', '', '', '', exc.kind, str(exc))
    except Exception as exc:
        return base | _failed_service('', '', '', '', type(exc).__name__, str(exc))


def candidate_rag_answer(case: Mapping[str, Any], service: Mapping[str, Any]) -> dict[str, Any]:
    target_config = _candidate_target_config(service)
    target = {
        'router_chat_url': _text(target_config.get('router_chat_url')),
        'router_admin_url': _text(target_config.get('router_admin_url')),
        'algorithm_id': _text(target_config.get('algorithm_id')),
        'kb_id': case_kb_id(case, target_config),
    }
    if service.get('status') != 'ready':
        health = service.get('healthcheck') if isinstance(service.get('healthcheck'), Mapping) else {}
        error_type = 'candidate_service_skipped' if service.get('status') == 'skipped' else \
            'candidate_service_unavailable'
        message = _text(health.get('message')) or (
            'candidate evaluation skipped because repair patch is not verified'
            if service.get('status') == 'skipped' else 'candidate not ready'
        )
        return failed_rag_answer(case, {}, target, error_type, message)
    return answer_case(case, target_config)


def compare_eval_detail_for_repair(baseline: Mapping[str, Any], candidate: Mapping[str, Any]) -> dict[str, Any]:
    skipped = candidate.get('total') and candidate.get('quality_counts', {}).get('skipped') == candidate.get('total')
    baseline_rows, candidate_rows = _rows_by_case(baseline.get('rows')), _rows_by_case(candidate.get('rows'))
    case_ids = list(dict.fromkeys([*baseline_rows, *candidate_rows, *baseline.get('case_ids', ()),
                                   *candidate.get('case_ids', ())]))
    before, after = _summary_metrics(baseline), _summary_metrics(candidate)
    delta = {key: round(after[key] - before[key], 4) for key in before}
    case_deltas = [_case_delta(case_id, baseline_rows.get(case_id), candidate_rows.get(case_id))
                   for case_id in case_ids]
    failures = list(candidate.get('execution_failures') or [])
    candidate_failed = bool(failures) or not (candidate.get('checks') or {}).get('ready')
    regressions = [row for row in case_deltas if row['outcome'] == 'regressed']
    guard = _goodcase_guard(case_deltas, baseline_rows)
    reasons = []
    if skipped:
        reasons.append('candidate evaluation skipped because repair patch is not verified')
    if candidate_failed:
        reasons.append('candidate evaluation produced execution failures')
    if regressions:
        reasons.append(f'{len(regressions)} case(s) regressed on overall_score')
    verdict = 'skipped' if skipped else 'candidate_eval_failed' if candidate_failed else 'review_candidate'
    return {
        'id': 'abtest.comparison',
        'status': 'skipped' if skipped else 'failed' if candidate_failed else 'completed',
        'verdict': verdict,
        'case_ids': case_ids,
        'case_count': len(case_ids),
        'metrics': {'baseline': before, 'candidate': after, 'delta': delta},
        'case_deltas': case_deltas,
        'goodcase_guard': guard,
        'policy': {'primary_metric': 'overall_score', 'guard_metrics': ['overall_score', 'answer_correctness']},
        'decision': {'status': verdict, 'primary_metric': 'overall_score', 'reasons': reasons},
        'reasons': reasons,
        'missing_metrics': [{'case_id': row['case_id'], 'outcome': row['outcome']} for row in case_deltas
                            if row['outcome'].startswith('missing_')],
        'data_warnings': [],
        'baseline': {'total': baseline.get('total', 0), 'quality_counts': dict(baseline.get('quality_counts') or {})},
        'candidate': {'total': candidate.get('total', 0),
                      'quality_counts': dict(candidate.get('quality_counts') or {})},
        'summary': {'metrics': {'baseline': before, 'candidate': after, 'delta': delta},
                    'case_deltas': case_deltas, 'goodcase_guard': guard, 'decision': verdict,
                    'case_count': len(case_ids), 'reasons': reasons},
    }


def _candidate_target_config(service: Mapping[str, Any]) -> dict[str, Any]:
    config = service.get('candidate_config') if isinstance(service.get('candidate_config'), Mapping) else {}
    return dict(config) | {
        'router_chat_url': service.get('router_chat_url'),
        'router_admin_url': service.get('router_admin_url'),
        'algorithm_id': service.get('algorithm_id'),
    }


def _service_patch(patch: Mapping[str, Any], workspace: Mapping[str, Any]) -> dict[str, Any]:
    diff = patch.get('diff') if isinstance(patch.get('diff'), Mapping) else {}
    return {
        'status': patch.get('status'),
        'workspace_ref': workspace.get('workspace_ref'),
        'diff': ''.join(str(value) for value in diff.values()),
    }


def _same_registration(existing: Mapping[str, Any], body: Mapping[str, Any]) -> bool:
    return existing.get('code_path') == body.get('code_path') and dict(existing.get('config') or {}) == body['config']


def _register_request(spec: RouterAlgorithmSpec) -> dict[str, Any]:
    return {'id': spec.id, 'name': spec.name, 'code_path': spec.code_path,
            'instance_count': spec.instance_count, 'config': dict(spec.config)}


def _write_candidate_ledger(
    ctx: Any | None,
    spec: RouterAlgorithmSpec,
    router_chat_url: str,
    admin_url: str,
    register_request: Mapping[str, Any],
    patch: Mapping[str, Any],
) -> None:
    root = os.getenv('LAZYMIND_EVO_BASE_DIR')
    run_id = _text(getattr(ctx, 'run_id', ''))
    if not root or not run_id:
        return
    output_key = next(iter(getattr(ctx, 'output_key_by_name', {}).values()), None)
    candidate_ref = getattr(output_key, 'artifact_id', 'abtest.candidate_service')
    ledger = RouterAlgorithmLedger(Path(root) / 'artifact-store')
    ledger.upsert_algorithm(
        algorithm_id=spec.id,
        thread_id=run_id,
        run_id=run_id,
        candidate_ref=str(candidate_ref),
        router_admin_url=admin_url,
        service_url=router_chat_url,
        code_path=spec.code_path,
        config_hash=json_hash(spec.config),
        register_request_hash=json_hash(register_request),
        cleanup_policy=_text(patch.get('cleanup_policy')) or 'thread_delete',
    )


def _failed_service(algorithm_id: str, router_chat_url: str, admin_url: str, code_path: str,
                    error_type: str, message: str) -> dict[str, Any]:
    return {'status': 'failed', 'service_kind': 'router_algorithm', 'algorithm_id': algorithm_id,
            'router_chat_url': router_chat_url, 'router_admin_url': admin_url, 'code_path': code_path,
            'healthcheck': {'status': 'failed', 'type': error_type, 'message': message}}


def _candidate_algorithm_id(config: Mapping[str, Any], patch: Mapping[str, Any], run_id: str) -> str:
    explicit = _text(config.get('algorithm_id'))
    if explicit:
        algorithm_id = _safe_id(explicit, 'evo_candidate')[:64]
        if not algorithm_id.startswith('evo_'):
            raise ValueError('candidate_config.algorithm_id must start with evo_')
        return algorithm_id
    digest = hashlib.sha1(json.dumps({'workspace': patch.get('workspace_ref'), 'diff': patch.get('diff')},
                                     sort_keys=True, default=str).encode()).hexdigest()[:10]
    return f'evo_{_safe_id(_text(config.get("thread_id") or run_id), "run")}_{digest}'[:64]


def _candidate_env(config: Mapping[str, Any], algorithm_id: str) -> dict[str, str]:
    kb_name = _text(config.get('agentic_kb_name') or os.getenv('LAZYMIND_AGENTIC_KB_NAME') or 'general_algo')
    env = {'LAZYMIND_ALGO_ID': _text(config.get('algo_id')) or kb_name,
           'LAZYMIND_AGENTIC_KB_NAME': kb_name,
           'LAZYMIND_ROUTER_ALGORITHM_ID': algorithm_id,
           'LAZYMIND_ENABLE_ROUTER': 'false',
           'LAZYMIND_ROUTER_CHILD_PROXIED_ONLY': 'true'}
    env.update({key: _text(os.getenv(key)) for key in ENV_PASSTHROUGH if _text(os.getenv(key))})
    extra = config.get('env') if isinstance(config.get('env'), Mapping) else {}
    env.update({_text(key): _text(value) for key, value in extra.items() if _text(key) and _text(value)})
    return env


def _code_path(config: Mapping[str, Any], patch: Mapping[str, Any]) -> str:
    if config.get('code_path'):
        return _text(config['code_path'])
    workspace = Path(_text(patch.get('workspace_ref'))).as_posix()
    if not workspace:
        raise ValueError('verified repair patch must provide workspace_ref or candidate_config.code_path')
    return f'{workspace.rstrip("/")}/lazymind/chat'


def _summary_metrics(summary: Mapping[str, Any]) -> dict[str, float]:
    metrics = summary.get('metrics') if isinstance(summary.get('metrics'), Mapping) else {}
    return {key: _float(metrics.get(key if key == 'correct_rate' else f'{key}_avg')) for key in COMPARE_METRICS}


def _rows_by_case(rows: object) -> dict[str, Mapping[str, Any]]:
    items = rows if isinstance(rows, (list, tuple)) else ()
    return {
        _text(row.get('case_id')): row
        for row in items
        if isinstance(row, Mapping) and _text(row.get('case_id'))
    }


def _case_delta(case_id: str, baseline: Mapping[str, Any] | None, candidate: Mapping[str, Any] | None) -> dict[str, Any]:
    before, after = _row_metrics(baseline or {}), _row_metrics(candidate or {})
    delta = {key: round(after[key] - before[key], 4) for key in before}
    if not baseline:
        outcome = 'missing_baseline'
    elif not candidate:
        outcome = 'missing_candidate'
    elif candidate.get('failure_type') == 'infra_failure' or delta['overall_score'] < -0.0001:
        outcome = 'regressed'
    elif delta['overall_score'] > 0.0001:
        outcome = 'improved'
    else:
        outcome = 'unchanged'
    return {'case_id': case_id, 'outcome': outcome, 'before': before, 'after': after, 'delta': delta,
            'baseline_quality': _text((baseline or {}).get('quality_label')),
            'candidate_quality': _text((candidate or {}).get('quality_label'))}


def _row_metrics(row: Mapping[str, Any]) -> dict[str, float]:
    return {key: _float(row.get(key)) for key in COMPARE_METRICS if key != 'correct_rate'}


def _goodcase_guard(case_deltas: list[dict[str, Any]], baseline_rows: Mapping[str, Mapping[str, Any]]) -> dict[str, Any]:
    violations = [row for row in case_deltas
                  if baseline_rows.get(row['case_id'], {}).get('quality_label') == 'good'
                  and row['delta']['overall_score'] < -0.05]
    return {'status': 'failed' if violations else 'passed', 'violations': violations}


def _mapping(value: object, name: str) -> Mapping[str, Any]:
    if not isinstance(value, Mapping):
        raise ValueError(f'{name} must be a mapping')
    return value


def _safe_id(value: str, fallback: str) -> str:
    return SAFE_ID.sub('_', value).strip('._-') or fallback


def _text(value: object) -> str:
    return str(value or '').strip()


def _float(value: object) -> float:
    try:
        return round(float(value or 0.0), 4)
    except (TypeError, ValueError):
        return 0.0


def _int_between(value: object, default: int, low: int, high: int) -> int:
    return max(low, min(high, int(value if value not in (None, '') else default)))
