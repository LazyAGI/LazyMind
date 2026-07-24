from __future__ import annotations

import hashlib
import json
import os
import re
from collections.abc import Mapping
from pathlib import Path
from typing import Any

from evo.operations.eval.answer import answer_case, case_kb_id, failed_rag_answer
from evo.operations.route.router_algorithm import ensure_owned_algorithm, manage_owned_algorithm
from evo.operations.route.router_ledger import RouterAlgorithmLedger
from evo.operations.route.router_manager import (
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
DEFAULT_MAX_RETRIES = '8'
PATCH_STATUSES = {'verified', 'unvalidated'}
SAFE_ID = re.compile(r'[^A-Za-z0-9_.-]+')


def candidate_service(
    config: Mapping[str, Any],
    patch: Mapping[str, Any],
    ctx: Any | None = None,
    workspace: Mapping[str, Any] | None = None,
) -> dict[str, Any]:
    patch = _candidate_patch(patch, workspace or {})
    base = {'candidate_config': dict(config), 'patch_status': _text(patch.get('status'))}
    if not _text(patch.get('diff')):
        return base | _failed('', '', '', '', 'invalid_repair_patch', 'repair patch has empty diff')
    if _text(patch.get('status')) not in PATCH_STATUSES:
        return base | _failed(
            '', '', '', '', 'invalid_repair_patch',
            f"candidate evaluation requires final repair patch, got {_text(patch.get('status'))}",
        )

    algorithm_id = router_chat_url = admin_url = code_path = ''
    manager: RouterManager | None = None
    try:
        algorithm_id = _algorithm_id(config, patch, _text(getattr(ctx, 'run_id', 'run')))
        router_chat_url = normalize_chat_url(_required(config, 'router_chat_url'))
        admin_url = _required(config, 'router_admin_url')
        manager = RouterManager(admin_url, router_chat_url)
        code_path = _code_path(config, patch)
        spec = RouterAlgorithmSpec(
            id=algorithm_id,
            name=_text(config.get('name')) or algorithm_id,
            code_path=code_path,
            instance_count=_int_between(config.get('instance_count'), 1, 1, 4),
            config=_environment(config, algorithm_id),
        )
        root = _required(os.environ, 'LAZYMIND_EVO_BASE_DIR')
        run_id = _text(getattr(ctx, 'run_id', ''))
        if not run_id:
            raise ValueError('candidate materializer requires ctx.run_id')
        output = next(iter(getattr(ctx, 'output_key_by_name', {}).values()), None)
        timeout_s = _int_between(
            config.get('startup_timeout_s') or config.get('startup_timeout_seconds'),
            180,
            10,
            900,
        )
        owner = {
            'thread_id': run_id,
            'run_id': run_id,
            'candidate_ref': str(getattr(output, 'artifact_id', 'abtest.candidate_service')),
            'cleanup_policy': 'thread_delete',
        }
        registration, detail = ensure_owned_algorithm(
            manager,
            RouterAlgorithmLedger(Path(root) / 'artifact-store'),
            spec,
            owner,
            timeout_s=timeout_s,
        )
        return base | {
            'status': 'ready',
            'service_kind': 'router_algorithm',
            'algorithm_id': algorithm_id,
            'router_chat_url': manager.router_chat_url,
            'router_admin_url': manager.router_admin_url,
            'cleanup_allowed': any(registration.get(key) is True for key in ('created', 'reactivated')),
            'workspace_ref': _text(patch.get('workspace_ref')),
            'code_path': code_path,
            'register_request': spec.payload(),
            'register_response': registration,
            'healthcheck': manager.healthcheck_from_detail(detail),
        }
    except RouterManagerError as exc:
        return base | _failed(
            algorithm_id,
            router_chat_url,
            manager.router_admin_url if manager else admin_url,
            code_path,
            exc.kind,
            str(exc),
        )
    except Exception as exc:
        return base | _failed(algorithm_id, router_chat_url, admin_url, code_path, type(exc).__name__, str(exc))


def candidate_rag_answer(case: Mapping[str, Any], service: Mapping[str, Any]) -> dict[str, Any]:
    config = service.get('candidate_config') if isinstance(service.get('candidate_config'), Mapping) else {}
    target_config = dict(config) | {
        'router_chat_url': service.get('router_chat_url'),
        'router_admin_url': service.get('router_admin_url'),
        'algorithm_id': service.get('algorithm_id'),
    }
    if service.get('status') == 'ready':
        return answer_case(case, target_config)
    target = {
        'router_chat_url': _text(target_config.get('router_chat_url')),
        'router_admin_url': _text(target_config.get('router_admin_url')),
        'algorithm_id': _text(target_config.get('algorithm_id')),
        'kb_id': case_kb_id(case, target_config),
    }
    health = service.get('healthcheck') if isinstance(service.get('healthcheck'), Mapping) else {}
    return failed_rag_answer(
        case,
        {},
        target,
        'candidate_service_unavailable',
        _text(health.get('message')) or 'candidate not ready',
    )


def stop_candidate(service: Mapping[str, Any] | None) -> dict[str, Any]:
    if not service or service.get('status') != 'ready':
        return {'status': 'not_applicable', 'reason': 'candidate_not_ready'}
    if service.get('cleanup_allowed') is not True:
        return {'status': 'not_applicable', 'reason': 'candidate_not_owned'}
    algorithm_id = _text(service.get('algorithm_id'))
    if not algorithm_id.startswith('evo_'):
        return {'status': 'not_applicable', 'reason': 'candidate_not_owned'}
    manager = RouterManager(
        _required(service, 'router_admin_url'),
        _required(service, 'router_chat_url'),
    )
    try:
        root = _required(os.environ, 'LAZYMIND_EVO_BASE_DIR')
        manage_owned_algorithm(
            manager,
            RouterAlgorithmLedger(Path(root) / 'artifact-store'),
            algorithm_id,
            'stop',
            timeout_s=0,
        )
        return {'status': 'completed', 'algorithm_id': algorithm_id}
    except RouterManagerError as exc:
        return {'status': 'failed', 'algorithm_id': algorithm_id, 'error_type': exc.kind, 'message': str(exc)}
    except Exception as exc:
        return {'status': 'failed', 'algorithm_id': algorithm_id, 'error_type': 'ledger_error', 'message': str(exc)}


def _candidate_patch(patch: Mapping[str, Any], workspace: Mapping[str, Any]) -> dict[str, Any]:
    diff = patch.get('diff')
    return {
        'status': patch.get('status'),
        'workspace_ref': _text(patch.get('workspace_ref')) or workspace.get('workspace_ref'),
        'diff': ''.join(str(value) for value in diff.values()) if isinstance(diff, Mapping) else _text(diff),
    }


def _algorithm_id(config: Mapping[str, Any], patch: Mapping[str, Any], run_id: str) -> str:
    explicit = _text(config.get('algorithm_id'))
    if explicit:
        value = _safe_id(explicit, 'evo_candidate')[:64]
        if not value.startswith('evo_'):
            raise ValueError('candidate_config.algorithm_id must start with evo_')
        return value
    digest = hashlib.sha1(json.dumps(
        {'workspace': patch.get('workspace_ref'), 'diff': patch.get('diff')},
        sort_keys=True,
        default=str,
    ).encode()).hexdigest()[:10]
    return f'evo_{_safe_id(_text(config.get("thread_id") or run_id), "run")}_{digest}'[:64]


def _environment(config: Mapping[str, Any], algorithm_id: str) -> dict[str, str]:
    kb_name = _text(config.get('agentic_kb_name') or os.getenv('LAZYMIND_AGENTIC_KB_NAME') or 'general_algo')
    env = {
        'LAZYMIND_ALGO_ID': _text(config.get('algo_id')) or kb_name,
        'LAZYMIND_AGENTIC_KB_NAME': kb_name,
        'LAZYMIND_ROUTER_ALGORITHM_ID': algorithm_id,
        'LAZYMIND_MAX_RETRIES': _max_retries(config),
        'LAZYMIND_ENABLE_ROUTER': 'false',
        'LAZYMIND_ROUTER_CHILD_PROXIED_ONLY': 'true',
    }
    env.update({key: _text(os.getenv(key)) for key in ENV_PASSTHROUGH if _text(os.getenv(key))})
    extra = config.get('env') if isinstance(config.get('env'), Mapping) else {}
    env.update({_text(key): _text(value) for key, value in extra.items() if _text(key) and _text(value)})
    return env


def _code_path(config: Mapping[str, Any], patch: Mapping[str, Any]) -> str:
    workspace = Path(_required(patch, 'workspace_ref')).as_posix().rstrip('/')
    expected = f'{workspace}/algorithm/lazymind/chat'
    explicit = Path(_text(config.get('code_path'))).as_posix().rstrip('/') if _text(config.get('code_path')) else ''
    if explicit and explicit != expected:
        raise ValueError('candidate_config.code_path must match final repair patch workspace')
    return expected


def _max_retries(config: Mapping[str, Any]) -> str:
    value = _text(config.get('max_retries') or os.getenv('LAZYMIND_EVO_CHAT_MAX_RETRIES'))
    return value if value.isdigit() and int(value) > 0 else DEFAULT_MAX_RETRIES


def _failed(
    algorithm_id: str,
    router_chat_url: str,
    router_admin_url: str,
    code_path: str,
    error_type: str,
    message: str,
) -> dict[str, Any]:
    return {
        'status': 'failed',
        'service_kind': 'router_algorithm',
        'algorithm_id': algorithm_id,
        'router_chat_url': router_chat_url,
        'router_admin_url': router_admin_url,
        'code_path': code_path,
        'healthcheck': {'status': 'failed', 'type': error_type, 'message': message},
    }


def _required(value: Mapping[str, Any], key: str) -> str:
    result = _text(value.get(key))
    if not result:
        raise ValueError(f'{key} is required')
    return result


def _safe_id(value: str, fallback: str) -> str:
    return SAFE_ID.sub('_', value).strip('._-') or fallback


def _text(value: object) -> str:
    return str(value or '').strip()


def _int_between(value: object, default: int, low: int, high: int) -> int:
    return max(low, min(high, int(value if value not in (None, '') else default)))
