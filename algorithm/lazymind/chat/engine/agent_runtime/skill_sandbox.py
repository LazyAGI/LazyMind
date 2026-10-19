"""Skill script and CLI execution backends."""
from __future__ import annotations

import os
import shutil
import subprocess
import sys
from typing import Any

import lazyllm
from lazyllm.tools.sandbox.dummy_sandbox import DummySandbox
from lazymind.config import config
from lazymind.sandbox_runner.client import SandboxRunnerClient
from lazymind.sandbox_runner.protocol import error_result


_LOCAL_COMMANDS = frozenset({
    'awk', 'bash', 'cat', 'cp', 'curl', 'cut', 'date', 'diff', 'dirname',
    'echo', 'env', 'false', 'ffmpeg', 'find', 'git', 'grep', 'head', 'jq',
    'ls', 'mkdir', 'mv', 'node', 'npm', 'npx', 'pandoc', 'printf', 'python',
    'python3', 'rg', 'sed', 'sh', 'sort', 'tail', 'tar', 'test', 'touch',
    'tr', 'true', 'uname', 'uniq', 'unzip', 'wc', 'xargs', 'zip',
})


class SkillScriptSandbox(DummySandbox):
    """Explicitly unsafe local-development backend with interpreter dispatch."""

    def capabilities(self) -> dict[str, Any]:
        return {
            'status': 'ok',
            'runtimes': {
                'python': True,
                'bash': shutil.which('bash') is not None,
                'node': shutil.which('node') is not None,
            },
            'commands': sorted(command for command in _LOCAL_COMMANDS if shutil.which(command)),
            'profiles': ['development'],
            'network_modes': ['host'],
            'supports': {'script': True, 'command': True, 'declared_outputs': False},
            'isolation': 'none',
        }

    def execute_script(
        self, source_dir, rel_path, args=None, cwd='.', env=None, policy=None, session_id=None,
    ):
        del policy, session_id
        context = self._create_context()
        try:
            root = context['temp_dir']
            shutil.copytree(source_dir, root, dirs_exist_ok=True)
            script = self._resolve_child(root, rel_path, 'rel_path')
            run_cwd = self._resolve_child(root, cwd or '.', 'cwd')
            if not os.path.isfile(script):
                return {'status': 'missing', 'path': script, 'rel_path': rel_path, 'cwd': run_cwd}
            if not os.path.isdir(run_cwd):
                raise FileNotFoundError(f'cwd not found: {run_cwd}')
            extension = os.path.splitext(script)[1].lower()
            runtime = {
                '.py': sys.executable,
                '.sh': 'bash',
                '.bash': 'bash',
                '.js': 'node',
                '.cjs': 'node',
                '.mjs': 'node',
            }.get(extension)
            if runtime is None:
                return {
                    'status': 'failed', 'error_type': 'unsupported_runtime',
                    'extension': extension, 'stdout': '', 'stderr': 'Unsupported Skill script extension.',
                    'exit_code': None, 'cwd': run_cwd,
                }
            run_env = {**os.environ, **(env or {})}
            executable = shutil.which(runtime, path=run_env.get('PATH', os.defpath))
            if executable is None:
                return {
                    'status': 'failed', 'error_type': 'missing_dependency',
                    'dependency': runtime, 'stdout': '', 'stderr': f'Required runtime not found: {runtime}',
                    'exit_code': None, 'cwd': run_cwd,
                }
            completed = subprocess.run(
                [executable, script, *(args or [])], cwd=run_cwd, env=run_env,
                text=True, capture_output=True, timeout=self._timeout,
            )
            return {
                'status': 'ok' if completed.returncode == 0 else 'failed',
                'stdout': completed.stdout, 'stderr': completed.stderr,
                'exit_code': completed.returncode, 'cwd': run_cwd,
                **({'error_type': 'EXIT_NON_ZERO'} if completed.returncode != 0 else {}),
            }
        except subprocess.TimeoutExpired:
            return error_result('TIMEOUT', f'Execution timed out after {self._timeout} seconds.')
        finally:
            self._cleanup_context(context)

    def execute_command(
        self, source_dir, argv, cwd='.', env=None, policy=None, session_id=None,
    ) -> dict[str, Any]:
        del policy, session_id
        if not isinstance(argv, list) or not argv or any(not isinstance(item, str) for item in argv):
            return error_result('COMMAND_DENIED', 'argv must be a non-empty list of strings.')
        command = argv[0]
        if '/' in command or '\\' in command or command not in _LOCAL_COMMANDS:
            return error_result('COMMAND_DENIED', f'CLI command is not allowed in the local backend: {command}')
        if command in {'bash', 'sh'} and any(item in {'-c', '--command'} for item in argv[1:]):
            return error_result(
                'COMMAND_DENIED',
                'Inline shell programs are not allowed; place the command in a declared scripts/ file.',
            )
        context = self._create_context()
        try:
            root = context['temp_dir']
            shutil.copytree(source_dir, root, dirs_exist_ok=True)
            run_cwd = self._resolve_child(root, cwd or '.', 'cwd')
            if not os.path.isdir(run_cwd):
                return error_result('FILESYSTEM_DENIED', f'Working directory not found: {cwd}')
            run_env = {**os.environ, **(env or {})}
            executable = shutil.which(command, path=run_env.get('PATH', os.defpath))
            if executable is None:
                return error_result(
                    'COMMAND_NOT_FOUND', f'CLI command is not installed: {command}', dependency=command,
                )
            completed = subprocess.run(
                [executable, *argv[1:]], cwd=run_cwd, env=run_env,
                text=True, capture_output=True, timeout=self._timeout,
            )
            return {
                'status': 'ok' if completed.returncode == 0 else 'failed',
                'stdout': completed.stdout,
                'stderr': completed.stderr,
                'exit_code': completed.returncode,
                'cwd': run_cwd,
                **({'error_type': 'EXIT_NON_ZERO'} if completed.returncode != 0 else {}),
            }
        except subprocess.TimeoutExpired:
            return error_result('TIMEOUT', f'Execution timed out after {self._timeout} seconds.')
        finally:
            self._cleanup_context(context)


