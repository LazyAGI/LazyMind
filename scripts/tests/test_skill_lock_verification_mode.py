import json
from pathlib import Path
import subprocess
import tempfile
import unittest


SCRIPT = Path(__file__).resolve().parents[1] / 'skill_lock_verification_mode.py'
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
