"""Online metadata and publication gates; never upload to a real account."""
import hashlib
import importlib.util
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import Mock, patch
import zipfile

SCRIPT = Path(__file__).resolve().parents[1] / 'desktop/scripts/publish-python-components.py'
spec = importlib.util.spec_from_file_location('publish_components', SCRIPT)
publish = importlib.util.module_from_spec(spec)
spec.loader.exec_module(publish)
verify = publish.verify


def entry_fixture():
    tag = 'a' * 64
    base = f'https://modelscope.cn/datasets/test/repo/resolve/master/RAG/tags/{tag}/'
    return dict(id='rag', platform='windows', arch='amd64', pythonAbi='cp311', tag=tag,
                revision=tag, baseFingerprint='b' * 64, sha256='c' * 64,
                filename='rag.zip', url=base + 'rag.zip', catalogUrl=base + 'python-components.json',
                sizeBytes=1, unpackedBytes=100, packages={'orjson': '3.11.3'})


def catalog_for(entry):
    return dict(schemaVersion=1, platform=entry['platform'], arch=entry['arch'], components={'rag': entry})


class OnlineCatalogTests(unittest.TestCase):
    def resolve(self, pinned, catalog):
        opener = Mock()
        opener.open.return_value = io.BytesIO(json.dumps(catalog).encode())
        with patch.object(verify, 'build_opener', return_value=opener):
            return verify.resolve_catalog_entry(pinned)

    def test_resigned_revision_and_hash_replace_embedded_hints(self):
        pinned = entry_fixture()
        updated = {**pinned, 'revision': 'd' * 64, 'sha256': 'e' * 64, 'filename': 'signed.zip',
                   'url': pinned['url'].replace('rag.zip', 'signed.zip'), 'catalogUrl': 'https://other.example/catalog'}
        updated.pop('tag')  # Compatible with the supplied schema-1 repair catalog.
        resolved = self.resolve(pinned, catalog_for(updated))
        self.assertEqual(resolved['sha256'], updated['sha256'])
        self.assertEqual(resolved['revision'], updated['revision'])
        self.assertEqual(resolved['tag'], pinned['tag'])
        self.assertEqual(resolved['catalogUrl'], pinned['catalogUrl'])

    def test_bad_remote_metadata_is_rejected(self):
        pinned = entry_fixture()
        for field, value in [('platform', 'darwin'), ('arch', 'arm64'), ('pythonAbi', 'cp312'),
                             ('baseFingerprint', 'd' * 64), ('packages', {}), ('tag', 'e' * 64),
                             ('revision', '../escape'), ('sha256', 'bad'), ('sizeBytes', 0),
                             ('filename', '../escape.zip'), ('url', 'http://example.org/rag.zip')]:
            with self.subTest(field=field), self.assertRaises(RuntimeError):
                self.resolve(pinned, catalog_for({**pinned, field: value}))

    def test_oversize_catalog_is_rejected(self):
        opener = Mock()
        opener.open.return_value = io.BytesIO(b'x' * (1024 * 1024 + 1))
        with patch.object(verify, 'build_opener', return_value=opener), self.assertRaisesRegex(RuntimeError, 'exceeds'):
            verify.resolve_catalog_entry(entry_fixture())

    def test_legacy_catalog_does_not_contact_network(self):
        entry = entry_fixture()
        entry.pop('catalogUrl')
        with patch.object(verify, 'build_opener') as opener:
            self.assertEqual(verify.resolve_catalog_entry(entry), entry)
            opener.assert_not_called()


