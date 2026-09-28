"""Local Skill script execution with an explicit interpreter contract."""
from __future__ import annotations

import os
import shutil
import subprocess
import sys

import lazyllm
from lazyllm.tools.sandbox.dummy_sandbox import DummySandbox


class SkillScriptSandbox(DummySandbox):
    """Keep DummySandbox isolation while dispatching Skill scripts by extension."""

    def execute_script(self, source_dir, rel_path, args=None, cwd='.', env=None):
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
            }
        finally:
            self._cleanup_context(context)


def configure_skill_sandbox(skill_manager):
    """Use the interpreter-aware sandbox only for local Skill scripts.

    ReactAgent shares its ``sandbox`` argument with ToolManager. Passing this
    sandbox to the agent would therefore route ordinary tools through the
    DummySandbox and serialize structured MCP results. SkillManager already
    owns a separate sandbox instance, so replace only that local default and
    preserve explicitly configured remote providers.
    """
    if skill_manager is None:
        return None
    if lazyllm.config['sandbox_type'] == 'dummy':
        skill_manager._sandbox = SkillScriptSandbox()
    return skill_manager._sandbox
