import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch


SCRIPT = Path(__file__).resolve().parents[1] / 'desktop/scripts/prune-python-runtime.py'
SPEC = importlib.util.spec_from_file_location('desktop_python_pruning', SCRIPT)
pruning = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(pruning)


class PrunePythonRuntimeTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.runtime = self.root / 'runtime'
        (self.runtime / 'runtimes/python').mkdir(parents=True)
        self.site = self.runtime / 'deps/python/algorithm/Lib/site-packages'
        self.site.mkdir(parents=True)

    def put(self, name, content='fixture\n'):
        path = self.site / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content, encoding='utf-8')
        return path

    def test_dry_run_is_non_mutating_and_idempotent_apply(self):
        self.put("numpy/tests/test_example.py")
        first = pruning.trim_runtime(self.runtime, False)
        self.assertEqual(first['python_bytes_before'], first['python_bytes_after'])
        self.assertTrue((self.site / 'numpy/tests/test_example.py').exists())
        applied = pruning.trim_runtime(self.runtime, True)
        self.assertEqual(first['candidate_removed_bytes'], applied['candidate_removed_bytes'])
        again = pruning.trim_runtime(self.runtime, True)
        self.assertEqual(again['candidate_removed_files'], 0)
        self.assertEqual(again['python_bytes_before'], applied['python_bytes_after'])

    def test_rebuildable_bytecode_only(self):
        self.put('package/a.py')
        cached = self.put('package/__pycache__/a.cpython-311.pyc')
        legacy = self.put('package/a.pyc')
        sourceless = self.put('package/only.pyc')
        orphan_cache = self.put('package/__pycache__/only.cpython-311.pyc')
        pruning.trim_runtime(self.runtime, True)
        self.assertFalse(cached.exists())
        self.assertFalse(legacy.exists())
        self.assertTrue(sourceless.exists())
        self.assertTrue(orphan_cache.exists())

    def test_test_suites_removed_but_testing_helpers_data_and_skills_preserved(self):
        suite = self.put('numpy/core/tests/test_example.py')
        preserve = [self.put('numpy/testing/__init__.py'), self.put('numpy/core/data.dat'),
                    self.put('scipy/_lib/_testutils.py'), self.put('unknown/tests/runtime.py'),
                    self.put('pandas-1.dist-info/LICENSE')]
        skill = self.runtime / 'builtin-skills/packages/fixture.zip'
        skill.parent.mkdir(parents=True)
        skill.write_bytes(b'unchanged')
        pruning.trim_runtime(self.runtime, True)
        self.assertFalse(suite.exists())
        self.assertTrue(all(path.exists() for path in preserve))
        self.assertEqual(skill.read_bytes(), b'unchanged')

    def test_symlinked_directories_are_not_followed(self):
        outside = self.root / 'outside'
        outside.mkdir()
        (outside / 'a.py').write_text('fixture')
        bytecode = outside / 'a.pyc'
        bytecode.write_bytes(b'fixture')
        try:
            (self.site / 'linked').symlink_to(outside, target_is_directory=True)
        except OSError:
            self.skipTest('Creating symlinks requires permission on this Windows host')
        pruning.trim_runtime(self.runtime, True)
        self.assertTrue(bytecode.exists())

    def junction(self, link, target):
        subprocess.run(['cmd.exe', '/d', '/c', 'mklink', '/J', str(link), str(target)],
                       check=True, capture_output=True)
        # Remove the junction before TemporaryDirectory cleans the fixture.
        self.addCleanup(lambda: os.rmdir(link) if os.path.lexists(link) else None)

    @unittest.skipUnless(os.name == 'nt', 'Requires a real Windows directory junction')
    def test_uv_python_junction_alias_is_counted_and_pruned_once(self):
        python_root = self.runtime / 'runtimes/python'
        versioned = python_root / 'cpython-3.11.15-windows-x86_64-none'
        source = versioned / 'Lib/__future__.py'
        source.parent.mkdir(parents=True)
        source.write_bytes(b'# original standard library\n')
        cached = source.parent / '__pycache__/__future__.cpython-311.pyc'
        cached.parent.mkdir()
        cached.write_bytes(b'cached bytecode')
        alias = python_root / 'cpython-3.11-windows-x86_64-none'
        self.junction(alias, versioned)
        expected = source.stat().st_size + cached.stat().st_size
        dry = pruning.trim_runtime(self.runtime, False)
        report = pruning.trim_runtime(self.runtime, True)
        self.assertEqual(dry['python_bytes_before'], expected)
        self.assertEqual(dry['candidate_removed_files'], 1)
        self.assertEqual(report['candidate_removed_files'], 1)
        self.assertEqual(report['python_bytes_after'], source.stat().st_size)
        self.assertFalse(cached.exists())
        self.assertEqual((alias / 'Lib/__future__.py').read_bytes(), source.read_bytes())
        self.assertEqual(pruning.trim_runtime(self.runtime, True)['candidate_removed_files'], 0)

    @unittest.skipUnless(os.name == 'nt', 'Requires a real Windows directory junction')
    def test_junction_target_is_not_scanned_or_cleaned(self):
        outside = self.root / 'external-python'
        outside.mkdir()
        (outside / 'a.py').write_bytes(b'original')
        cached = outside / 'a.pyc'
        cached.write_bytes(b'cache')
        empty = outside / 'empty'
        empty.mkdir()
        self.junction(self.site / 'linked', outside)
        # A linked source root must be excluded too, not just child links.
        self.assertEqual(list(pruning.files_under(self.site / 'linked')), [])
        pruning.trim_runtime(self.runtime, True)
        self.assertTrue(cached.is_file())
        self.assertTrue(empty.is_dir())

    def test_disappearing_bytecode_during_deletion_is_harmless(self):
        self.put('package/a.py')
        cached = self.put('package/__pycache__/a.cpython-311.pyc').resolve()
        original_unlink = Path.unlink

        def raced_unlink(path, *args, **kwargs):
            if path == cached:
                original_unlink(path)  # Another cleanup already removed it.
            return original_unlink(path, *args, **kwargs)

        with patch.object(Path, 'unlink', raced_unlink):
            pruning.trim_runtime(self.runtime, True)
        self.assertFalse(cached.exists())

    def test_permission_errors_are_not_silently_ignored(self):
        self.put('package/a.py')
        cached = self.put('package/a.pyc')
        with patch.object(Path, 'unlink', side_effect=PermissionError('access denied')):
            with self.assertRaises(PermissionError):
                pruning.trim_runtime(self.runtime, True)
        self.assertTrue(cached.exists())

    def test_refuses_non_runtime_directory(self):
        with self.assertRaises(ValueError):
            pruning.trim_runtime(self.root, True)

    def test_reports_are_machine_and_human_readable(self):
        self.put("numpy/tests/test_example.py")
        report = pruning.trim_runtime(self.runtime, True)
        output = self.root / 'report.json'
        pruning.write_report(report, output)
        self.assertEqual(json.loads(output.read_text()), report)
        self.assertIn('not installer download savings', output.with_suffix('.md').read_text())


if __name__ == '__main__':
    unittest.main()
