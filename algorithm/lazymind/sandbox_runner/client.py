from __future__ import annotations

import base64
import gzip
import hashlib
import io
import json
import os
import socket
import struct
import tarfile
import uuid
from pathlib import Path
from typing import Any, Optional

from .protocol import (
    HEADER_BYTES,
    MAX_RESPONSE_BYTES,
    PROTOCOL_VERSION,
    error_result,
    safe_relative_path,
    safe_output_pattern,
)


class SandboxRunnerClient:
    def __init__(
        self,
        socket_path: str,
        *,
        timeout: int = 180,
        memory_mb: int = 1024,
        cpu_seconds: int = 60,
        pids: int = 64,
        output_bytes: int = 1024 * 1024,
        bundle_bytes: int = 32 * 1024 * 1024,
    ):
        self.socket_path = socket_path
        self.timeout = max(1, int(timeout))
        self.memory_mb = max(16, int(memory_mb))
        self.cpu_seconds = max(1, int(cpu_seconds))
        self.pids = max(1, int(pids))
        self.output_bytes = max(1024, int(output_bytes))
        self.bundle_bytes = max(1024, int(bundle_bytes))

    def execute_script(
        self,
        source_dir: str,
        rel_path: str,
        args: Optional[list[str]] = None,
        cwd: str = '.',
        env: Optional[dict[str, str]] = None,
        policy: Optional[dict[str, Any]] = None,
        session_id: Optional[str] = None,
        input_files: Optional[list[str]] = None,
        declared_outputs: Optional[list[str]] = None,
        skill_id: Optional[str] = None,
        revision: Optional[str] = None,
        request_id: Optional[str] = None,
    ) -> dict[str, Any]:
        try:
            entrypoint = safe_relative_path(rel_path, 'rel_path')
            run_cwd = safe_relative_path(cwd, 'cwd', allow_dot=True)
            bundle = self._bundle(source_dir)
        except (OSError, ValueError) as exc:
            return error_result('FILESYSTEM_DENIED', str(exc))
        try:
            argv = self._string_list(args or [], 'args')
            run_env = self._string_mapping(env or {}, 'env')
            inputs = self._relative_paths(input_files or [], 'input_files')
            outputs = self._output_patterns(declared_outputs or [])
        except ValueError as exc:
            return error_result('COMMAND_DENIED', str(exc))
        return self._execute({
            'runtime': self._runtime_for_entrypoint(entrypoint),
            'mode': 'script',
            'entrypoint': entrypoint,
            'argv': argv,
            'args': argv,
            'cwd': run_cwd,
            'bundle': bundle,
            'tree_hash': bundle['sha256'],
            'input_files': inputs,
            'declared_outputs': outputs,
            'env': run_env,
            'policy': dict(policy or {}),
            'session_id': str(session_id or uuid.uuid4()),
            'skill_id': str(skill_id or (policy or {}).get('skill_key') or ''),
            'revision': str(revision or 'latest'),
        }, request_id=request_id)

    def execute_command(
        self,
        source_dir: str,
        argv: list[str],
        cwd: str = '.',
        env: Optional[dict[str, str]] = None,
        policy: Optional[dict[str, Any]] = None,
        session_id: Optional[str] = None,
        input_files: Optional[list[str]] = None,
        declared_outputs: Optional[list[str]] = None,
        skill_id: Optional[str] = None,
        revision: Optional[str] = None,
        request_id: Optional[str] = None,
    ) -> dict[str, Any]:
        try:
            command = self._string_list(argv, 'argv')
            if not command:
                raise ValueError('argv must contain at least one command name.')
            run_env = self._string_mapping(env or {}, 'env')
            inputs = self._relative_paths(input_files or [], 'input_files')
            outputs = self._output_patterns(declared_outputs or [])
        except ValueError as exc:
            return error_result('COMMAND_DENIED', str(exc))
        try:
            run_cwd = safe_relative_path(cwd, 'cwd', allow_dot=True)
            bundle = self._bundle(source_dir)
        except (OSError, ValueError) as exc:
            return error_result('FILESYSTEM_DENIED', str(exc))
        return self._execute({
            'runtime': 'cli',
            'mode': 'command',
            'argv': command,
            'cwd': run_cwd,
            'bundle': bundle,
            'tree_hash': bundle['sha256'],
            'input_files': inputs,
            'declared_outputs': outputs,
            'env': run_env,
            'policy': dict(policy or {}),
            'session_id': str(session_id or uuid.uuid4()),
            'skill_id': str(skill_id or (policy or {}).get('skill_key') or ''),
            'revision': str(revision or 'latest'),
        }, request_id=request_id)

    def ping(self) -> dict[str, Any]:
        return self._rpc({'version': PROTOCOL_VERSION, 'action': 'ping'})

    def capabilities(self) -> dict[str, Any]:
        return self._rpc({'version': PROTOCOL_VERSION, 'action': 'capabilities'})

    def cancel(self, request_id: str) -> dict[str, Any]:
        return self._rpc({
            'version': PROTOCOL_VERSION,
            'action': 'cancel',
            'request_id': str(request_id or ''),
        })

    def release_session(self, session_id: str) -> dict[str, Any]:
        return self._rpc({
            'version': PROTOCOL_VERSION,
            'action': 'release_session',
            'session_id': str(session_id or ''),
        })

    def _execute(self, request: dict[str, Any], *, request_id: Optional[str] = None) -> dict[str, Any]:
        policy = request.get('policy') or {}
        allowed_domains = policy.get('allowed_domains') or []
        network_policy = {'mode': 'domain_allowlist' if allowed_domains else 'deny'}
        request.update({
            'version': PROTOCOL_VERSION,
            'action': 'execute',
            'request_id': str(request_id or uuid.uuid4()),
            'limits': {
                'timeout_seconds': self.timeout,
                'memory_mb': self.memory_mb,
                'cpu_seconds': self.cpu_seconds,
                'pids': self.pids,
                'output_bytes': self.output_bytes,
            },
            'required_bins': list(policy.get('required_bins') or []),
            'required_env': list(policy.get('required_env') or []),
            'network': network_policy,
            'network_policy': network_policy,
        })
        return self._rpc(request)

    @staticmethod
    def _runtime_for_entrypoint(entrypoint: str) -> str:
        suffix = Path(entrypoint).suffix.lower()
        return {
            '.py': 'python',
            '.sh': 'bash',
            '.bash': 'bash',
            '.js': 'node',
            '.cjs': 'node',
            '.mjs': 'node',
        }.get(suffix, 'unsupported')

    @staticmethod
    def _relative_paths(values: list[Any], label: str) -> list[str]:
        if not isinstance(values, list):
            raise ValueError(f'{label} must be a list of relative paths.')
        return [safe_relative_path(value, label) for value in values]

    @staticmethod
    def _output_patterns(values: list[Any]) -> list[str]:
        if not isinstance(values, list):
            raise ValueError('declared_outputs must be a list of relative paths or globs.')
        return [safe_output_pattern(value) for value in values]

    def _rpc(self, request: dict[str, Any]) -> dict[str, Any]:
        try:
            payload = json.dumps(request, ensure_ascii=False, separators=(',', ':')).encode('utf-8')
            with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as connection:
                connection.settimeout(self.timeout + 5)
                connection.connect(self.socket_path)
                connection.sendall(struct.pack('!Q', len(payload)) + payload)
                size = struct.unpack('!Q', self._recv_exact(connection, HEADER_BYTES))[0]
                if size <= 0 or size > MAX_RESPONSE_BYTES:
                    raise ValueError(f'invalid sandbox response size: {size}')
                response = json.loads(self._recv_exact(connection, size).decode('utf-8'))
            if not isinstance(response, dict):
                raise ValueError('sandbox response must be a JSON object')
            return response
        except (ConnectionError, OSError, TimeoutError, ValueError, json.JSONDecodeError) as exc:
            return error_result(
                'SANDBOX_UNAVAILABLE',
                f'Skill sandbox runner is unavailable at {self.socket_path}: {exc}',
            )

    @staticmethod
    def _recv_exact(connection: socket.socket, size: int) -> bytes:
        chunks = bytearray()
        while len(chunks) < size:
            chunk = connection.recv(size - len(chunks))
            if not chunk:
                raise ConnectionError('sandbox runner closed the connection')
            chunks.extend(chunk)
        return bytes(chunks)

    def _bundle(self, source_dir: str) -> dict[str, str]:
        root = Path(source_dir).resolve(strict=True)
        if not root.is_dir():
            raise ValueError(f'Skill source directory not found: {source_dir}')
        file_count = 0
        total_size = 0
        output = io.BytesIO()
        with gzip.GzipFile(fileobj=output, mode='wb', mtime=0) as compressed:
            with tarfile.open(fileobj=compressed, mode='w', format=tarfile.PAX_FORMAT) as archive:
                for path in sorted(root.rglob('*')):
                    if path.is_symlink():
                        raise ValueError(f'Skill packages may not contain symbolic links: {path.relative_to(root)}')
                    if path.is_dir():
                        continue
                    if not path.is_file():
                        raise ValueError(f'Skill package contains a non-regular file: {path.relative_to(root)}')
                    file_count += 1
                    if file_count > 2048:
                        raise ValueError('Skill package contains more than 2048 files.')
                    size = path.stat().st_size
                    if size > self.bundle_bytes:
                        raise ValueError(f'Skill package file is too large: {path.relative_to(root)}')
                    total_size += size
                    if total_size > self.bundle_bytes * 2:
                        raise ValueError('Skill package uncompressed content is too large.')
                    archive.add(
                        path,
                        arcname=path.relative_to(root).as_posix(),
                        recursive=False,
                        filter=self._normalize_tar_info,
                    )
        data = output.getvalue()
        if len(data) > self.bundle_bytes:
            raise ValueError(
                f'Skill package is {len(data)} bytes and exceeds the {self.bundle_bytes}-byte limit.'
            )
        return {
            'format': 'tar.gz+base64',
            'sha256': hashlib.sha256(data).hexdigest(),
            'data': base64.b64encode(data).decode('ascii'),
        }

    @staticmethod
    def _normalize_tar_info(info: tarfile.TarInfo) -> tarfile.TarInfo:
        info.uid = 0
        info.gid = 0
        info.uname = ''
        info.gname = ''
        info.mtime = 0
        info.mode = 0o644
        info.pax_headers = {}
        return info

    @staticmethod
    def _string_list(value: list[Any], label: str) -> list[str]:
        if not isinstance(value, list) or any(not isinstance(item, str) for item in value):
            raise ValueError(f'{label} must be a list of strings.')
        return list(value)

    @staticmethod
    def _string_mapping(value: dict[Any, Any], label: str) -> dict[str, str]:
        if not isinstance(value, dict) or any(
            not isinstance(key, str) or not isinstance(item, str) for key, item in value.items()
        ):
            raise ValueError(f'{label} must map strings to strings.')
        return dict(value)


def main() -> int:
    socket_path = os.environ.get('LAZYMIND_SKILL_SANDBOX_SOCKET', '/run/lazymind-sandbox/runner.sock')
    result = SandboxRunnerClient(socket_path, timeout=2).ping()
    return 0 if result.get('status') == 'ok' else 1


if __name__ == '__main__':
    raise SystemExit(main())
