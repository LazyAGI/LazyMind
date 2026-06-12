from __future__ import annotations

from contextlib import suppress
import json
import os
import shlex
import shutil
import socket
import subprocess
import time
import urllib.parse
import urllib.request
from pathlib import Path
from typing import Any

from ...artifacts import ArtifactDraft, ArtifactRef
from ...runtime import OperationContext, OperationOutput
from ..abtest.cutover import disable_candidate_algorithm

COPY_DIRS = ('lazymind', 'chat', 'common', 'vocab', 'parsing', 'processor')
COPY_FILES = ('.dockerignore', 'Dockerfile', 'config.py', 'requirements.txt')
OLD_CONFIG = """def _model_config_path_post_action(resolved_path):
    if not resolved_path: return
    lazyllm.config['auto_model_config_map_path'] = str(resolved_path)"""
NEW_CONFIG = """def _model_config_path_post_action(resolved_path):
    if not resolved_path: return
    value = str(resolved_path)
    lazyllm.config._impl['auto_model_config_map_path'] = value"""


class PrepareCandidateWorkspaceOperation:
    def execute(self, ctx: OperationContext) -> OperationOutput:
        source = Path(str(ctx.params.get('candidate_source_dir') or default_algorithm_dir()))
        workspace = prepare_candidate_workspace(
            Path(str(ctx.params.get('candidate_workdir') or ctx.draft_dir / 'candidate')), source
        )
        payload = {'id': str(ctx.params.get('output_id') or 'candidate_workspace'), 'workspace_ref': str(workspace),
                   'source_dir': str(source)}
        return _out(ctx, payload, 'CandidateWorkspace', 'candidate_workspace', 'candidate workspace ready')


class StartCandidateServiceOperation:
    def execute(self, ctx: OperationContext) -> OperationOutput:
        workspace = _workspace(ctx)
        command = command_args(ctx.params.get('candidate_service_command'))
        chat_url = str(ctx.params.get('candidate_chat_url') or '')
        health_url = str(ctx.params.get('candidate_healthcheck_url') or '')
        ctx.report_progress(phase='candidate_service', status='running', message='starting candidate service',
                            detail={'workspace_ref': str(workspace), 'command': command})
        proc, health, process = start_candidate_process(
            ctx, workspace, command, chat_url, health_url, workspace / '.evo_repair_logs' / 'candidate_service.log'
        )
        payload = {'id': str(ctx.params.get('output_id') or 'candidate_service'), 'workspace_ref': str(workspace),
                   'service_url': chat_url, 'dataset_name': str(ctx.params.get('dataset_name') or ''),
                   'healthcheck': health, 'process': process}
        ctx.report_progress(phase='candidate_service', status='success', message='candidate service ready',
                            detail={'pid': proc.pid, 'service_url': chat_url})
        return OperationOutput([ArtifactDraft(payload['id'], 'CandidateServiceRun', payload, ctx.operation_run_id)])


class StopCandidateServiceOperation:
    def execute(self, ctx: OperationContext) -> OperationOutput:
        ref = ArtifactRef.parse(str(ctx.params.get('candidate_service_ref') or ''))
        pid = int((ctx.artifact_graph.get(ref).get('process') or {}).get('pid') or 0)
        payload = {'id': str(ctx.params.get('output_id') or 'candidate_service_stop'),
                   'candidate_service_ref': str(ref), 'pid': pid, 'stopped': terminate_pid(pid)}
        return _out(ctx, payload, 'CandidateServiceStop', 'candidate_service', 'candidate service stopped')


def candidate_params(*, run_root: Path, dataset_name: str, overrides: dict[str, Any] | None = None) -> dict[str, Any]:
    params = dict(overrides or {})
    port = int(params.get('candidate_port') or free_port())
    return {'candidate_workdir': str((run_root / 'candidate').resolve()),
            'candidate_source_dir': str(default_algorithm_dir()),
            'candidate_service_command': f'python -m lazymind.chat.app --host 127.0.0.1 --port {port}',
            'candidate_healthcheck_url': f'http://127.0.0.1:{port}/health',
            'candidate_chat_url': f'http://127.0.0.1:{port}/api/chat/stream',
            'dataset_name': dataset_name,
            'opencode_container': os.getenv('EVO_FLOW_OPENCODE_CONTAINER') or default_opencode_container(),
            # Verification is mandatory for patch acceptance; default to a syntax-level
            # check of the patched package, overridable with project-specific commands.
            'verification_commands': ['python -m compileall -q lazymind'],
            'repair_scope': default_repair_scope()} | params


