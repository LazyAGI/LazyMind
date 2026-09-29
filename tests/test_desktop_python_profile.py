import importlib.util
from pathlib import Path
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('profile', ROOT / 'desktop/scripts/desktop-python-profile.py')
profile = importlib.util.module_from_spec(spec)
spec.loader.exec_module(profile)


class ProfileTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.runtime = Path(self.temp.name) / 'runtime'
        self.site = self.runtime / 'deps/python/algorithm/lib/python3.11/site-packages'
        self.site.mkdir(parents=True)

    def distribution(self, name, module, requires=()):
        info = self.site / f'{name.replace("-", "_")}-1.0.dist-info'
        info.mkdir()
        (self.site / module).mkdir()
        (self.site / module / '__init__.py').write_text('# module')
        (info / 'METADATA').write_text(f'Name: {name}\nVersion: 1.0\n' +
                                     ''.join(f'Requires-Dist: {r}\n' for r in requires))
        (info / 'LICENSE').write_text('license')
        (info / 'RECORD').write_text('\n'.join(f'{p},,' for p in [
            f'{module}/__init__.py', f'{info.name}/METADATA', f'{info.name}/LICENSE', f'{info.name}/RECORD']))

    def test_removes_only_unused_providers_and_keeps_grpc_for_split(self):
        self.distribution('volcengine-python-sdk', 'volcenginesdkarkruntime')
        self.distribution('opensearch-py', 'opensearchpy', ['opensearch-protobufs'])
        self.distribution('opensearch-protobufs', 'opensearch_protobufs', ['grpcio'])
        self.distribution('grpcio', 'grpc')
        self.distribution('pymilvus', 'pymilvus', ['grpcio'])
        report = profile.remove_providers(self.runtime, self.site)
        self.assertEqual(set(report['removedDistributions']), profile.REMOVED)
        self.assertGreater(report['removedBytes'], 0)
        self.assertTrue((self.site / 'grpc/__init__.py').exists())
        self.assertFalse((self.site / 'opensearchpy').exists())
        self.assertFalse((self.site / 'volcenginesdkarkruntime').exists())

    def test_new_consumer_blocks_removal(self):
        self.distribution('opensearch-py', 'opensearchpy')
        self.distribution('new-consumer', 'consumer', ['opensearch-py'])
        with self.assertRaisesRegex(RuntimeError, 'still requires'):
            profile.remove_providers(self.runtime, self.site)
        self.assertTrue((self.site / 'opensearchpy/__init__.py').exists())

    def test_unknown_files_block_partial_removal(self):
        self.distribution('opensearch-py', 'opensearchpy')
        (self.site / 'opensearchpy/unknown.dat').write_text('keep')
        with self.assertRaisesRegex(RuntimeError, 'Unowned'):
            profile.remove_providers(self.runtime, self.site)
        self.assertTrue((self.site / 'opensearchpy/__init__.py').exists())


if __name__ == '__main__':
    unittest.main()
