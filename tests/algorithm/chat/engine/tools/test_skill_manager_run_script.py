from __future__ import annotations

import os

import pytest
from types import SimpleNamespace
from lazyllm.tools.agent import ToolExecutionError
from lazyllm.tools.agent.skill_manager import SkillManager


class MaterializingFS:
    def __init__(self):
        self.materialized = []

    def materialize_dir(self, path: str, local_dir: str):
        self.materialized.append((path, local_dir))
        script_dir = os.path.join(local_dir, 'scripts')
        os.makedirs(script_dir, exist_ok=True)
        with open(os.path.join(script_dir, 'check.py'), 'w', encoding='utf-8') as fh:
            fh.write('print("ok")\n')
        return {
            'source_path': path,
            'local_dir': local_dir,
            'materialized': True,
            'files': ['scripts/check.py'],
        }


def test_run_script_uses_fs_materialize_dir_without_source_specific_branch(monkeypatch):
    fs = MaterializingFS()
    calls = []

    def execute_script(**kwargs):
        calls.append(kwargs)
        return {'status': 'ok', 'stdout': 'ok\n', 'stderr': '', 'exit_code': 0}

    manager = SkillManager(dir='', fs=fs, sandbox=SimpleNamespace(execute_script=execute_script))
    manager._skills_index = {
        'pkg': {
            'name': 'pkg',
            'path': 'remote://skills/coding/pkg',
            'skill_md': 'remote://skills/coding/pkg/SKILL.md',
        }
    }

    result = manager.run_script('pkg', 'scripts/check.py', args=['--fast'])

    assert result['status'] == 'ok'
    assert fs.materialized[0][0] == 'remote://skills/coding/pkg'
    assert calls[0]['rel_path'] == 'scripts/check.py'
    assert calls[0]['args'] == ['--fast']
    assert calls[0]['source_dir'] == fs.materialized[0][1]
    assert calls[0]['cwd'] == '.'


def test_run_script_reports_missing_materialized_script(monkeypatch):
    fs = MaterializingFS()
    manager = SkillManager(dir='', fs=fs)
    manager._skills_index = {
        'pkg': {
            'name': 'pkg',
            'path': 'remote://skills/coding/pkg',
            'skill_md': 'remote://skills/coding/pkg/SKILL.md',
        }
    }

    with pytest.raises(ToolExecutionError, match='scripts/missing.py.*was not found'):
        manager.run_script('pkg', 'scripts/missing.py')

    assert fs.materialized[0][0] == 'remote://skills/coding/pkg'
