from __future__ import annotations

import json
import os
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path
from typing import Any

from ...artifacts import ArtifactDraft, ArtifactRef
from ...ids import validate_id
from ...runtime import OperationContext, OperationOutput


class CutoverCandidateAlgorithmOperation:
    def execute(self, ctx: OperationContext) -> OperationOutput:
        comparison_ref = _ref(ctx, 'abtest_comparison_ref', 'ABTestComparison')
        workspace_ref = _ref(ctx, 'candidate_workspace_ref', 'CandidateWorkspace')
        comparison, workspace = ctx.artifact_graph.get(comparison_ref), ctx.artifact_graph.get(workspace_ref)
        if (comparison.get('decision') or {}).get('status') != 'accept':
            raise ValueError('candidate cutover requires accepted ABTestComparison')
        router_url = origin(str(ctx.params.get('router_url') or ctx.params.get('target_chat_url') or ''))
        algorithm_id = _algorithm_id(ctx)
        code_path = _code_path(workspace, ctx.params.get('code_path'))
        ctx.report_progress(phase='abtest_cutover', status='running', message='registering parser algorithm',
                            detail={'algorithm_id': algorithm_id, 'workspace_ref': workspace.get('workspace_ref')})
        parser_registration = register_parser_algorithm(workspace, algorithm_id)
        body = {'id': algorithm_id, 'name': algorithm_id, 'code_path': code_path,
                'instance_count': int(ctx.params.get('instance_count') or 1),
                'config': candidate_env(ctx.params, algorithm_id)}
        ctx.report_progress(phase='abtest_cutover', status='running', message='registering candidate algorithm',
                            detail={'router_url': router_url, 'algorithm_id': algorithm_id, 'code_path': code_path})
        registered: dict[str, Any] = {}
        try:
            registered = request_json('POST', f'{router_url}/inner/algorithm/register', body)
            weights = _weights(algorithm_id, int(ctx.params.get('candidate_weight') or 100))
            ctx.report_progress(phase='abtest_cutover', status='running', message='switching chat traffic',
                                detail={'router_url': router_url, 'weights': weights})
            strategy = request_json('PUT', f'{router_url}/inner/ab/strategy', {'weights': weights})
        except Exception:
            if registered:
                _try_request_json('DELETE', f'{router_url}/inner/algorithm/{urllib.parse.quote(algorithm_id)}')
            drop_parser_algorithm(Path(str(workspace.get('workspace_ref') or '')), algorithm_id)
            raise
        payload = {'id': str(ctx.params.get('output_id') or 'candidate_algorithm_cutover'),
                   'algorithm_id': algorithm_id, 'router_url': router_url, 'code_path': code_path,
                   'workspace_ref': str(workspace.get('workspace_ref') or ''),
                   'parser_registration': parser_registration, 'abtest_comparison_ref': str(comparison_ref),
                   'candidate_workspace_ref': str(workspace_ref), 'register_response': registered,
                   'strategy': strategy, 'weights': weights, 'status': 'active'}
        ctx.report_progress(phase='abtest_cutover', status='success', message='candidate algorithm cutover finished',
                            detail={'algorithm_id': algorithm_id, 'ports': registered.get('ports', [])})
        return OperationOutput([ArtifactDraft(payload['id'], 'CandidateAlgorithmCutover', payload,
                                              ctx.operation_run_id, [comparison_ref, workspace_ref])])


def disable_candidate_algorithm(payload: dict[str, Any]) -> None:
    router_url, algorithm_id = str(payload.get('router_url') or ''), str(payload.get('algorithm_id') or '')
    if router_url and algorithm_id and payload.get('status') == 'active':
        try:
            strategy = request_json('GET', f'{router_url}/inner/ab/strategy')
            if algorithm_id in ((strategy.get('strategy') or {}).get('weights') or {}):
                request_json('DELETE', f'{router_url}/inner/ab/strategy')
            request_json('DELETE', f'{router_url}/inner/algorithm/{urllib.parse.quote(algorithm_id)}')
        finally:
            drop_parser_algorithm(Path(str(payload.get('workspace_ref') or '')), algorithm_id)


def register_parser_algorithm(workspace: dict[str, Any], algorithm_id: str) -> dict[str, Any]:
    root = Path(str(workspace.get('workspace_ref') or '')).resolve()
    _run_parser_command(root, 'register_parser_algorithm', algorithm_id)
    return {'status': 'registered', 'algorithm_id': algorithm_id}


