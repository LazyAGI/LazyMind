from __future__ import annotations

import argparse
import base64
import binascii
import hashlib
import io
import json
import os
import secrets
import shutil
import signal
import socketserver
import struct
import subprocess
import sys
import tarfile
import tempfile
import threading
import time
from pathlib import Path
from typing import Any, Optional

from .protocol import (
    ENV_NAME,
    FORBIDDEN_ENV,
    HEADER_BYTES,
    MAX_REQUEST_BYTES,
    MAX_RESPONSE_BYTES,
    PROTOCOL_VERSION,
    SUPPORTED_RUNTIMES,
    error_result,
    normalize_domains,
    safe_relative_path,
    safe_output_pattern,
)
from .egress_proxy import control_request


DEFAULT_COMMANDS = frozenset({
    'awk', 'bash', 'cat', 'cp', 'curl', 'cut', 'date', 'diff', 'dirname',
    'echo', 'env', 'false', 'ffmpeg', 'find', 'git', 'grep', 'head', 'jq',
    'ls', 'mkdir', 'mv', 'node', 'npm', 'npx', 'pandoc', 'printf', 'python',
    'python3', 'rg', 'sed', 'sh', 'sort', 'tail', 'tar', 'test', 'touch',
    'tr', 'true', 'uname', 'uniq', 'unzip', 'wc', 'xargs', 'zip',
})
RUNTIMES = {
    '.py': 'python3',
    '.sh': 'bash',
    '.bash': 'bash',
    '.js': 'node',
    '.cjs': 'node',
    '.mjs': 'node',
}
FIXED_PATH = '/usr/local/bin:/usr/bin:/bin'


