"""Exercise the scheduler's real tag command against a local Git remote."""
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

import yaml

ROOT = Path(__file__).resolve().parents[2]


class ScheduleSnapshotTests(unittest.TestCase):
    def test_snapshot_tags_same_main_commit_without_moving_main(self):
        workflow = yaml.safe_load((ROOT / '.github/workflows/agent-e2e-scheduled.yml').read_text())
        command = workflow['jobs']['snapshot']['steps'][1]['run']
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            remote = root / 'remote.git'
            source = root / 'source'
            env = {**os.environ, 'GIT_CONFIG_NOSYSTEM': '1',
                   'GIT_AUTHOR_NAME': 'CI test', 'GIT_AUTHOR_EMAIL': 'ci@example.invalid',
                   'GIT_COMMITTER_NAME': 'CI test', 'GIT_COMMITTER_EMAIL': 'ci@example.invalid',
                   'GITHUB_RUN_ID': '12345', 'GITHUB_OUTPUT': str(root / 'output'),
                   'GITHUB_STEP_SUMMARY': str(root / 'summary')}

            def git(*args, cwd=source):
                return subprocess.check_output(['git', *args], cwd=cwd, env=env,
                                               stderr=subprocess.PIPE, text=True).strip()

            git('init', '--bare', str(remote), cwd=root)
            source.mkdir()
            git('init', '-b', 'main')
            (source / 'fixture').write_text('main snapshot\n')
            git('add', 'fixture')
            git('commit', '-m', 'fixture')
            sha = git('rev-parse', 'HEAD')
            git('remote', 'add', 'origin', str(remote))
            git('push', 'origin', 'main')
            tags = []
            for attempt in ('1', '2'):
                (root / 'output').write_text('')
                subprocess.run(['bash', '-c', command], cwd=source,
                               env={**env, 'GITHUB_RUN_ATTEMPT': attempt},
                               check=True, capture_output=True, text=True)
                outputs = dict(line.split('=', 1) for line in (root / 'output').read_text().splitlines())
                self.assertEqual(outputs['sha'], sha)
                self.assertTrue(outputs['tag'].startswith('ci-'))
                self.assertEqual(git('rev-parse', 'refs/tags/' + outputs['tag'], cwd=remote), sha)
                self.assertEqual(git('rev-parse', 'refs/heads/main', cwd=remote), sha)
                tags.append(outputs['tag'])
            self.assertNotEqual(*tags)