def start_candidate_process(ctx, workspace, command, chat_url, health_url, log_path, *, timeout_s=120):
    if not command or not chat_url or not health_url:
        raise ValueError('candidate_service_command, candidate_chat_url and candidate_healthcheck_url are required')
    if not same_origin(health_url, chat_url):
        raise ValueError('candidate healthcheck and chat URLs must share scheme, host and port')
    log_path.parent.mkdir(parents=True, exist_ok=True)
    log = log_path.open('w', encoding='utf-8')
    try:
        proc = subprocess.Popen(command, cwd=str(workspace), stdout=log, stderr=subprocess.STDOUT, text=True)
    finally:
        log.close()
    ctx.register_cancel_callback(lambda: terminate_pid(proc.pid))
    try:
        wait_health(health_url, proc=proc, timeout_s=timeout_s)
        if proc.poll() is not None: raise RuntimeError('candidate service exited after healthcheck')
    except Exception:
        terminate_pid(proc.pid)
        raise
    return proc, {'status': 'passed', 'healthcheck_url': health_url, 'pid_alive': True}, {
        'pid': proc.pid, 'log_path': str(log_path), 'command': command}


def prepare_candidate_workspace(workspace: Path, source: Path) -> Path:
    workspace, source = workspace.resolve(), source.resolve()
    if not workspace.exists(): copy_source_tree(source, workspace)
    if not _is_algorithm_dir(workspace):
        raise RuntimeError(f'candidate_workdir is not LazyMind algorithm dir: {workspace}')
    ensure_git_baseline(workspace)
    return workspace


def cleanup_candidate_artifacts(run_dir: Path) -> None:
    run_dir = run_dir.resolve()
    manifests = sorted((run_dir / 'artifacts' / 'manifests').glob('candidate_*.json'))
    for cutover in _payloads(run_dir, manifests, 'CandidateAlgorithmCutover'):
        _cleanup_step(disable_candidate_algorithm, cutover)
    for service in _payloads(run_dir, manifests, 'CandidateServiceRun'):
        _cleanup_step(terminate_pid, int((service.get('process') or {}).get('pid') or 0))
    for workspace in _payloads(run_dir, manifests, 'CandidateWorkspace'):
        path = Path(str(workspace.get('workspace_ref') or '')).resolve()
        if path.exists() and path != run_dir and run_dir in path.parents: _cleanup_step(shutil.rmtree, path)


def _cleanup_step(fn, *args) -> None:
    with suppress(Exception):
        fn(*args)


def default_repair_scope() -> dict[str, Any]:
    return {'allowed_roots': ['lazymind/chat'], 'seed_files': [],
            'blocked_roots': ['tests', '.git', 'lazyllm'], 'allow_new_files': True}


def default_algorithm_dir() -> Path:
    here = Path(__file__).resolve()
    for path in (Path('/app/algorithm'), Path.cwd() / 'algorithm', Path.cwd().parent / 'LazyRAG' / 'algorithm',
                 here.parents[4] / 'LazyRAG' / 'algorithm', Path.cwd().parent / 'algorithm'):
        if _is_algorithm_dir(path): return path
    return Path.cwd().parent / 'algorithm'


def default_opencode_container() -> str:
    return '' if Path('/var/lib/lazymind/uploads').exists() else 'lazyrag-evo-api-1'


def copy_source_tree(source: Path, target: Path) -> None:
    if not _is_algorithm_dir(source):
        raise RuntimeError(f'candidate source is not LazyMind algorithm dir: {source}')
    target.mkdir(parents=True, exist_ok=True)
    ignore = shutil.ignore_patterns('.git', '.evo_repair_logs', '__pycache__', '*.pyc')
    for name in COPY_DIRS:
        if (source / name).exists(): shutil.copytree(source / name, target / name, ignore=ignore)
    for name in COPY_FILES:
        if (source / name).exists(): shutil.copy2(source / name, target / name)
    normalize_candidate_config(target / 'config.py')


