"""Exercise release integrity checks before the native evidence worker runs."""

import hashlib
import json
import os
from pathlib import Path
import runpy
import stat
import tempfile
import unittest
from unittest.mock import patch
import zipfile


PROBE = runpy.run_path(str(Path(__file__).with_name('verify-update-format.py')))


class EvidenceArtifactTests(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.root = Path(directory.name)

    def fixture(self, name, data):
        (self.root / name).write_bytes(data)
        return {'size': len(data), 'sha256': hashlib.sha256(data).hexdigest()}

    def test_release_requires_an_exact_artifact_and_catalog(self):
        rag = {'id': 'rag', 'tag': 'a' * 64, 'pythonAbi': 'cp311', 'baseFingerprint': 'b' * 64,
               'packages': {'milvus-lite': '3.0'}, 'schemaVersion': 1, 'signingMode': 'developer-id'}
        catalog = json.dumps({'platform': 'darwin', 'arch': 'arm64', 'schemaVersion': 1,
                              'components': {'rag': rag}}).encode()
        package = self.fixture('package.zip', b'package staged by the build')
        components = self.fixture('components.json', catalog)
        config = {'mode': 'official', 'audience': 'official', 'releaseId': 'darwin-arm64-1.0.0',
                  'version': '1.0.0', 'platform': 'darwin', 'arch': 'arm64', 'signingIdentity': 'ABCDEFGHIJ',
                  'updaterProtocol': 2}
        release = {key: value for key, value in config.items() if key not in ('mode', 'audience')}
        release['package'] = {**package, 'unpackedBytes': 128}
        release['components'] = components
        record = {'config': config, 'release': release, 'formatProfile': 'format-v2-' + 'c' * 64,
                  'componentDescriptor': {key: value for key, value in rag.items()
                                          if key not in ('signingMode', 'schemaVersion')}}
        self.fixture('release.json', json.dumps(record).encode())
        with patch.object(PROBE['sys'], 'platform', 'darwin'), patch.object(PROBE['platform'], 'machine', return_value='arm64'):
            self.assertEqual(PROBE['release_inputs'](self.root, False)[0], record)
            self.fixture('components.json', catalog.replace(b'3.0', b'3.1'))
            with self.assertRaisesRegex(ValueError, 'components.json SHA-256'):
                PROBE['release_inputs'](self.root, False)
            self.fixture('components.json', catalog)
            self.fixture('package.zip', b'Package staged by the build')
            with self.assertRaisesRegex(ValueError, 'package.zip SHA-256'):
                PROBE['release_inputs'](self.root, False)

    def test_windows_pair_uses_default_directory_and_uninstalls_between_packages(self):
        local = self.root / 'User With Spaces'
        installed = local / 'Programs/LazyMind'
        uninstall = installed / 'Uninstall LazyMind.exe'
        release = {'platform': 'windows', 'signingIdentity': 'fixture'}
        order = []
        def run(exe, flags, *, timeout):
            order.append((exe.name, flags))
            if exe == uninstall:
                (installed / 'LazyMind.exe').unlink()
                uninstall.unlink()  # NSIS can leave an empty resources directory.
                return
            self.assertEqual(flags, '/S --full-install')
            (installed / 'resources').mkdir(parents=True)
            (installed / 'LazyMind.exe').touch()
            uninstall.touch()
        with patch.dict(os.environ, LOCALAPPDATA=str(local), GITHUB_ACTIONS='true', RUNNER_ENVIRONMENT='github-hosted'), \
                patch.dict(PROBE['materialized_app'].__wrapped__.__globals__, run_windows_installer=run, verify_windows_signature=lambda *_: None):
            for role in ('source', 'target'):
                with PROBE['materialized_app'](Path(role + '.exe'), release, self.root / (role + '-app'), False) as resources:
                    self.assertEqual(resources, installed / 'resources')
                    order.append((role, 'verify'))
                self.assertFalse(installed.exists())
            installed.mkdir()
            with self.assertRaisesRegex(ValueError, 'existing Windows installation'), \
                    PROBE['materialized_app'](Path('target.exe'), release, self.root / 'target-app', False):
                self.fail('unexpected install')
        self.assertEqual(order, [(name, action) for role in ('source', 'target')
                                for name, action in [(role + '.exe', '/S --full-install'), (role, 'verify'), (uninstall.name, '/S')]])

    def test_fixture_data_accepts_native_windows_and_posix_paths(self):
        prefix = 'partitions/_default'
        data = self.root / prefix / 'data' / 'segment.parquet'
        data.parent.mkdir(parents=True)
        data.write_bytes(b'fixture')
        for relative in ('data/segment.parquet', r'data\segment.parquet'):
            self.assertEqual(PROBE['fixture_data_file'](self.root, prefix, relative), data)
        for relative in ('/data/segment.parquet', r'C:\data\segment.parquet', r'data\..\segment.parquet',
                         r'data\C:\segment.parquet', r'\\host\data\segment.parquet', 'data//segment.parquet', None):
            with self.subTest(relative=relative), self.assertRaises(ValueError):
                PROBE['fixture_data_file'](self.root, prefix, relative)

    def test_uninstall_cleanup_leaves_files_and_links_untouched(self):
        directory = self.root / 'LazyMind'
        directory.mkdir()
        file = directory / 'leftover.exe'
        file.write_bytes(b'retain')
        with self.assertRaises((ValueError, OSError)):
            PROBE['remove_empty_install_tree'](directory)
        self.assertEqual(file.read_bytes(), b'retain')
        file.unlink()
        outside = self.root / 'outside'
        outside.mkdir()
        link = directory / 'redirect'
        try:
            link.symlink_to(outside, target_is_directory=True)
        except OSError as error:
            self.skipTest(f'cannot create directory symlink: {error}')
        with self.assertRaises((ValueError, OSError)):
            PROBE['remove_empty_install_tree'](directory)
        self.assertTrue(link.is_symlink() and outside.is_dir())

    def test_app_zip_requires_paths_and_links_inside_the_bundle(self):
        package = self.root / 'package.zip'
        root = 'LazyMind.app/Contents'
        for unsafe in ('member', 'symlink', 'symlink ancestor', None):
            with self.subTest(unsafe=unsafe):
                with zipfile.ZipFile(package, 'w') as archive:
                    archive.writestr(root + '/Resources/file', b'content')
                    link = zipfile.ZipInfo(root + '/Frameworks/Current')
                    link.create_system = 3  # Unix mode bits
                    link.external_attr = (stat.S_IFLNK | 0o755) << 16
                    archive.writestr(link, '../../../outside' if unsafe == 'symlink' else 'Versions/A')
                    if unsafe == 'member':
                        archive.writestr(root + '/../../outside', b'escaped')
                    if unsafe == 'symlink ancestor':
                        archive.writestr(root + '/Frameworks/Current/payload', b'escaped')
                if unsafe:
                    with self.assertRaises(ValueError):
                        PROBE['validate_app_zip'](package, 'LazyMind.app', 1024)
                else:
                    PROBE['validate_app_zip'](package, 'LazyMind.app', 1024)


if __name__ == '__main__':
    unittest.main()
