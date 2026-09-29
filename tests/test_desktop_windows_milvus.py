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


if __name__ == '__main__':
    unittest.main()
