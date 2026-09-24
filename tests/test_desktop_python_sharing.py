import hashlib
import importlib.util
import json
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest
import venv

spec = importlib.util.spec_from_file_location('sharing', Path(__file__).parents[1] / 'desktop/scripts/share-python-dependencies.py')
sharing = importlib.util.module_from_spec(spec)
spec.loader.exec_module(sharing)

class SharingTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.runtime = Path(self.temp.name) / 'runtime'
        for env in ('algorithm', 'auth-service', 'channel-gateway'):
            venv.EnvBuilder(with_pip=False).create(self.runtime / 'deps/python' / env)
        self.sites = sharing.sites_in(self.runtime)

    def wheel(self, env, name='sample', version='1.0', contents='VALUE = 42\n', extra=None):
        site = self.sites[env]
        files = {f'{name}/__init__.py': contents, f'{name}/data.txt': '中文 resource',
                 f'{name}-{version}.dist-info/METADATA': f'Metadata-Version: 2.1\nName: {name}\nVersion: {version}\n',
                 f'{name}-{version}.dist-info/WHEEL': 'Wheel-Version: 1.0\nRoot-Is-Purelib: true\nTag: py3-none-any\n'}
        if extra: files.update(extra)
        record = f'{name}-{version}.dist-info/RECORD'
        files[record] = ''.join(f'{f},,\n' for f in [*files, record])
        for name, value in files.items():
            p = site / name; p.parent.mkdir(parents=True, exist_ok=True); p.write_text(value, encoding='utf-8')

    def probe(self, runtime, env, expected):
        exe = runtime / 'deps/python' / env / ('Scripts/python.exe' if sys.platform == 'win32' else 'bin/python')
        code = "import sample,importlib.metadata as m,importlib.resources as r; print(sample.VALUE,m.version('sample'),r.files('sample').joinpath('data.txt').read_text(encoding='utf-8'))"
        result = subprocess.check_output([str(exe), '-I', '-B', '-X', 'utf8', '-c', code], text=True, encoding='utf-8')
        self.assertIn(expected, result)

    def test_share_relocate_resources_metadata_and_resume(self):
        for env in self.sites: self.wheel(env)
        report = sharing.share(self.runtime, True)
        self.assertEqual(len(report['shared']), 1)
        self.assertGreater(report['savedBytes'], 0)
        for env in self.sites: self.probe(self.runtime, env, '42 1.0 中文 resource')
        moved = self.runtime.with_name('移动 runtime with spaces')
        self.runtime.rename(moved)
        for env in self.sites: self.probe(moved, env, '42 1.0 中文 resource')
        self.assertEqual(sharing.share(moved, True)['savedBytes'], report['savedBytes'])

    def test_version_content_and_environment_isolation(self):
        self.wheel('algorithm',version='2.0',contents='VALUE = 99\n')
        self.wheel('auth-service')
        self.wheel('channel-gateway')
        report=sharing.share(self.runtime, True)
        self.assertEqual(report['shared'][0]['environments'], ['auth-service','channel-gateway'])
        self.probe(self.runtime,'algorithm','99 2.0')
        self.probe(self.runtime,'auth-service','42 1.0')

    def test_same_version_different_content_and_unrecorded_files_not_shared(self):
        self.wheel('algorithm', contents='VALUE = 99\n')
        self.wheel('auth-service'); self.wheel('channel-gateway')
        (self.sites['channel-gateway'] / 'sample/unrecorded.txt').write_text('keep me')
        self.assertFalse(sharing.share(self.runtime,True)['shared'])
        self.assertTrue((self.sites['channel-gateway'] / 'sample/unrecorded.txt').exists())

    def test_hooks_native_and_namespace_are_skipped(self):
        for env in self.sites: self.wheel(env,extra={'sample/hook.pth': 'import dangerous\n'})
        self.assertFalse(sharing.share(self.runtime,True)['shared'])

    def test_pruned_record_bytecode_does_not_block_sharing(self):
        for env in self.sites:
            self.wheel(env)
            with (self.sites[env] / 'sample-1.0.dist-info/RECORD').open('a') as f:
                f.write('sample/__pycache__/removed.cpython-311.pyc,,\n')
        self.assertEqual(len(sharing.share(self.runtime, True)['shared']), 1)

    def test_unrecorded_symlink_is_not_followed_or_deleted(self):
        self.wheel('algorithm'); self.wheel('auth-service')
        outside = Path(self.temp.name) / 'outside.txt'
        outside.write_text('preserve')
        link = self.sites['auth-service'] / 'sample/link.txt'
        try:
            link.symlink_to(outside)
        except OSError:
            self.skipTest('symlink creation unavailable')
        self.assertFalse(sharing.share(self.runtime, True)['shared'])
        self.assertEqual(outside.read_text(), 'preserve')
        self.assertTrue(link.is_symlink())

    def test_damaged_shared_copy_fails_resume(self):
        for env in self.sites: self.wheel(env)
        report=sharing.share(self.runtime,True)
        (self.runtime / report['shared'][0]['path'] / 'sample/__init__.py').write_text('broken')
        with self.assertRaises(RuntimeError): sharing.share(self.runtime,True)

if __name__ == '__main__': unittest.main()