def normalize_candidate_config(path: Path) -> None:
    if not path.exists(): return
    text = path.read_text(encoding='utf-8')
    updated = text.replace(OLD_CONFIG, NEW_CONFIG)
    if updated != text: path.write_text(updated, encoding='utf-8')


def ensure_git_baseline(workspace: Path) -> None:
    git = ['git', '-c', f'safe.directory={workspace}', '-C', str(workspace)]
    if not (workspace / '.git').exists():
        subprocess.run([*git, 'init'], check=True, capture_output=True, text=True)
    if subprocess.run([*git, 'rev-parse', '--verify', 'HEAD'], capture_output=True, text=True).returncode:
        subprocess.run([*git, 'add', '.'], check=True, capture_output=True, text=True)
        subprocess.run([*git, '-c', 'user.email=evo@example.local', '-c', 'user.name=evo',
                        'commit', '-m', 'baseline'], check=True, capture_output=True, text=True)


def wait_health(url: str, *, proc: subprocess.Popen | None = None, timeout_s: int = 120) -> None:
    deadline, last = time.time() + timeout_s, ''
    while time.time() < deadline:
        if proc and proc.poll() is not None:
            raise RuntimeError(f'candidate service exited with code {proc.returncode}')
        try:
            with urllib.request.urlopen(url, timeout=3) as response:
                if 200 <= response.status < 300: return
                last = f'HTTP {response.status}'
        except Exception as exc:
            last = str(exc)
        time.sleep(1)
    raise TimeoutError(f'candidate service healthcheck timed out: {last}')


def terminate_pid(pid: int, grace_s: float = 10.0) -> bool:
    if pid <= 0 or not _alive(pid): return False
    try:
        os.kill(pid, 15)
        deadline = time.time() + grace_s
        while time.time() < deadline:
            if not _alive(pid): return True
            time.sleep(0.2)
        os.kill(pid, 9)
        return True
    except OSError:
        return False


def free_port() -> int:
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        return int(sock.getsockname()[1])


def command_args(value: Any) -> list[str]:
    return [str(item) for item in value] if isinstance(value, list) else shlex.split(str(value or ''))


def same_origin(left: str, right: str) -> bool:
    a, b = urllib.parse.urlparse(left), urllib.parse.urlparse(right)
    return (a.scheme, a.hostname, a.port) == (b.scheme, b.hostname, b.port)


def _out(ctx, payload, schema, phase, message) -> OperationOutput:
    detail = {key: payload[key] for key in ('workspace_ref', 'pid', 'stopped') if key in payload}
    ctx.report_progress(phase=phase, status='success', message=message, detail=detail)
    return OperationOutput([ArtifactDraft(payload['id'], schema, payload, ctx.operation_run_id)])


def _workspace(ctx: OperationContext) -> Path:
    ref = str(ctx.params.get('candidate_workspace_ref') or '')
    if ref: return Path(str(ctx.artifact_graph.get(ArtifactRef.parse(ref)).get('workspace_ref') or '')).resolve()
    return Path(str(ctx.params.get('candidate_workdir') or '')).resolve()


def _is_algorithm_dir(path: Path) -> bool:
    return (path / 'lazymind' / 'chat' / 'app.py').exists() or (path / 'chat' / 'app' / 'chat.py').exists()


def _alive(pid: int) -> bool:
    try:
        os.kill(pid, 0)
        return True
    except OSError:
        return False


def _payloads(run_dir: Path, manifests: list[Path], schema_name: str) -> list[dict[str, Any]]:
    out = []
    for path in manifests:
        manifest = json.loads(path.read_text(encoding='utf-8'))
        if manifest.get('schema_name') != schema_name: continue
        for version in manifest.get('versions') or []:
            payload = run_dir / str(version.get('payload_ref') or '')
            if payload.exists(): out.append(json.loads(payload.read_text(encoding='utf-8')))
    return out