class PublicationTests(unittest.TestCase):
    def test_hf_uses_main_and_verifies_its_own_catalog_without_changing_local_catalog(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            entry = self.fixture(root)
            original = (root / 'python-components.json').read_bytes()
            api = Mock()
            with patch.object(verify, 'resolve_catalog_entry', side_effect=lambda value: value) as resolve, \
                    patch.object(verify, 'acquire_bundle'):
                publish.publish(root, 'test/mirror', api=api, host='hf')
            self.assertTrue(resolve.call_args.args[0]['catalogUrl'].startswith(
                'https://huggingface.co/datasets/test/mirror/resolve/main/RAG/tags/'))
            self.assertEqual((root / 'python-components.json').read_bytes(), original)
            self.assertEqual(api.upload_file.call_count, 3)
            for call in api.upload_file.call_args_list:
                self.assertEqual(call.kwargs['revision'], 'main')

    def test_adhoc_requires_label_and_checks_actual_signatures(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            entry = self.fixture(root, 'darwin')
            api = Mock()
            with self.assertRaisesRegex(RuntimeError, 'labelled'):
                publish.publish(root, 'test/repo', python=Path('/python'), api=api, signing_mode='adhoc')
            api.upload_file.assert_not_called()
            old = entry['filename']
            entry.update(filename='rag-adhoc-test.zip', signingMode='adhoc')
            entry['url'] = entry['url'].replace(old, entry['filename'])
            (root / old).rename(root / entry['filename'])
            (root / 'python-components.json').write_text(json.dumps(catalog_for(entry)))
            with patch.object(verify, 'verify_macos_team_id') as signatures, \
                    patch.object(verify, 'resolve_catalog_entry', return_value=entry), \
                    patch.object(verify, 'acquire_bundle'):
                publish.publish(root, 'test/repo', python=Path('/python'), api=api, signing_mode='adhoc')
            self.assertEqual(signatures.call_args.kwargs, {'adhoc': True})

    def fixture(self, directory, platform='windows'):
        entry = entry_fixture()
        entry['platform'] = platform
        with zipfile.ZipFile(directory / entry['filename'], 'w') as archive:
            archive.writestr('bundle-manifest.json', json.dumps(entry))
            archive.writestr('site-packages/orjson/__init__.py', '# fixture')
        payload = (directory / entry['filename']).read_bytes()
        entry.update(sha256=hashlib.sha256(payload).hexdigest(), sizeBytes=len(payload))
        (directory / 'python-components.json').write_text(json.dumps(catalog_for(entry)))
        return entry

    def test_zip_and_checksums_precede_catalog_then_download_verification(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            entry = self.fixture(root)
            order = []
            api = Mock()
            api.upload_file.side_effect = lambda **kw: order.append(Path(kw['path_in_repo']).name)
            with patch.object(verify, 'resolve_catalog_entry', return_value=entry) as resolve, \
                    patch.object(verify, 'acquire_bundle', side_effect=lambda *args: order.append('download')):
                publish.publish(root, 'test/repo', api=api)
            self.assertEqual(order, ['rag.zip', 'SHA256SUMS', 'python-components.json', 'download'])
            resolve.assert_called_once_with(entry)
            for call in api.upload_file.call_args_list:
                self.assertEqual(call.kwargs['repo_type'], 'dataset')
                self.assertTrue(call.kwargs['path_in_repo'].startswith('RAG/tags/' + entry['tag'] + '/'))

    def test_mac_signatures_checked_against_explicit_host_before_upload(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            entry = self.fixture(root, 'darwin')
            api = Mock()
            with patch.object(verify, 'verify_macos_team_id', side_effect=RuntimeError('TeamIdentifier mismatch')) as signed:
                with self.assertRaisesRegex(RuntimeError, 'TeamIdentifier'):
                    publish.publish(root, 'test/repo', python=Path('/signed/python'), api=api)
                self.assertEqual(signed.call_args.args[1], Path('/signed/python'))
                api.upload_file.assert_not_called()
            with self.assertRaisesRegex(RuntimeError, 'signed bundled Python'):
                publish.publish(root, 'test/repo', api=api)

    def test_corruption_or_wrong_destination_never_uploads(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            entry = self.fixture(root)
            api = Mock()
            with self.assertRaisesRegex(RuntimeError, 'destination'):
                publish.publish(root, 'wrong/repo', api=api)
            (root / entry['filename']).write_bytes(b'corrupt')
            with self.assertRaisesRegex(RuntimeError, 'size'):
                publish.publish(root, 'test/repo', api=api)
            api.upload_file.assert_not_called()

    def test_failed_zip_upload_never_publishes_catalog(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self.fixture(root)
            api = Mock()
            api.upload_file.side_effect = RuntimeError('upload failed')
            with self.assertRaisesRegex(RuntimeError, 'upload failed'):
                publish.publish(root, 'test/repo', api=api)
            self.assertEqual(api.upload_file.call_count, 1)


if __name__ == '__main__':
    unittest.main()
