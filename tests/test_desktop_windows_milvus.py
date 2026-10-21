"""Guard the Windows-only third-party patch and its wheel RECORD bookkeeping."""
import contextlib
import csv
import importlib.util
import io
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('milvus_patch', ROOT / 'desktop/scripts/patch-windows-milvus.py')
fix = importlib.util.module_from_spec(spec)
spec.loader.exec_module(fix)


class WindowsMilvusPatchTests(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.site = Path(temp.name)
        self.original = b'os.rename(tmp_path, target_path)\n'
        self.patched = b'os.replace(tmp_path, target_path)\n'
        for key, value in [('ORIGINAL_SHA256', fix.digest(self.original)), ('PATCHED_SHA256', fix.digest(self.patched))]:
            self.enterContext(patch.object(fix, key, value))
        self.source = self.site / fix.SOURCE
        self.source.parent.mkdir(parents=True)
        self.source.write_bytes(self.original)
        self.info = self.site / 'milvus_lite-3.0.dist-info'
        self.info.mkdir()
        (self.info / 'METADATA').write_text('Name: milvus-lite\nVersion: 3.0\n')
        self.record = self.info / 'RECORD'
        self.record.write_text(f'{fix.SOURCE},{fix.record_hash(self.original)},{len(self.original)}\n'
                               f'{self.info.name}/METADATA,,\n{self.info.name}/RECORD,,\n')
        self.index_source = self.site / fix.INDEX_SOURCE
        self.index_source.write_bytes(fix.INDEX_OLD)
        self.enterContext(patch.object(fix, 'INDEX_ORIGINAL_SHA256', fix.digest(fix.INDEX_OLD)))
        self.enterContext(patch.object(fix, 'INDEX_PATCHED_SHA256', fix.digest(fix.INDEX_NEW)))
        with self.record.open('a') as record:
            record.write(f'{fix.INDEX_SOURCE},{fix.record_hash(fix.INDEX_OLD)},{len(fix.INDEX_OLD)}\n')
        self.record_original = self.record.read_bytes()

    def test_updates_record_and_is_idempotent(self):
        cache = self.source.parent / '__pycache__' / 'manifest.cpython-311.pyc'
        cache.parent.mkdir()
        cache.write_bytes(b'stale cache')
        report = fix.patch_site(self.site)
        self.assertTrue(report['changed'])
        self.assertEqual(self.source.read_bytes(), self.patched)
        self.assertFalse(cache.exists())
        fix.verify_site(self.site)
        rows = list(csv.reader(io.StringIO(self.record.read_text())))
        self.assertEqual(rows[0], [fix.SOURCE, fix.record_hash(self.patched), str(len(self.patched))])
        record = self.record.read_bytes()
        self.assertFalse(fix.patch_site(self.site)['changed'])
        self.assertEqual(self.record.read_bytes(), record)

    def test_rejects_new_dependency_version(self):
        (self.info / 'METADATA').write_text('Name: milvus-lite\nVersion: 3.1\n')
        with self.assertRaisesRegex(RuntimeError, 'requires exactly one'):
            fix.patch_site(self.site)
        self.assertEqual(self.source.read_bytes(), self.original)

    def test_rejects_unreviewed_source(self):
        self.source.write_bytes(self.original + b'# upstream change')
        with self.assertRaisesRegex(RuntimeError, 'hash changed'):
            fix.patch_site(self.site)
        self.assertEqual(self.record.read_bytes(), self.record_original)

    def test_rejects_missing_or_incorrect_source_record(self):
        for row in ('', f'{fix.SOURCE},sha256=invalid,1\n'):
            self.record.write_text(row + f'{self.info.name}/RECORD,,\n')
            with self.subTest(row=row), self.assertRaisesRegex(RuntimeError, 'does not match RECORD'):
                fix.patch_site(self.site)
            self.assertEqual(self.source.read_bytes(), self.original)

    def test_record_failure_restores_source(self):
        write = fix.atomic_write
        def fail_record(path, data):
            if path.resolve() == self.record.resolve():
                raise PermissionError('record locked')
            return write(path, data)
        with patch.object(fix, 'atomic_write', side_effect=fail_record):
            with self.assertRaises(PermissionError):
                fix.patch_site(self.site)
        self.assertEqual(self.source.read_bytes(), self.original)
        self.assertEqual(self.record.read_bytes(), self.record_original)
        self.assertEqual(self.index_source.read_bytes(), fix.INDEX_OLD)

    def test_rejects_unreviewed_index_before_modifying_manifest(self):
        self.index_source.write_bytes(fix.INDEX_OLD + b'# changed')
        with self.assertRaisesRegex(RuntimeError, 'hash changed'):
            fix.patch_site(self.site)
        self.assertEqual(self.source.read_bytes(), self.original)
        self.assertEqual(self.record.read_bytes(), self.record_original)

    def test_verifier_rejects_unpatched_component(self):
        with self.assertRaisesRegex(RuntimeError, 'fix is missing'):
            fix.verify_site(self.site)

    def test_cli_refuses_mac_before_modifying_files(self):
        with patch.object(fix.sys, 'platform', 'darwin'), \
             patch.object(fix.sys, 'argv', ['patch', str(self.site)]), \
             contextlib.redirect_stderr(io.StringIO()):
            with self.assertRaises(SystemExit) as error:
                fix.main()
        self.assertEqual(error.exception.code, 2)
        self.assertEqual(self.source.read_bytes(), self.original)


class AtomicIndexPublicationTests(unittest.TestCase):
    def test_process_death_does_not_publish_empty_index_and_retry_succeeds(self):
        import subprocess
        import sys
        import textwrap
        with tempfile.TemporaryDirectory() as directory:
            target = Path(directory) / 'segment.vector.brute_force.idx'
            # Terminate after opening the output, before the serializer writes.
            # os._exit also skips finally blocks, like Windows TerminateProcess.
            script = '''import os
from pathlib import Path
class Index:
    def save(self, destination):
        with open(destination, 'wb'):
            os._exit(71)
idx = Index()
'''
            script += f'path = {str(target)!r}\nindex_dir = {directory!r}\n'
            script += textwrap.dedent(fix.INDEX_NEW.decode())
            result = subprocess.run([sys.executable, '-c', script], capture_output=True, text=True)
            self.assertEqual(result.returncode, 71, result.stderr)
            self.assertFalse(target.exists(), 'an interrupted index must not be visible to the loader')
            self.assertTrue(list(Path(directory).glob('.index-*.tmp')))
            class CompleteIndex:
                def save(self, destination):
                    Path(destination).write_bytes(b'complete index')
            exec(textwrap.dedent(fix.INDEX_NEW.decode()),
                 {'os': __import__('os'), 'idx': CompleteIndex(), 'path': str(target), 'index_dir': directory})
            self.assertEqual(target.read_bytes(), b'complete index')

    def test_serialization_failure_preserves_existing_index(self):
        import textwrap
        with tempfile.TemporaryDirectory() as directory:
            target = Path(directory) / 'segment.idx'
            target.write_bytes(b'existing valid index')
            class BrokenIndex:
                def save(self, destination):
                    Path(destination).write_bytes(b'partial')
                    raise OSError('disk full')
            with self.assertRaisesRegex(OSError, 'disk full'):
                exec(textwrap.dedent(fix.INDEX_NEW.decode()),
                     {'os': __import__('os'), 'idx': BrokenIndex(), 'path': str(target), 'index_dir': directory})
            self.assertEqual(target.read_bytes(), b'existing valid index')
            self.assertFalse(list(Path(directory).glob('.index-*')))


if __name__ == '__main__':
    unittest.main()
