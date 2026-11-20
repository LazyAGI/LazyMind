"""Exercise release integrity checks before the native evidence worker runs."""

import hashlib
import json
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