class SandboxExecutionEngine:
    def __init__(
        self,
        root: str,
        *,
        allowed_commands: Optional[set[str]] = None,
        max_bundle_bytes: int = 32 * 1024 * 1024,
        max_unpacked_bytes: int = 64 * 1024 * 1024,
        max_files: int = 2048,
        session_ttl_seconds: int = 3600,
        sandbox_uid_base: Optional[int] = 20000,
        sandbox_uid_count: int = 30000,
        egress_control_socket: Optional[str] = None,
        egress_proxy_url: str = 'http://sandbox-egress:18081',
        max_output_file_bytes: int = 1024 * 1024,
    ):
        self.root = Path(root)
        self.root.mkdir(parents=True, exist_ok=True)
        os.chmod(self.root, 0o711)
        self.allowed_commands = frozenset(allowed_commands or DEFAULT_COMMANDS)
        self.max_bundle_bytes = max(1024, int(max_bundle_bytes))
        self.max_unpacked_bytes = max(self.max_bundle_bytes, int(max_unpacked_bytes))
        self.max_files = max(1, int(max_files))
        self.session_ttl_seconds = max(60, int(session_ttl_seconds))
        self.sandbox_uid_base = sandbox_uid_base
        self.sandbox_uid_count = max(1, int(sandbox_uid_count))
        self.egress_control_socket = egress_control_socket
        self.egress_proxy_url = egress_proxy_url.rstrip('/')
        self.max_output_file_bytes = max(1024, int(max_output_file_bytes))
        self._state_lock = threading.Lock()
        self._workspace_locks: dict[str, threading.Lock] = {}
        self._session_uids: dict[str, int] = {}
        self._used_uids: set[int] = set()
        self._next_uid = 0
        self._active_processes: dict[str, tuple[subprocess.Popen, str]] = {}
        self._cancelled_requests: set[str] = set()
        self._releasing_sessions: set[str] = set()

    def capabilities(self) -> dict[str, Any]:
        commands = sorted(
            command for command in self.allowed_commands
            if shutil.which(command, path=FIXED_PATH) is not None
        )
        runtimes = {
            name: shutil.which(executable, path=FIXED_PATH) is not None
            for name, executable in {'python': 'python3', 'bash': 'bash', 'node': 'node'}.items()
        }
        return {
            'status': 'ok',
            'protocol_version': PROTOCOL_VERSION,
            'runtimes': runtimes,
            'commands': commands,
            'profiles': ['base'],
            'image_profile': 'base-v1',
            'network_modes': ['deny', 'domain_allowlist'] if self.egress_control_socket else ['deny'],
            'supports': {
                'script': True,
                'command': True,
                'preflight': True,
                'declared_outputs': True,
                'session_workspace': True,
                'cancel': True,
                'listen_ports': False,
            },
        }

    def execute(self, request: dict[str, Any]) -> dict[str, Any]:
        if request.get('version') != PROTOCOL_VERSION:
            return error_result('SANDBOX_UNAVAILABLE', 'Unsupported sandbox protocol version.')
        try:
            preflight = self.preflight(request)
            if preflight.get('status') != 'ok':
                return preflight
            session_id = str(request.get('session_id') or '').strip()
            if not session_id or len(session_id) > 512:
                raise ValueError('session_id must contain between 1 and 512 characters.')
            bundle_data, bundle_sha = self._decode_bundle(request.get('bundle'))
            if request.get('tree_hash') != bundle_sha:
                raise ValueError('tree_hash must match the uploaded Skill package digest.')
            session_key = hashlib.sha256(session_id.encode('utf-8')).hexdigest()
            with self._state_lock:
                if session_key in self._releasing_sessions:
                    return error_result(
                        'SANDBOX_UNAVAILABLE',
                        'The sandbox session is being released.',
                    )
            workspace_key = f'{session_key}/{bundle_sha}'
            lock = self._workspace_lock(workspace_key)
            with lock:
                uid = self._uid_for_session(session_key)
                skill_root, workspace = self._prepare_workspace(
                    session_key, bundle_sha, bundle_data, uid,
                )
                result = self._execute_in_workspace(request, skill_root, workspace, uid, session_key)
                self._touch(workspace.parent)
            self._cleanup_stale(exclude=workspace_key)
            return result
        except ValueError as exc:
            return error_result('FILESYSTEM_DENIED', str(exc), blocked_resource='skill_package')
        except Exception as exc:
            return error_result('SANDBOX_UNAVAILABLE', f'Sandbox runner failed: {type(exc).__name__}: {exc}')

    def preflight(self, request: dict[str, Any]) -> dict[str, Any]:
        try:
            request_id = str(request.get('request_id') or '').strip()
            if not request_id or len(request_id) > 512:
                return error_result(
                    'COMMAND_DENIED',
                    'request_id must contain between 1 and 512 characters.',
                )
            runtime = str(request.get('runtime') or '')
            if runtime not in SUPPORTED_RUNTIMES:
                return error_result('RUNTIME_NOT_SUPPORTED', f'Unsupported Skill runtime: {runtime or "(missing)"}')
            mode = request.get('mode')
            if (mode == 'command') != (runtime == 'cli'):
                return error_result('RUNTIME_NOT_SUPPORTED', 'runtime and execution mode do not match.')
            safe_relative_path(request.get('cwd'), 'cwd', allow_dot=True)
            self._relative_list(request.get('input_files') or [], 'input_files')
            self._output_patterns(request.get('declared_outputs') or [])
            policy = self._request_policy(request)
            if policy is None:
                return error_result('COMMAND_DENIED', 'Skill execution policy must be an object.')
            profile = str(policy.get('runtime_profile') or 'base')
            if profile != 'base':
                return error_result(
                    'RUNTIME_NOT_SUPPORTED',
                    f'Runtime profile is not available: {profile}',
                    dependency=profile,
                )
            try:
                domains = normalize_domains(policy.get('allowed_domains') or [])
            except ValueError as exc:
                return error_result(
                    'NETWORK_DENIED', str(exc), blocked_resource='allowed_domains',
                )
            network = request.get('network_policy') or request.get('network')
            expected_mode = 'domain_allowlist' if domains else 'deny'
            if not isinstance(network, dict) or network.get('mode') != expected_mode:
                return error_result(
                    'NETWORK_DENIED',
                    f'Skill sandbox request must use network mode {expected_mode}.',
                    blocked_resource='network_policy',
                )
            if domains and not self.egress_control_socket:
                return error_result(
                    'NETWORK_DENIED',
                    'This runner has no controlled egress proxy configured.',
                    blocked_resource=domains[0],
                    allowed_domains=domains,
                )
            required_bins = self._string_list(policy.get('required_bins') or [], 'required_bins')
            denied_bins = sorted({name for name in required_bins if name not in self.allowed_commands})
            if denied_bins:
                return error_result(
                    'COMMAND_DENIED',
                    f'Required CLI is not allowed by the sandbox image: {denied_bins[0]}',
                    dependency=denied_bins[0],
                    blocked_resource=denied_bins[0],
                )
            missing_bins = sorted({
                name for name in required_bins if shutil.which(name, path=FIXED_PATH) is None
            })
            if missing_bins:
                return error_result(
                    'COMMAND_NOT_FOUND',
                    f'Required CLI is not installed in the sandbox image: {missing_bins[0]}',
                    dependency=missing_bins[0],
                    available_commands=self.capabilities()['commands'],
                )
            any_bins = self._string_list(policy.get('any_bins') or [], 'any_bins')
            allowed_alternatives = [name for name in any_bins if name in self.allowed_commands]
            if any_bins and not allowed_alternatives:
                return error_result(
                    'COMMAND_DENIED',
                    f'None of the alternative CLIs are allowed by the sandbox image: {", ".join(any_bins)}',
                    blocked_resource=any_bins[0],
                )
            if allowed_alternatives and not any(
                shutil.which(name, path=FIXED_PATH) for name in allowed_alternatives
            ):
                return error_result(
                    'COMMAND_NOT_FOUND',
                    f'None of the alternative CLIs are installed in the sandbox image: '
                    f'{", ".join(allowed_alternatives)}',
                    dependency=allowed_alternatives[0],
                    available_commands=self.capabilities()['commands'],
                )
            env_result = self._validate_env(request.get('env'), policy)
            if env_result is not None:
                return env_result
            limits_result = self._limits(request.get('limits'))
            if isinstance(limits_result, dict):
                return limits_result
            argv_result = self._resolve_argv(request, None)
            if isinstance(argv_result, dict):
                return argv_result
            return {'status': 'ok', 'runtime': runtime, 'profile': profile}
        except ValueError as exc:
            return error_result('FILESYSTEM_DENIED', str(exc), blocked_resource='request')

    def cancel(self, request_id: str) -> dict[str, Any]:
        request_id = str(request_id or '').strip()
        with self._state_lock:
            record = self._active_processes.get(request_id)
            if record is None:
                return {'status': 'not_found', 'request_id': request_id}
            process, _session_key = record
            self._cancelled_requests.add(request_id)
        self._kill_process_group(process.pid)
        return {'status': 'ok', 'request_id': request_id}

    def release_session(self, session_id: str) -> dict[str, Any]:
        raw = str(session_id or '').strip()
        if not raw:
            return error_result('FILESYSTEM_DENIED', 'session_id is required.')
        session_key = hashlib.sha256(raw.encode('utf-8')).hexdigest()
        with self._state_lock:
            self._releasing_sessions.add(session_key)
            request_ids = [
                request_id for request_id, (_process, key) in self._active_processes.items()
                if key == session_key
            ]
        try:
            for request_id in request_ids:
                self.cancel(request_id)
            deadline = time.monotonic() + 5
            while time.monotonic() < deadline:
                with self._state_lock:
                    active = any(
                        key == session_key for _process, key in self._active_processes.values()
                    )
                if not active:
                    break
                time.sleep(0.01)
            with self._state_lock:
                locks = [
                    lock for key, lock in self._workspace_locks.items()
                    if key.startswith(session_key + '/')
                ]
            for lock in locks:
                lock.acquire()
            try:
                self._remove_tree(self.root / session_key)
            finally:
                for lock in reversed(locks):
                    lock.release()
            with self._state_lock:
                uid = self._session_uids.pop(session_key, None)
                if uid is not None:
                    self._used_uids.discard(uid)
                for key in list(self._workspace_locks):
                    if key.startswith(session_key + '/'):
                        self._workspace_locks.pop(key, None)
            return {'status': 'ok', 'session_id': raw, 'cancelled': len(request_ids)}
        finally:
            with self._state_lock:
                self._releasing_sessions.discard(session_key)

    def _execute_in_workspace(
        self,
        request: dict[str, Any],
        skill_root: Path,
        workspace: Path,
        uid: Optional[int],
        session_key: str,
    ) -> dict[str, Any]:
        policy = self._request_policy(request) or {}
        domains = normalize_domains(policy.get('allowed_domains') or [])
        egress_token = secrets.token_urlsafe(32) if self.egress_control_socket else None
        env_result = self._execution_env(request.get('env'), policy, workspace, uid, egress_token)
        if isinstance(env_result, dict) and env_result.get('status') == 'error':
            return env_result
        run_env = env_result
        try:
            cwd = safe_relative_path(request.get('cwd'), 'cwd', allow_dot=True)
        except ValueError as exc:
            return error_result('FILESYSTEM_DENIED', str(exc), blocked_resource='cwd')
        run_cwd = workspace if cwd == '.' else workspace.joinpath(*cwd.split('/'))
        for rel_path in self._relative_list(request.get('input_files') or [], 'input_files'):
            source = skill_root.joinpath(*rel_path.split('/'))
            if source.is_symlink() or not source.is_file():
                return error_result(
                    'FILESYSTEM_DENIED',
                    f'Declared input file was not found in the Skill package: {rel_path}',
                    blocked_resource=rel_path,
                )
        try:
            resolved_workspace = workspace.resolve(strict=True)
            resolved_cwd = run_cwd.resolve(strict=True)
        except FileNotFoundError:
            return error_result(
                'FILESYSTEM_DENIED',
                f'Working directory does not exist in the Skill workspace: {cwd}',
                blocked_resource=cwd,
            )
        if (
            os.path.commonpath((resolved_workspace, resolved_cwd)) != str(resolved_workspace)
            or not resolved_cwd.is_dir()
        ):
            return error_result(
                'FILESYSTEM_DENIED',
                'Working directory must stay inside the Skill workspace.',
                blocked_resource=cwd,
            )
        argv_result = self._resolve_argv(request, skill_root)
        if isinstance(argv_result, dict):
            return argv_result
        argv = argv_result
        limits_result = self._limits(request.get('limits'))
        if isinstance(limits_result, dict):
            return limits_result
        if egress_token:
            try:
                response = control_request(self.egress_control_socket, {
                    'action': 'register',
                    'token': egress_token,
                    'domains': domains,
                    'ttl_seconds': limits_result[0] + 10,
                })
            except (OSError, ValueError) as exc:
                return error_result(
                    'SANDBOX_UNAVAILABLE',
                    f'Controlled egress proxy is unavailable: {exc}',
                    blocked_resource=domains[0] if domains else 'network_policy',
                )
            if response.get('status') != 'ok':
                return error_result(
                    'NETWORK_DENIED',
                    str(response.get('error') or 'Controlled egress policy was rejected.'),
                    blocked_resource=domains[0] if domains else 'network_policy',
                )
        try:
            result = self._run(
                argv, resolved_cwd, run_env, limits_result, uid,
                str(request.get('request_id') or ''), session_key,
            )
            if egress_token:
                try:
                    proxy_status = control_request(self.egress_control_socket, {
                        'action': 'status', 'token': egress_token,
                    })
                except (OSError, ValueError) as exc:
                    return error_result(
                        'SANDBOX_UNAVAILABLE',
                        f'Could not verify the controlled egress result: {exc}',
                    )
                violation = proxy_status.get('violation')
                if isinstance(violation, dict):
                    message = str(
                        violation.get('reason') or 'Network destination was denied by policy.'
                    )
                    blocked_resource = (
                        violation.get('blocked_resource')
                        or (domains[0] if domains else 'network_policy')
                    )
                    result.update({
                        'status': 'failed',
                        'reason_code': 'NETWORK_DENIED',
                        'error_type': 'NETWORK_DENIED',
                        'error': message,
                        'blocked_resource': str(blocked_resource),
                    })
                    result['stderr'] = '\n'.join(
                        part for part in (str(result.get('stderr') or '').strip(), message)
                        if part
                    )
                    return result
            if result.get('status') == 'ok':
                outputs = self._collect_outputs(
                    workspace,
                    self._output_patterns(request.get('declared_outputs') or []),
                )
                if isinstance(outputs, dict):
                    return outputs
                result['output_files'] = outputs
            return result
        finally:
            if egress_token:
                try:
                    control_request(self.egress_control_socket, {
                        'action': 'unregister', 'token': egress_token,
                    })
                except (OSError, ValueError):
                    pass

    def _resolve_argv(
        self, request: dict[str, Any], skill_root: Optional[Path],
    ) -> list[str] | dict[str, Any]:
        mode = request.get('mode')
        try:
            supplied = self._string_list(request.get('argv') or [], 'argv')
        except ValueError as exc:
            return error_result('COMMAND_DENIED', str(exc))
        blocked_argument = next((item for item in supplied if self._unsafe_path_argument(item)), None)
        if blocked_argument is not None:
            return error_result(
                'FILESYSTEM_DENIED',
                'Absolute paths and parent-directory traversal are not allowed in sandbox arguments.',
                blocked_resource=blocked_argument,
            )
        if mode == 'script':
            try:
                entrypoint = safe_relative_path(request.get('entrypoint'), 'entrypoint')
            except ValueError as exc:
                return error_result('FILESYSTEM_DENIED', str(exc), blocked_resource='entrypoint')
            script = skill_root.joinpath(*entrypoint.split('/')) if skill_root is not None else None
            if script is not None and not script.is_file():
                return error_result(
                    'FILESYSTEM_DENIED',
                    f'Skill script was not found: {entrypoint}',
                    blocked_resource=entrypoint,
                )
            suffix = (script.suffix if script is not None else Path(entrypoint).suffix).lower()
            runtime = RUNTIMES.get(suffix)
            if runtime is None:
                return error_result(
                    'RUNTIME_NOT_SUPPORTED',
                    f'Unsupported Skill script extension: {suffix or "(none)"}',
                )
            executable = shutil.which(runtime, path=FIXED_PATH)
            if executable is None:
                return error_result(
                    'RUNTIME_NOT_FOUND',
                    f'Required runtime is not installed in the sandbox image: {runtime}',
                    dependency=runtime,
                )
            expected_runtime = {
                'python3': 'python', 'bash': 'bash', 'node': 'node',
            }[runtime]
            if request.get('runtime') != expected_runtime:
                return error_result('RUNTIME_NOT_SUPPORTED', 'runtime does not match the script extension.')
            if script is None:
                return [executable, entrypoint, *supplied]
            return [executable, str(script), *supplied]
        if mode != 'command':
            return error_result('COMMAND_DENIED', 'Execution mode must be script or command.')
        if not supplied:
            return error_result('COMMAND_DENIED', 'argv must contain at least one command name.')
        command = supplied[0]
        if '/' in command or '\\' in command or command not in self.allowed_commands:
            return error_result(
                'COMMAND_DENIED',
                f'CLI command is not allowed in the sandbox: {command}',
                blocked_resource=command,
            )
        if command in {'bash', 'sh'} and any(item in {'-c', '--command'} for item in supplied[1:]):
            return error_result(
                'COMMAND_DENIED',
                'Inline shell programs are not allowed; place the command in a declared scripts/ file.',
                blocked_resource=command,
            )
        executable = shutil.which(command, path=FIXED_PATH)
        if executable is None:
            return error_result(
                'COMMAND_NOT_FOUND',
                f'CLI command is not installed in the sandbox image: {command}',
                dependency=command,
                available_commands=self.capabilities()['commands'],
            )
        return [executable, *supplied[1:]]

    @staticmethod
    def _unsafe_path_argument(value: str) -> bool:
        candidates = [value]
        if '=' in value:
            candidates.append(value.split('=', 1)[1])
        for candidate in candidates:
            normalized = candidate.strip().replace('\\', '/')
            if normalized.startswith('/') or normalized.lower().startswith('file:/'):
                return True
            if (
                normalized == '..'
                or normalized.startswith('../')
                or '/../' in normalized
                or normalized.endswith('/..')
            ):
                return True
        return False

    def _execution_env(
        self,
        raw_env: Any,
        policy: dict[str, Any],
        workspace: Path,
        uid: Optional[int],
        egress_token: Optional[str],
    ) -> dict[str, str] | dict[str, Any]:
        validation = self._validate_env(raw_env, policy)
        if validation is not None:
            return validation
        required_env = self._string_list(policy.get('required_env') or [], 'required_env')
        raw_env = raw_env or {}
        home = workspace / '.home'
        temp = workspace / '.tmp'
        home.mkdir(mode=0o700, exist_ok=True)
        temp.mkdir(mode=0o700, exist_ok=True)
        if uid is not None:
            os.chown(home, uid, uid)
            os.chown(temp, uid, uid)
        result = {
            'PATH': FIXED_PATH,
            'HOME': str(home),
            'TMPDIR': str(temp),
            'LANG': 'C.UTF-8',
            'LC_ALL': 'C.UTF-8',
            'PYTHONUNBUFFERED': '1',
        }
        if egress_token:
            proxy_url = self.egress_proxy_url.replace('://', f'://{egress_token}:@', 1)
            result.update({
                'HTTP_PROXY': proxy_url,
                'HTTPS_PROXY': proxy_url,
                'http_proxy': proxy_url,
                'https_proxy': proxy_url,
                'NO_PROXY': '',
                'no_proxy': '',
            })
        else:
            result.update({'NO_PROXY': '*', 'no_proxy': '*'})
        for name in required_env:
            result[name] = raw_env[name]
        return result

    def _validate_env(self, raw_env: Any, policy: dict[str, Any]) -> Optional[dict[str, Any]]:
        if raw_env is None:
            raw_env = {}
        if not isinstance(raw_env, dict):
            return error_result('COMMAND_DENIED', 'env must map variable names to string values.')
        required_env = self._string_list(policy.get('required_env') or [], 'required_env')
        forbidden = sorted({name for name in required_env if name in FORBIDDEN_ENV or not ENV_NAME.fullmatch(name)})
        if forbidden:
            return error_result(
                'COMMAND_DENIED',
                f'Environment variable is reserved by the sandbox: {forbidden[0]}',
                dependency=forbidden[0],
            )
        missing = [name for name in required_env if not isinstance(raw_env.get(name), str) or not raw_env[name]]
        if missing:
            return error_result(
                'MISSING_ENV',
                f'Skill requires an environment variable that is not configured: {missing[0]}',
                missing_env=missing,
            )
        for name in required_env:
            value = raw_env[name]
            if len(value.encode('utf-8')) > 65536:
                return error_result('COMMAND_DENIED', f'Environment variable is too large: {name}')
        return None

    @staticmethod
    def _limits(raw: Any) -> tuple[int, int, int, int, int] | dict[str, Any]:
        if not isinstance(raw, dict):
            return error_result('RESOURCE_LIMIT', 'Execution limits are required.')
        try:
            timeout = min(300, max(1, int(raw.get('timeout_seconds', 180))))
            memory_mb = min(4096, max(16, int(raw.get('memory_mb', 1024))))
            cpu_seconds = min(300, max(1, int(raw.get('cpu_seconds', 60))))
            pids = min(256, max(1, int(raw.get('pids', 64))))
            output_bytes = min(16 * 1024 * 1024, max(1024, int(raw.get('output_bytes', 1024 * 1024))))
        except (TypeError, ValueError):
            return error_result('RESOURCE_LIMIT', 'Execution limits must be positive integers.')
        return timeout, memory_mb, cpu_seconds, pids, output_bytes

    @staticmethod
    def _process_error_type(returncode: int, stderr_preview: str) -> str:
        resource_signals = {
            -getattr(signal, 'SIGKILL', 9),
            -getattr(signal, 'SIGXCPU', 24),
        }
        resource_markers = (
            'memoryerror', 'cannot allocate memory', 'out of memory',
            'std::bad_alloc', 'fork: retry: resource temporarily unavailable',
            'fork: resource temporarily unavailable', 'cannot fork',
            "can't fork", 'fork failed',
        )
        network_markers = (
            'could not resolve host', 'failed to connect', 'name or service not known',
            'network is unreachable', 'temporary failure in name resolution',
            'getaddrinfo failed', 'enetwork', 'eai_again',
        )
        if returncode in resource_signals or any(
            marker in stderr_preview for marker in resource_markers
        ):
            return 'RESOURCE_LIMIT'
        if any(marker in stderr_preview for marker in network_markers):
            return 'NETWORK_DENIED'
        return 'EXIT_NON_ZERO'

    @staticmethod
    def _request_policy(request: dict[str, Any]) -> Optional[dict[str, Any]]:
        raw = request.get('policy') or {}
        if not isinstance(raw, dict):
            return None
        policy = dict(raw)
        for name in ('required_bins', 'required_env'):
            if name in request:
                policy[name] = request[name]
        return policy

    @staticmethod
    def _relative_list(value: Any, label: str) -> list[str]:
        if not isinstance(value, list):
            raise ValueError(f'{label} must be a list of relative paths.')
        return [safe_relative_path(item, label) for item in value]

    @staticmethod
    def _output_patterns(value: Any) -> list[str]:
        if not isinstance(value, list):
            raise ValueError('declared_outputs must be a list of relative paths or globs.')
        return [safe_output_pattern(item) for item in value]

    def _collect_outputs(
        self, workspace: Path, patterns: list[str],
    ) -> list[dict[str, Any]] | dict[str, Any]:
        if not patterns:
            return []
        root = workspace.resolve(strict=True)
        matches: dict[str, Path] = {}
        for pattern in patterns:
            for path in workspace.glob(pattern):
                rel_path = path.relative_to(workspace).as_posix()
                if path.is_symlink() or not path.is_file():
                    return error_result(
                        'FILESYSTEM_DENIED',
                        f'Declared output is not a regular file: {rel_path}',
                        blocked_resource=rel_path,
                    )
                resolved = path.resolve(strict=True)
                if os.path.commonpath((root, resolved)) != str(root):
                    return error_result(
                        'FILESYSTEM_DENIED',
                        f'Declared output escapes the Skill workspace: {rel_path}',
                        blocked_resource=rel_path,
                    )
                matches[rel_path] = resolved
        total = 0
        result = []
        for rel_path, path in sorted(matches.items()):
            size = path.stat().st_size
            total += size
            if size > self.max_output_file_bytes or total > self.max_output_file_bytes:
                return error_result(
                    'OUTPUT_LIMIT',
                    f'Declared output files exceed the {self.max_output_file_bytes}-byte export limit.',
                    blocked_resource=rel_path,
                )
            content = path.read_bytes()
            result.append({
                'path': rel_path,
                'size': size,
                'sha256': hashlib.sha256(content).hexdigest(),
                'data': base64.b64encode(content).decode('ascii'),
            })
        return result

    def _run(
        self,
        argv: list[str],
        cwd: Path,
        env: dict[str, str],
        limits: tuple[int, int, int, int, int],
        uid: Optional[int],
        request_id: str,
        session_key: str,
    ) -> dict[str, Any]:
        timeout, memory_mb, cpu_seconds, pids, output_bytes = limits
        started = time.monotonic()
        stdout_file = tempfile.TemporaryFile(dir=cwd)
        stderr_file = tempfile.TemporaryFile(dir=cwd)
        process: Optional[subprocess.Popen] = None
        registered = False

        try:
            launcher_argv = [
                sys.executable,
                str(Path(__file__).with_name('launcher.py')),
                '--cpu-seconds',
                str(cpu_seconds),
                '--memory-mb',
                str(memory_mb),
                '--pids',
                str(pids),
                '--output-bytes',
                str(output_bytes),
                '--uid',
                str(uid if uid is not None else -1),
                '--',
                *argv,
            ]
            process = subprocess.Popen(
                launcher_argv,
                cwd=str(cwd),
                env=env,
                stdin=subprocess.DEVNULL,
                stdout=stdout_file,
                stderr=stderr_file,
                close_fds=True,
                start_new_session=True,
            )
            with self._state_lock:
                duplicate = request_id in self._active_processes
                releasing = session_key in self._releasing_sessions
                if not duplicate and not releasing:
                    self._active_processes[request_id] = (process, session_key)
                    registered = True
            if duplicate or releasing:
                self._kill_process_group(process.pid)
                process.wait()
                if releasing:
                    return error_result('CANCELLED', 'The sandbox session was released.')
                return error_result(
                    'COMMAND_DENIED',
                    f'An execution with request_id {request_id!r} is already active.',
                    blocked_resource=request_id,
                )
            try:
                returncode = process.wait(timeout=timeout)
            except subprocess.TimeoutExpired:
                self._kill_process_group(process.pid)
                process.wait()
                return self._completed_result(
                    'TIMEOUT', f'Execution timed out after {timeout} seconds.', process.returncode,
                    stdout_file, stderr_file, output_bytes, started,
                )
            self._kill_process_group(process.pid)
            with self._state_lock:
                cancelled = request_id in self._cancelled_requests
            if cancelled:
                return self._completed_result(
                    'CANCELLED', 'Execution was cancelled.', returncode,
                    stdout_file, stderr_file, output_bytes, started,
                )
            stdout_size = os.fstat(stdout_file.fileno()).st_size
            stderr_size = os.fstat(stderr_file.fileno()).st_size
            if (
                stdout_size >= output_bytes
                or stderr_size >= output_bytes
                or returncode == -getattr(signal, 'SIGXFSZ', 25)
            ):
                return self._completed_result(
                    'OUTPUT_LIMIT', f'Execution output exceeded {output_bytes} bytes.', returncode,
                    stdout_file, stderr_file, output_bytes, started,
                )
            if returncode != 0:
                stderr_file.seek(0)
                stderr_preview = stderr_file.read(min(output_bytes, 65536)).decode('utf-8', errors='replace').lower()
                if returncode == 125 and 'sandbox_launcher_error:' in stderr_preview:
                    return self._completed_result(
                        'SANDBOX_UNAVAILABLE', 'Sandbox launcher failed.', returncode,
                        stdout_file, stderr_file, output_bytes, started,
                    )
                error_type = self._process_error_type(returncode, stderr_preview)
                message = (
                    'Execution was terminated by a resource limit.'
                    if error_type == 'RESOURCE_LIMIT'
                    else 'Network access is unavailable in the Skill sandbox.'
                    if error_type == 'NETWORK_DENIED'
                    else f'Command exited with status {returncode}.'
                )
                return self._completed_result(
                    error_type, message, returncode, stdout_file, stderr_file, output_bytes, started,
                )
            return self._completed_result(
                None, '', returncode, stdout_file, stderr_file, output_bytes, started,
            )
        except FileNotFoundError as exc:
            return error_result('COMMAND_NOT_FOUND', str(exc))
        except PermissionError as exc:
            return error_result('FILESYSTEM_DENIED', str(exc))
        except OSError as exc:
            return error_result('SANDBOX_UNAVAILABLE', f'Failed to start sandbox process: {exc}')
        finally:
            if registered:
                with self._state_lock:
                    record = self._active_processes.get(request_id)
                    if record is not None and record[0] is process:
                        self._active_processes.pop(request_id, None)
                    self._cancelled_requests.discard(request_id)
            stdout_file.close()
            stderr_file.close()

    @staticmethod
    def _kill_process_group(pid: int) -> None:
        try:
            os.killpg(pid, signal.SIGKILL)
        except ProcessLookupError:
            pass

    @staticmethod
    def _completed_result(
        error_type: Optional[str],
        message: str,
        returncode: int,
        stdout_file: Any,
        stderr_file: Any,
        limit: int,
        started: float,
    ) -> dict[str, Any]:
        stdout_file.seek(0)
        stderr_file.seek(0)
        stdout = stdout_file.read(limit).decode('utf-8', errors='replace')
        stderr = stderr_file.read(limit).decode('utf-8', errors='replace')
        result = {
            'status': 'ok' if error_type is None else 'failed',
            'reason_code': error_type,
            'stdout': stdout,
            'stderr': stderr or message,
            'exit_code': returncode,
            'duration_ms': int((time.monotonic() - started) * 1000),
            'output_files': [],
        }
        if error_type is not None:
            result.update(error_type=error_type, error=message)
        return result

    def _prepare_workspace(
        self,
        session_key: str,
        bundle_sha: str,
        bundle_data: bytes,
        uid: Optional[int],
    ) -> tuple[Path, Path]:
        session_root = self.root / session_key
        package_root = session_root / bundle_sha
        skill_root = package_root / 'skill'
        workspace = package_root / 'workspace'
        if not skill_root.exists():
            staging = Path(tempfile.mkdtemp(prefix='.staging-', dir=self.root))
            try:
                extracted = staging / 'skill'
                extracted.mkdir()
                self._extract_bundle(bundle_data, extracted)
                copied = staging / 'workspace'
                shutil.copytree(extracted, copied)
                session_root.mkdir(mode=0o700, exist_ok=True)
                package_root.mkdir(mode=0o700, exist_ok=True)
                os.replace(extracted, skill_root)
                os.replace(copied, workspace)
                self._make_read_only(skill_root)
                self._make_workspace_writable(workspace, uid)
                os.chmod(session_root, 0o711)
                os.chmod(package_root, 0o711)
            finally:
                shutil.rmtree(staging, ignore_errors=True)
        return skill_root, workspace

    def _extract_bundle(self, data: bytes, target: Path) -> None:
        total_size = 0
        count = 0
        try:
            archive = tarfile.open(fileobj=io.BytesIO(data), mode='r:gz')
        except (tarfile.TarError, OSError) as exc:
            raise ValueError(f'Invalid Skill package archive: {exc}') from exc
        with archive:
            for member in archive.getmembers():
                if member.isdir():
                    continue
                if not member.isfile():
                    raise ValueError(f'Skill package contains a forbidden archive member: {member.name}')
                rel = safe_relative_path(member.name, 'archive member')
                count += 1
                total_size += member.size
                if count > self.max_files:
                    raise ValueError(f'Skill package contains more than {self.max_files} files.')
                if member.size > self.max_unpacked_bytes or total_size > self.max_unpacked_bytes:
                    raise ValueError('Skill package uncompressed content is too large.')
                destination = target.joinpath(*rel.split('/'))
                destination.parent.mkdir(parents=True, exist_ok=True)
                source = archive.extractfile(member)
                if source is None:
                    raise ValueError(f'Unable to read Skill package member: {rel}')
                with source, destination.open('xb') as output:
                    shutil.copyfileobj(source, output, length=1024 * 1024)

    @staticmethod
    def _make_read_only(root: Path) -> None:
        for path in root.rglob('*'):
            os.chmod(path, 0o555 if path.is_dir() else 0o444)
        os.chmod(root, 0o555)

    @staticmethod
    def _make_workspace_writable(root: Path, uid: Optional[int]) -> None:
        for path in root.rglob('*'):
            os.chmod(path, 0o700 if path.is_dir() else 0o600)
            if uid is not None:
                os.chown(path, uid, uid)
        os.chmod(root, 0o700)
        if uid is not None:
            os.chown(root, uid, uid)

    def _decode_bundle(self, raw: Any) -> tuple[bytes, str]:
        if not isinstance(raw, dict) or raw.get('format') != 'tar.gz+base64':
            raise ValueError('Skill bundle must use tar.gz+base64 format.')
        expected = str(raw.get('sha256') or '')
        try:
            data = base64.b64decode(str(raw.get('data') or ''), validate=True)
        except (binascii.Error, ValueError) as exc:
            raise ValueError('Skill bundle is not valid base64.') from exc
        if not data or len(data) > self.max_bundle_bytes:
            raise ValueError(f'Skill bundle exceeds the {self.max_bundle_bytes}-byte compressed limit.')
        actual = hashlib.sha256(data).hexdigest()
        if len(expected) != 64 or actual != expected:
            raise ValueError('Skill bundle digest does not match its payload.')
        return data, actual

    @staticmethod
    def _string_list(value: Any, label: str) -> list[str]:
        if not isinstance(value, list) or any(not isinstance(item, str) or not item for item in value):
            raise ValueError(f'{label} must be a list of non-empty strings.')
        return list(value)

    def _workspace_lock(self, key: str) -> threading.Lock:
        with self._state_lock:
            return self._workspace_locks.setdefault(key, threading.Lock())

    def _uid_for_session(self, session_key: str) -> Optional[int]:
        if self.sandbox_uid_base is None:
            return None
        with self._state_lock:
            existing = self._session_uids.get(session_key)
            if existing is not None:
                return existing
            for _ in range(self.sandbox_uid_count):
                uid = self.sandbox_uid_base + self._next_uid
                self._next_uid = (self._next_uid + 1) % self.sandbox_uid_count
                if uid not in self._used_uids:
                    self._used_uids.add(uid)
                    self._session_uids[session_key] = uid
                    return uid
        raise RuntimeError('Sandbox session UID pool is exhausted.')

    def _cleanup_stale(self, exclude: str) -> None:
        cutoff = time.time() - self.session_ttl_seconds
        excluded_session, _, excluded_bundle = exclude.partition('/')
        for session_root in list(self.root.iterdir()):
            if not session_root.is_dir() or session_root.name.startswith('.staging-'):
                continue
            for package_root in list(session_root.iterdir()):
                if session_root.name == excluded_session and package_root.name == excluded_bundle:
                    continue
                key = f'{session_root.name}/{package_root.name}'
                lock = self._workspace_lock(key)
                if not lock.acquire(blocking=False):
                    continue
                try:
                    if package_root.stat().st_mtime < cutoff:
                        self._remove_tree(package_root)
                        with self._state_lock:
                            self._workspace_locks.pop(key, None)
                except OSError:
                    pass
                finally:
                    lock.release()
            try:
                if not any(session_root.iterdir()):
                    session_root.rmdir()
                    with self._state_lock:
                        uid = self._session_uids.pop(session_root.name, None)
                        if uid is not None:
                            self._used_uids.discard(uid)
            except (FileNotFoundError, OSError):
                pass

    @staticmethod
    def _touch(path: Path) -> None:
        try:
            path.touch(exist_ok=True)
        except OSError:
            pass

    @staticmethod
    def _remove_tree(path: Path) -> None:
        if not path.exists():
            return
        try:
            for child in path.rglob('*'):
                try:
                    os.chmod(child, 0o700 if child.is_dir() else 0o600)
                except OSError:
                    pass
            os.chmod(path, 0o700)
            shutil.rmtree(path)
        except OSError:
            shutil.rmtree(path, ignore_errors=True)