def drop_parser_algorithm(root: Path, algorithm_id: str) -> None:
    if root.exists() and algorithm_id:
        _run_parser_command(root.resolve(), 'drop_parser_algorithm', algorithm_id)


def candidate_env(params: dict[str, Any], algorithm_id: str) -> dict[str, str]:
    doc_source = params.get('document_server_url') or os.getenv('LAZYMIND_EVO_DOCUMENT_SERVER_URL')
    doc_url = str(doc_source or 'http://parsing:8000').split(',', 1)[0].rstrip('/')
    env = {'LAZYMIND_ALGO_ID': algorithm_id, 'LAZYMIND_AGENTIC_KB_NAME': algorithm_id,
           'LAZYMIND_DOCUMENT_SERVER_URL': f'{doc_url},{algorithm_id}'}
    for key in ('LAZYMIND_DOCUMENT_PROCESSOR_URL', 'LAZYMIND_MILVUS_URI', 'LAZYMIND_OPENSEARCH_URI',
                'LAZYMIND_OPENSEARCH_USER', 'LAZYMIND_OPENSEARCH_PASSWORD', 'LAZYMIND_MODEL_CONFIG_PATH'):
        if value := os.getenv(key):
            env[key] = value
    return env | {k: str(v) for k, v in dict(params.get('config') or {}).items()}


def _run_parser_command(root: Path, func: str, algorithm_id: str) -> None:
    if not (root / 'lazymind' / 'parsing' / 'service' / 'build_document.py').exists():
        raise ValueError(f'candidate parser code not found: {root}')
    env = os.environ.copy()
    env['PYTHONPATH'] = os.pathsep.join([str(root), '/opt/lazyllm', env.get('PYTHONPATH', '')])
    code = (
        'from lazymind.parsing.service.build_document import {func}; '
        '{func}({algorithm_id})'
    ).format(func=func, algorithm_id=json.dumps(algorithm_id))
    run = subprocess.run([sys.executable, '-c', code], cwd=str(root), env=env, capture_output=True, text=True)
    if run.returncode:
        detail = (run.stderr or run.stdout or '').strip()[-2000:]
        raise RuntimeError(f'{func} failed for {algorithm_id}: {detail}')


def request_json(method: str, url: str, payload: dict[str, Any] | None = None, *, timeout_s: int = 180) -> dict:
    data = None if payload is None else json.dumps(payload).encode('utf-8')
    req = urllib.request.Request(url, data=data, method=method, headers={'Content-Type': 'application/json'})
    try:
        with urllib.request.urlopen(req, timeout=timeout_s) as response:
            raw = response.read().decode('utf-8')
    except urllib.error.HTTPError as exc:
        detail = exc.read().decode('utf-8', errors='replace')
        raise RuntimeError(f'{method} {url} failed: HTTP {exc.code} {detail}') from exc
    return json.loads(raw) if raw else {}


def _try_request_json(method: str, url: str) -> None:
    try:
        request_json(method, url)
    except Exception:
        pass


def origin(url: str) -> str:
    parsed = urllib.parse.urlparse(url)
    if not parsed.scheme or not parsed.netloc:
        raise ValueError(f'invalid router_url or target_chat_url: {url!r}')
    return f'{parsed.scheme}://{parsed.netloc}'


def _algorithm_id(ctx: OperationContext) -> str:
    value = str(ctx.params.get('algorithm_id') or f'evo_{ctx.run_id}_{int(time.time())}')
    return validate_id(value.replace('@', '_'), 'algorithm_id')


def _code_path(workspace: dict[str, Any], override: Any = None) -> str:
    path = Path(str(override or workspace.get('workspace_ref') or '')).resolve()
    chat_path = path if (path / 'app.py').exists() else path / 'lazymind' / 'chat'
    if not chat_path.exists():
        raise ValueError(f'candidate chat code_path not found: {chat_path}')
    return str(chat_path)


def _weights(algorithm_id: str, candidate_weight: int) -> dict[str, int]:
    weight = max(0, min(100, candidate_weight))
    return {key: value for key, value in {'default': 100 - weight, algorithm_id: weight}.items() if value > 0}


def _ref(ctx: OperationContext, name: str, schema: str) -> ArtifactRef:
    ref = ArtifactRef.parse(str(ctx.params.get(name) or ''))
    if ctx.artifact_graph.schema_name(ref) != schema:
        raise ValueError(f'{name} must be {schema}: {ref}')
    return ref
