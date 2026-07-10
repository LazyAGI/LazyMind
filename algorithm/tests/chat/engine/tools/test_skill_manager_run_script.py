from __future__ import annotations

import os

from lazyllm.tools.agent import skill_manager as skill_manager_mod
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

    def fake_shell_tool(cmd, cwd=None, allow_unsafe=False):
        calls.append({'cmd': cmd, 'cwd': cwd, 'allow_unsafe': allow_unsafe})
        return {'status': 'ok', 'stdout': 'ok\n', 'stderr': '', 'exit_code': 0, 'cwd': cwd}

    monkeypatch.setattr(skill_manager_mod, '_shell_tool', fake_shell_tool)

    manager = SkillManager(dir='', fs=fs)
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
    assert calls[0]['cmd'].endswith('scripts/check.py --fast')
    assert calls[0]['cwd'] == fs.materialized[0][1]


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

    result = manager.run_script('pkg', 'references/check.py')

    assert result['status'] == 'missing'
    assert result['path'].endswith('/references/check.py')
    assert fs.materialized[0][0] == 'remote://skills/coding/pkg'