class SandboxRequestHandler(socketserver.BaseRequestHandler):
    def handle(self) -> None:
        try:
            header = self._recv_exact(HEADER_BYTES)
            size = struct.unpack('!Q', header)[0]
            if size <= 0 or size > MAX_REQUEST_BYTES:
                response = error_result('SANDBOX_UNAVAILABLE', f'Invalid sandbox request size: {size}')
            else:
                request = json.loads(self._recv_exact(size).decode('utf-8'))
                if not isinstance(request, dict):
                    raise ValueError('request must be a JSON object')
                if request.get('action') == 'ping':
                    response = {'status': 'ok', 'version': PROTOCOL_VERSION}
                elif request.get('action') == 'capabilities':
                    response = self.server.engine.capabilities()
                elif request.get('action') == 'preflight':
                    response = self.server.engine.preflight(request)
                elif request.get('action') == 'execute':
                    response = self.server.engine.execute(request)
                elif request.get('action') == 'cancel':
                    response = self.server.engine.cancel(request.get('request_id'))
                elif request.get('action') == 'release_session':
                    response = self.server.engine.release_session(request.get('session_id'))
                else:
                    response = error_result('COMMAND_DENIED', 'Unknown sandbox action.')
        except (ConnectionError, OSError, ValueError, json.JSONDecodeError) as exc:
            response = error_result('SANDBOX_UNAVAILABLE', f'Invalid sandbox request: {exc}')
        payload = json.dumps(response, ensure_ascii=False, separators=(',', ':')).encode('utf-8')
        if len(payload) > MAX_RESPONSE_BYTES:
            payload = json.dumps(error_result('OUTPUT_LIMIT', 'Sandbox response exceeded its size limit.')).encode()
        self.request.sendall(struct.pack('!Q', len(payload)) + payload)

    def _recv_exact(self, size: int) -> bytes:
        chunks = bytearray()
        while len(chunks) < size:
            chunk = self.request.recv(size - len(chunks))
            if not chunk:
                raise ConnectionError('client closed the connection')
            chunks.extend(chunk)
        return bytes(chunks)


