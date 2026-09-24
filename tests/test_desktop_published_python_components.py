import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('published', ROOT / 'desktop/scripts/stage-published-python-components.py')
published = importlib.util.module_from_spec(spec)
spec.loader.exec_module(published)


class PublishedComponentsTests(unittest.TestCase):
    def test_exact_version_contract_accepts_equivalent_pep440_versions(self):
        published.validate_versions({'milvus-lite': '3.0.0'}, {'milvus-lite': '3.0'})

    def test_refuses_changed_missing_and_unexpected_dependencies(self):
        for actual in ({'numpy': '2.0'}, {}, {'numpy': '1.26.4', 'unexpected': '1'}):
            with self.subTest(actual=actual), self.assertRaisesRegex(RuntimeError, 'lock mismatch'):
                published.validate_versions({'numpy': '1.26.4'}, actual)

    def test_lock_rejects_ranges_markers_urls_and_duplicates(self):
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp) / 'lock'
            for value in ('numpy>=1', 'numpy==1; python_version>"3"', 'numpy @ https://example.com/numpy.whl',
                          'numpy==1\nnumpy==2', 'numpy==1.*', ''):
                path.write_text(value)
                with self.subTest(value=value), self.assertRaises(RuntimeError):
                    published.lock_versions(path)

    def test_import_failure_restores_optional_files_and_does_not_activate_catalog(self):
        self.check_import_failure('win32', 'AMD64')

    def test_mac_import_failure_restores_optional_files_and_does_not_activate_catalog(self):
        self.check_import_failure('darwin', 'arm64')

    def check_import_failure(self, host_platform, host_machine):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            runtime = root / 'runtime'
            site = runtime / 'deps/python/algorithm/Lib/site-packages'
            site.mkdir(parents=True)
            optional = set.union(*published.components.SEEDS.values())
            for name in optional | {'numpy'}:
                module = name.replace('-', '_')
                info = site / f'{module}-1.0.dist-info'
                info.mkdir()
                (site / module).mkdir()
                (site / module / '__init__.py').write_text('# synthetic module')
                (info / 'METADATA').write_text(f'Name: {name}\nVersion: 1.0\n')
                (info / 'RECORD').write_text('\n'.join(f'{p},,' for p in [
                    f'{module}/__init__.py', f'{info.name}/METADATA', f'{info.name}/RECORD']))
            lock = root / 'lock'
            lock.write_text(''.join(f'{n}==1.0\n' for n in optional | {'numpy'}))
            entry = dict(schemaVersion=1, platform='windows' if host_platform == 'win32' else 'darwin',
                         arch='amd64' if host_machine == 'AMD64' else 'arm64', pythonAbi='cp311',
                         filename='fixed.zip', packages={n: '1.0' for n in optional})
            catalog = root / 'catalog.json'
            catalog.write_text(json.dumps({**entry, 'components': {'rag': entry}}))
            cache = root / 'cache'
            cache.mkdir()
            (cache / 'fixed.zip').write_bytes(b'fixture')
            def failed_import(*args):
                self.assertFalse((site / 'spacy').exists())
                raise RuntimeError('incompatible overlay')
            with patch.object(published.sys, 'platform', host_platform), \
                 patch.object(published.sys, 'version_info', (3, 11, 15)), \
                 patch.object(published.platform, 'machine', return_value=host_machine), \
                 patch.object(published.sysconfig, 'get_path', return_value=str(site)), \
                 patch.object(published.verification, 'acquire_bundle'), \
                 patch.object(published.verification, 'extract_bundle', return_value=root / 'overlay'), \
                 patch.object(published.verification, 'run_child', side_effect=failed_import):
                with self.assertRaisesRegex(RuntimeError, 'incompatible overlay'):
                    published.stage(runtime, root / 'output', catalog, lock, cache)
            self.assertTrue((site / 'spacy/__init__.py').exists())
            self.assertTrue((site / 'numpy/__init__.py').exists())
            self.assertFalse((runtime / 'config/python-components.json').exists())

    def test_mac_rejects_windows_or_intel_catalog_before_removing_files(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            site = root / 'deps/python/algorithm/lib/python3.11/site-packages'
            site.mkdir(parents=True)
            sentinel = site / 'keep.py'
            sentinel.write_text('# untouched')
            for target, arch in [('windows', 'amd64'), ('darwin', 'amd64')]:
                catalog = root / 'catalog.json'
                entry = dict(schemaVersion=1, platform=target, arch=arch, pythonAbi='cp311')
                catalog.write_text(json.dumps({**entry, 'components': {'rag': entry}}))
                with self.subTest(target=target, arch=arch), \
                     patch.object(published.sys, 'platform', 'darwin'), \
                     patch.object(published.sys, 'version_info', (3, 11, 15)), \
                     patch.object(published.platform, 'machine', return_value='arm64'), \
                     patch.object(published.sysconfig, 'get_path', return_value=str(site)):
                    with self.assertRaisesRegex(RuntimeError, 'Wrong published catalog'):
                        published.stage(root, root / 'out', catalog, root / 'lock', root / 'cache')
                self.assertTrue(sentinel.exists())

    def test_mac_published_catalog_matches_original_release_and_lock(self):
        catalog = json.loads((ROOT / 'desktop/python-components/darwin-arm64.json').read_text())
        entry = catalog['components']['rag']
        expected = published.lock_versions(ROOT / 'desktop/python-components/darwin-arm64-requirements.lock')
        self.assertEqual(entry['filename'], 'lazymind-python-rag-darwin-arm64-cp311-00d718af0b2065c5.zip')
        self.assertEqual(entry['sha256'], '394a6d6b370eb6342d7524ee77fc7e8552fe83d3808ce003ece8f061a27538c5')
        self.assertEqual(entry['sizeBytes'], 65714900)
        self.assertEqual(catalog['platform'], 'darwin')
        self.assertEqual(catalog['arch'], 'arm64')
        self.assertTrue(entry['url'].endswith('/' + entry['filename']))
        published.validate_versions(entry['packages'], {name: expected[name] for name in entry['packages']})

    def test_published_catalog_has_fixed_identity_and_matching_pins(self):
        catalog = json.loads((ROOT / 'desktop/python-components/windows-amd64.json').read_text())
        entry = catalog['components']['rag']
        expected = published.lock_versions(ROOT / 'desktop/python-components/windows-amd64-requirements.lock')
        self.assertEqual(entry['filename'], 'lazymind-python-rag-windows-amd64-cp311-e262c0d2f05fe09d.zip')
        self.assertEqual(entry['sha256'], '258944a85d5aa29c0eb662ab21888c5c2894fdfb2c503d51f2129efa4e083bed')
        self.assertEqual(entry['sizeBytes'], 69342263)
        self.assertTrue(entry['url'].endswith('/' + entry['filename']))
        published.validate_versions(entry['packages'], {name: expected[name] for name in entry['packages']})


if __name__ == '__main__':
    unittest.main()
