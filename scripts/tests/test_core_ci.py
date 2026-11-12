import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import textwrap
import unittest


SCRIPT = Path(__file__).resolve().parents[1] / 'core_test_shard.py'
WORKFLOW = SCRIPT.parents[1] / '.github/workflows/ci.yml'
SHARDS = ('root', 'chat', 'workflow', 'other')
PACKAGES = (
    'lazymind/core',
    'lazymind/core/academic',
    'lazymind/core/chat',
    'lazymind/core/migrate',
    'lazymind/core/migrations',
    'lazymind/core/workflow',
    'lazymind/core/academic/child',
    'lazymind/core/chat/child',
    'lazymind/core/migrate/child',
    'lazymind/core/migrations/child',
    'lazymind/core/workflow/child',
    'lazymind/core/new-package',
    'lazymind/core/package-without-tests',
)


def workflow_step_block(name):
    workflow = WORKFLOW.read_text(encoding='utf-8')
    start = workflow.index(f'      - name: {name}\n')
    end = workflow.find('\n      - ', start + 1)
    return workflow[start:end if end >= 0 else None]


def workflow_step(name):
    step = workflow_step_block(name).splitlines()
    run = step.index('        run: |')
    lines = []
    for line in step[run + 1:]:
        if line and not line.startswith('          '):
            break
        lines.append(line)
    return textwrap.dedent('\n'.join(lines))


class CoreTestShardTests(unittest.TestCase):
    def select(self, shard, packages=PACKAGES):
        return subprocess.run(
            [sys.executable, str(SCRIPT), shard], input='\n'.join(packages),
            capture_output=True, text=True,
        )

    def test_heavy_packages_are_spread_across_the_existing_shards(self):
        for shard, packages in (
            ('root', ['lazymind/core', 'lazymind/core/academic']),
            ('chat', ['lazymind/core/chat', 'lazymind/core/migrations']),
            ('workflow', ['lazymind/core/migrate', 'lazymind/core/workflow']),
        ):
            with self.subTest(shard=shard):
                result = self.select(shard)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(result.stdout.splitlines(), packages)

    def test_other_keeps_subpackages_new_packages_and_packages_without_tests(self):
        result = self.select('other')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.splitlines(), [
            'lazymind/core/academic/child', 'lazymind/core/chat/child',
            'lazymind/core/migrate/child', 'lazymind/core/migrations/child', 'lazymind/core/workflow/child',
            'lazymind/core/new-package', 'lazymind/core/package-without-tests',
        ])

    def test_partition_is_complete_and_has_no_duplicates(self):
        selected = []
        for shard in SHARDS:
            result = self.select(shard)
            self.assertEqual(result.returncode, 0, result.stderr)
            selected.extend(result.stdout.splitlines())
        self.assertCountEqual(selected, PACKAGES)
        self.assertEqual(len(selected), len(set(selected)))

    def test_unknown_shard_fails_without_printing_packages(self):
        result = self.select('unknown')
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(result.stdout, '')

    def test_empty_input_fails_for_every_shard(self):
        for shard in SHARDS:
            with self.subTest(shard=shard):
                result = self.select(shard, ('', '  '))
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(result.stdout, '')

    def test_missing_shard_packages_fail_instead_of_defaulting_to_root(self):
        for shard in SHARDS:
            with self.subTest(shard=shard):
                packages = PACKAGES[:6] if shard == 'other' else ('lazymind/core/new-package',)
                result = self.select(shard, packages)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(result.stdout, '')


class CoreCIWorkflowTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        (self.root / 'backend/core').mkdir(parents=True)
        self.runner_temp = self.root / 'runner-temp'
        self.profiles = self.runner_temp / 'core-coverage'
        self.profiles.mkdir(parents=True)

    def run_step(self, name, **env):
        return subprocess.run(
            ['bash', '-e', '-o', 'pipefail', '-c', workflow_step(name)],
            cwd=self.root, env={**os.environ, 'RUNNER_TEMP': str(self.runner_temp), **env},
            capture_output=True, text=True,
        )

    def write_profiles(self):
        records = [
            'lazymind/core/root.go:1.1,2.1 1 1',
            'lazymind/core/chat/chat.go:1.1,2.1 9 0',
            'lazymind/core/workflow/workflow.go:1.1,2.1 2 1',
            'lazymind/core/other/other.go:1.1,2.1 8 0',
        ]
        for shard, record in zip(SHARDS, records):
            (self.profiles / f'coverage-core-{shard}.out').write_text(f'mode: set\n{record}\n')
        return records

    def test_gate_passes_only_when_all_shards_succeeded(self):
        for status in ('success', 'failure', 'cancelled', 'skipped'):
            with self.subTest(status=status):
                result = self.run_step('Require every core shard to pass', SHARDS_RESULT=status)
                self.assertEqual(result.returncode == 0, status == 'success', result.stderr)

    def test_merge_preserves_all_statement_counts_for_weighted_coverage(self):
        records = self.write_profiles()
        result = self.run_step('Merge backend core coverage')
        self.assertEqual(result.returncode, 0, result.stderr)
        lines = (self.root / 'backend/core/coverage-core.out').read_text().splitlines()
        self.assertEqual(lines[0], 'mode: set')
        self.assertCountEqual(lines[1:], records)
        blocks = [line.rsplit(' ', 2) for line in lines[1:]]
        total = sum(int(statements) for _, statements, _ in blocks)
        covered = sum(int(statements) for _, statements, count in blocks if int(count))
        self.assertEqual(total, 20)
        self.assertEqual(covered / total * 100, 15)

    def test_merge_rejects_a_missing_shard(self):
        for shard in SHARDS:
            with self.subTest(shard=shard):
                self.write_profiles()
                (self.profiles / f'coverage-core-{shard}.out').unlink()
                result = self.run_step('Merge backend core coverage')
                self.assertNotEqual(result.returncode, 0)

    def test_merge_rejects_a_different_coverage_mode(self):
        self.write_profiles()
        (self.profiles / 'coverage-core-chat.out').write_text('mode: count\n')
        result = self.run_step('Merge backend core coverage')
        self.assertNotEqual(result.returncode, 0)

    def test_build_cache_is_distinct_per_shard_and_revision(self):
        block = workflow_step_block('Cache core shard build outputs')
        key = next(line.strip()[5:] for line in block.splitlines() if line.startswith('          key: '))
        restore = block.split('          restore-keys: |\n', 1)[1].strip()

        def render(template, shard='root', revision='first', dependency='lock-a', **overrides):
            values = {
                'runner.os': 'Linux', 'runner.arch': 'X64',
                'steps.core-go-cache.outputs.image': 'ubuntu26',
                'steps.core-go-cache.outputs.version': 'go1.25.11',
                'matrix.shard': shard, 'github.sha': revision, **overrides,
            }
            rendered = re.sub(r'\$\{\{ hashFiles\(.*?\) \}\}', dependency, template)
            for name, value in values.items():
                rendered = rendered.replace('${{ ' + name + ' }}', value)
            self.assertNotIn('${{', rendered)
            return rendered

        self.assertEqual(len({render(key, shard=shard) for shard in SHARDS}), 4)
        self.assertNotEqual(render(key), render(key, revision='second'))
        for shard in SHARDS:
            self.assertTrue(render(key, shard=shard, revision='second').startswith(render(restore, shard=shard)))
        self.assertFalse(render(key, shard='chat').startswith(render(restore, shard='root')))
        self.assertNotEqual(render(restore), render(restore, dependency='lock-b'))
        for name, value in (('runner.os', 'Windows'), ('runner.arch', 'ARM64'),
                            ('steps.core-go-cache.outputs.image', 'ubuntu28'),
                            ('steps.core-go-cache.outputs.version', 'go1.26.0')):
            with self.subTest(changed=name):
                self.assertNotEqual(render(restore), render(restore, **{name: value}))
        for dependency in ('backend/core/go.mod', 'backend/core/go.sum',
                           'tests/backend/core/go.mod', 'tests/backend/core/go.sum'):
            self.assertIn(dependency, key)

    def test_shared_module_cache_has_only_one_writer(self):
        restore = workflow_step_block('Restore core Go modules')
        save = workflow_step_block('Save core Go modules')
        self.assertIn('uses: actions/cache/restore@v4', restore)
        self.assertNotIn('matrix.shard', restore)
        self.assertIn("if: matrix.shard == 'other' && steps.core-go-modules.outputs.cache-hit != 'true'", save)
        self.assertIn('uses: actions/cache/save@v4', save)
        self.assertIn('key: ${{ steps.core-go-modules.outputs.cache-primary-key }}', save)
        self.assertIn('path: ${{ steps.core-go-cache.outputs.modules }}', save)
        self.assertIn('path: ${{ steps.core-go-cache.outputs.build }}',
                      workflow_step_block('Cache core shard build outputs'))


if __name__ == '__main__':
    unittest.main()