def _runner_sandbox() -> SandboxRunnerClient:
    return SandboxRunnerClient(
        config['skill_sandbox_socket'],
        timeout=config['skill_sandbox_timeout'],
        memory_mb=config['skill_sandbox_memory_mb'],
        cpu_seconds=config['skill_sandbox_cpu_seconds'],
        pids=config['skill_sandbox_pids'],
        output_bytes=config['skill_sandbox_output_bytes'],
        bundle_bytes=config['skill_sandbox_bundle_bytes'],
    )


def configure_skill_sandbox(skill_manager):
    """Install the configured Skill-only execution backend.

    ReactAgent shares its ``sandbox`` argument with ToolManager. Passing this
    sandbox to the agent would therefore route ordinary tools through the
    DummySandbox and serialize structured MCP results. SkillManager already
    owns a separate sandbox instance, so replace only that local default and
    preserve explicitly configured remote providers.
    """
    if skill_manager is None:
        return None
    if lazyllm.config['sandbox_type'] == 'dummy':
        backend = str(config['skill_sandbox_backend'] or '').strip().lower()
        if backend == 'runner':
            skill_manager._sandbox = _runner_sandbox()
        elif backend == 'local':
            lazyllm.LOG.warning(
                'LAZYMIND_SKILL_SANDBOX_BACKEND=local executes Skill code on the host and is for development only.'
            )
            skill_manager._sandbox = SkillScriptSandbox(timeout=config['skill_sandbox_timeout'])
        else:
            raise ValueError(
                f'Unsupported Skill sandbox backend {backend!r}; expected runner or local.'
            )
    return skill_manager._sandbox
