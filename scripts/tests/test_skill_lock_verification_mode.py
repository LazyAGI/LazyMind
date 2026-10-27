import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import textwrap
import unittest


SCRIPT = Path(__file__).resolve().parents[1] / 'skill_lock_verification_mode.py'
WORKFLOW = SCRIPT.parents[1] / '.github/workflows/ci.yml'
SOURCES = """schema_version: 1
patch_catalog: patches/catalog.yaml
bundled_skills:
  - path: search/demo
    version: 1.0.0
skills:
  - source_url: https://example.test/remote.zip
    category: search
"""


class SkillLockVerificationModeTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        (self.root / 'backend/core').mkdir(parents=True)
        self.git('init', '-q')
        self.git('config', 'user.name', 'CI Test')
        self.git('config', 'user.email', 'ci@example.test')
        self.lock = {
            'schema_version': 1,
            'skills': [
                {'source_url': 'builtin://search/demo', 'tree_sha256': 'old'},
                {'source_url': 'https://example.test/remote.zip', 'tree_sha256': 'remote'},
            ],
        }
        self.write('skills/builtin-sources.yaml', SOURCES)
        self.write_lock()
        self.commit()
        self.base = self.git('rev-parse', 'HEAD').strip()

    def git(self, *args):
        return subprocess.check_output(['git', *args], cwd=self.root, text=True)

    def write(self, path, content):
        target = self.root / path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(content)

    def write_lock(self):
        self.write('skills/builtin-skills.lock.json', json.dumps(self.lock))

    def commit(self):
        self.git('add', '.')
        self.git('commit', '-qm', 'fixture')

    def mode(self):
        self.commit()
        return subprocess.check_output(
            ['python3', str(SCRIPT), self.base], cwd=self.root / 'backend/core', text=True,
        ).strip()

    def workflow_args(self, event='pull_request'):
        # Execute the production step; replace only event values and the
        # bundler executable so these tests never download remote packages.
        workflow = WORKFLOW.read_text(encoding='utf-8')
        start = workflow.index('          verify_args=(--verify-lock-artifacts)')
        lines = []
        for line in workflow[start:].splitlines():
            if line and not line.startswith('          '):
                break
            lines.append(line)
        script = textwrap.dedent('\n'.join(lines))
        script = script.replace('${{ github.event_name }}', event)
        script = script.replace('${{ github.event.pull_request.base.sha }}', self.base)
        script = script.replace('/tmp/base-builtin-skills.lock.json', str(self.root / 'base-lock.json'))
        (self.root / 'scripts').mkdir(exist_ok=True)
        shutil.copyfile(SCRIPT, self.root / 'scripts' / SCRIPT.name)
        capture = self.root / 'bundler-args.json'
        self.write('bin/go', '''#!/usr/bin/env python3
import json
import os
from pathlib import Path
import sys
Path(os.environ['BUNDLER_CAPTURE']).write_text(json.dumps(sys.argv[1:]))
''')
        (self.root / 'bin/go').chmod(0o755)
        env = {**os.environ, 'PATH': str(self.root / 'bin') + os.pathsep + os.environ['PATH'],
               'BUNDLER_CAPTURE': str(capture)}
        subprocess.run(['bash', '-e', '-c', script], cwd=self.root / 'backend/core',
                       env=env, check=True, capture_output=True, text=True)
        return json.loads(capture.read_text(encoding='utf-8'))

    def merge_after_target_skill_update(self, pr_changes_patch=False):
        self.git('checkout', '-qb', 'pr')
        if pr_changes_patch:
            self.write('skills/patches/catalog.yaml', 'PR patch change')
        else:
            self.write('frontend/change.ts', 'PR frontend change')
        self.commit()
        self.git('checkout', '-qb', 'target', self.base)
        self.lock['skills'][1]['tree_sha256'] = 'updated-on-target'
        self.write_lock()
        self.commit()
        self.git('merge', '--no-ff', '-qm', 'PR merge fixture', 'pr')

    def test_workflow_does_not_attribute_target_skill_changes_to_an_unrelated_pr(self):
        self.merge_after_target_skill_update()
        args = self.workflow_args()
        self.assertIn('--check-featured', args)
        self.assertNotIn('--verify-lock-artifacts', args)

    def test_workflow_strictly_verifies_pr_patches_against_the_actual_target_lock(self):
        self.merge_after_target_skill_update(pr_changes_patch=True)
        args = self.workflow_args()
        self.assertIn('--verify-lock-artifacts', args)
        self.assertIn('--verify-lock-base', args)
        base_lock = Path(args[args.index('--verify-lock-base') + 1])
        self.assertEqual(json.loads(base_lock.read_text(encoding='utf-8')), self.lock)

    def test_workflow_uses_frozen_catalog_verification_for_local_skill_changes(self):
        self.git('checkout', '-qb', 'pr')
        self.lock['skills'][0]['tree_sha256'] = 'updated-local'
        self.write_lock()
        self.commit()
        self.git('checkout', '-qb', 'target', self.base)
        self.git('merge', '--no-ff', '-qm', 'PR merge fixture', 'pr')
        args = self.workflow_args()
        self.assertIn('--catalog-only', args)
        self.assertIn('--frozen-lockfile', args)
        self.assertNotIn('--verify-lock-artifacts', args)

    def test_manual_workflow_keeps_full_artifact_verification(self):
        args = self.workflow_args(event='workflow_dispatch')
        self.assertIn('--verify-lock-artifacts', args)
        self.assertNotIn('--check-featured', args)
        self.assertNotIn('--verify-lock-base', args)

    def test_local_skill_and_version_change_use_offline_hash_verification(self):
        self.lock['skills'][0]['tree_sha256'] = 'new'
        self.write_lock()
        self.write('skills/builtin-sources.yaml', SOURCES.replace('1.0.0', '1.0.1'))
        self.write('skills/search/demo/SKILL.md', 'updated local skill')
        self.assertEqual(self.mode(), 'local')

    def test_remote_lock_change_requires_full_verification(self):
        self.lock['skills'][1]['tree_sha256'] = 'changed'
        self.write_lock()
        self.assertEqual(self.mode(), 'full')

    def test_featured_assets_use_frozen_catalog_verification(self):
        self.write('skills/featured/demo/assets/cover.png', 'updated cover')
        self.assertEqual(self.mode(), 'local')

    def test_featured_definitions_and_skill_code_require_full_verification(self):
        for path in ('skills/featured/demo/featured.yaml',
                     'skills/featured/demo/skill/scripts/tool.py'):
            with self.subTest(path=path):
                self.git('reset', '--hard', self.base)
                self.write(path, 'changed')
                self.assertEqual(self.mode(), 'full')

    def test_workflow_uses_frozen_catalog_for_cover_only_change(self):
        self.git('checkout', '-qb', 'pr')
        self.write('skills/featured/demo/assets/cover.png', 'updated cover')
        self.commit()
        self.git('checkout', '-qb', 'target', self.base)
        self.git('merge', '--no-ff', '-qm', 'PR merge fixture', 'pr')
        args = self.workflow_args()
        self.assertIn('--catalog-only', args)
        self.assertIn('--frozen-lockfile', args)
        self.assertNotIn('--verify-lock-artifacts', args)

    def test_remote_source_change_requires_full_verification(self):
        self.write('skills/builtin-sources.yaml', SOURCES.replace('category: search', 'category: research'))
        self.assertEqual(self.mode(), 'full')

    def test_patches_and_bundler_changes_require_full_verification(self):
        for path in ('skills/patches/catalog.yaml', 'backend/core/cmd/builtin-skill-bundle/main.go'):
            with self.subTest(path=path):
                self.git('reset', '--hard', self.base)
                self.write(path, 'changed')
                self.assertEqual(self.mode(), 'full')

    def test_yaml_aliases_cannot_hide_remote_source_changes(self):
        self.write('skills/builtin-sources.yaml', SOURCES.replace('bundled_skills:', 'bundled_skills: &shared'))
        self.assertEqual(self.mode(), 'full')


if __name__ == '__main__':
    unittest.main()