class ThreadingUnixServer(socketserver.ThreadingMixIn, socketserver.UnixStreamServer):
    daemon_threads = True
    allow_reuse_address = True

    def __init__(self, socket_path: str, engine: SandboxExecutionEngine):
        self.engine = engine
        super().__init__(socket_path, SandboxRequestHandler)


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser()
    parser.add_argument('--socket', default='/run/lazymind-sandbox/runner.sock')
    parser.add_argument('--root', default='/tmp/lazymind-sandbox/sessions')
    parser.add_argument('--max-bundle-bytes', type=int, default=32 * 1024 * 1024)
    parser.add_argument('--max-unpacked-bytes', type=int, default=64 * 1024 * 1024)
    parser.add_argument('--session-ttl-seconds', type=int, default=3600)
    parser.add_argument('--max-output-file-bytes', type=int, default=1024 * 1024)
    parser.add_argument('--egress-control-socket', default='')
    parser.add_argument('--egress-proxy-url', default='http://sandbox-egress:18081')
    return parser


def main() -> None:
    args = build_parser().parse_args()
    socket_path = Path(args.socket)
    socket_path.parent.mkdir(parents=True, exist_ok=True)
    if socket_path.exists() or socket_path.is_socket():
        socket_path.unlink()
    configured = os.environ.get('LAZYMIND_SANDBOX_ALLOWED_COMMANDS', '')
    allowed = {item.strip() for item in configured.split(',') if item.strip()} or set(DEFAULT_COMMANDS)
    engine = SandboxExecutionEngine(
        args.root,
        allowed_commands=allowed,
        max_bundle_bytes=args.max_bundle_bytes,
        max_unpacked_bytes=args.max_unpacked_bytes,
        session_ttl_seconds=args.session_ttl_seconds,
        egress_control_socket=args.egress_control_socket or None,
        egress_proxy_url=args.egress_proxy_url,
        max_output_file_bytes=args.max_output_file_bytes,
    )
    server = ThreadingUnixServer(str(socket_path), engine)
    os.chmod(socket_path, 0o600)
    try:
        server.serve_forever(poll_interval=0.2)
    finally:
        server.server_close()
        try:
            socket_path.unlink()
        except FileNotFoundError:
            pass


if __name__ == '__main__':
    main()
