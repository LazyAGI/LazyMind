from __future__ import annotations

import os
import shutil
import sys
from types import SimpleNamespace
from unittest.mock import MagicMock

import pytest

from lazymind.chat.engine.agent_runtime import skill_sandbox as mod


@pytest.mark.parametrize('extension,runtime', [
    ('.py', sys.executable), ('.sh', 'bash'), ('.bash', 'bash'),
    ('.js', 'node'), ('.cjs', 'node'), ('.mjs', 'node'), ('.JS', 'node'),
])
def test_interpreter_selection_preserves_arguments_cwd_and_env(tmp_path, monkeypatch, extension, runtime):
    (tmp_path / ('check' + extension)).write_text('content')
    (tmp_path / 'subdir').mkdir()
    calls = []
    monkeypatch.setattr(mod.shutil, 'which', lambda name, **kw: name)

    def run(argv, **kwargs):
        calls.append((argv, kwargs))
        assert os.path.isfile(argv[1])
        return type('Result', (), {'returncode': 0, 'stdout': 'ok', 'stderr': ''})()

    monkeypatch.setattr(mod.subprocess, 'run', run)
    result = mod.SkillScriptSandbox().execute_script(
        str(tmp_path), 'check' + extension, args=['a b', ';echo nope'], cwd='subdir', env={'EXAMPLE': 'value'},
    )
    argv, kwargs = calls[0]
    assert argv[0] == runtime
    assert argv[2:] == ['a b', ';echo nope']
    assert os.path.basename(kwargs['cwd']) == 'subdir'
    assert kwargs['env']['EXAMPLE'] == 'value'
    assert result['status'] == 'ok'
    assert not os.path.exists(argv[1])


def test_missing_node_never_falls_back_to_shell(tmp_path, monkeypatch):
    (tmp_path / 'check.js').write_text('console.log("ok")')
    monkeypatch.setattr(mod.shutil, 'which', lambda *a, **kw: None)
    monkeypatch.setattr(mod.subprocess, 'run', lambda *a, **kw: pytest.fail('must not execute'))
    result = mod.SkillScriptSandbox().execute_script(str(tmp_path), 'check.js')
    assert result['error_type'] == 'missing_dependency'
    assert result['dependency'] == 'node'
    assert result['exit_code'] is None


@pytest.mark.parametrize('filename', ['unknown.txt', 'no-extension'])
def test_unknown_extension_is_rejected(tmp_path, monkeypatch, filename):
    (tmp_path / filename).write_text('echo must-not-run')
    monkeypatch.setattr(mod.subprocess, 'run', lambda *a, **kw: pytest.fail('must not execute'))
    assert mod.SkillScriptSandbox().execute_script(str(tmp_path), filename)['error_type'] == 'unsupported_runtime'


@pytest.mark.parametrize('extension,code', [
    ('.py', 'print("runtime-ok")'), ('.sh', 'echo runtime-ok'),
    ('.js', 'console.log("runtime-ok")'), ('.cjs', 'console.log("runtime-ok")'),
    ('.mjs', 'import process from "node:process"; console.log("runtime-ok")'),
])
def test_real_interpreters(tmp_path, extension, code):
    if extension in ('.js', '.cjs', '.mjs') and not shutil.which('node'):
        pytest.skip('Node.js is not installed')
    (tmp_path / ('check' + extension)).write_text(code)
    result = mod.SkillScriptSandbox().execute_script(str(tmp_path), 'check' + extension)
    assert result['exit_code'] == 0, result
    assert result['stdout'].strip() == 'runtime-ok'


def test_path_escape_is_rejected_and_sandbox_cleaned(tmp_path, monkeypatch):
    root = tmp_path / 'sandbox'
    root.mkdir()
    source = tmp_path / 'source'
    source.mkdir()
    sandbox = mod.SkillScriptSandbox()
    monkeypatch.setattr(sandbox, '_create_context', lambda: {'temp_dir': str(root)})
    with pytest.raises(ValueError, match='inside the sandbox'):
        sandbox.execute_script(str(source), '../escape.py')
    assert not root.exists()


def test_non_default_provider_is_preserved():
    sentinel = object()
    manager = SimpleNamespace(_sandbox=sentinel)
    with mod.lazyllm.config.temp('sandbox_type', 'remote'):
        assert mod.configure_skill_sandbox(manager) is sentinel
    assert manager._sandbox is sentinel


def test_executor_wires_the_interpreter_only_to_skill_manager(monkeypatch):
    from lazymind.chat.engine.agent_runtime import (
        AgentExecutionOptions, AgentExecutor, AgentRole, AgentRunPlan, PromptBuilder,
    )
    from lazymind.chat.engine.agent_runtime import executor

    manager = SimpleNamespace(_sandbox=object())
    agent = MagicMock()
    agent._skill_manager = manager
    constructor = MagicMock(return_value=agent)
    monkeypatch.setattr(executor._agent_mod, 'ReactAgent', constructor)
    plan = AgentRunPlan(
        role=AgentRole.CHAT,
        prompt=PromptBuilder.for_role(AgentRole.CHAT).input('hello', source='user').build(),
        tools=[], stop_tools=[], execution_options=AgentExecutionOptions(),
    )
    AgentExecutor().create_agent('llm', plan)
    assert 'sandbox' not in constructor.call_args.kwargs
    assert isinstance(manager._sandbox, mod.SkillScriptSandbox)


def test_skill_manager_materializes_and_runs_real_javascript(tmp_path):
    from lazyllm.tools.agent.skill_manager import SkillManager

    if not shutil.which('node'):
        pytest.skip('Node.js is not installed')
    (tmp_path / 'scripts').mkdir()
    (tmp_path / 'scripts' / 'check.js').write_text('console.log(process.argv[2])')

    class SkillFS:
        def materialize_dir(self, path, local_dir):
            shutil.copytree(tmp_path, local_dir, dirs_exist_ok=True)
            return {'materialized': True, 'files': ['scripts/check.js']}

    manager = SkillManager(dir='', fs=SkillFS(), sandbox=mod.SkillScriptSandbox())
    manager._skills_index = {'sample': {
        'name': 'sample', 'path': 'remote://sample', 'skill_md': 'remote://sample/SKILL.md',
    }}
    result = manager.run_script('sample', 'scripts/check.js', args=['real-node-result'])
    assert result['status'] == 'ok'
    assert result['stdout'].strip() == 'real-node-result'
