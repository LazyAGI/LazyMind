"""Exercise dependency boundaries and real archive contents with synthetic wheels."""
import contextlib
import importlib.util
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
import zipfile

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('components', ROOT / 'desktop/scripts/build-python-components.py')
components = importlib.util.module_from_spec(spec)
spec.loader.exec_module(components)


class Distribution:
    def __init__(self, name, requires=()):
        self.requires = requires


class DependencyGroupsTests(unittest.TestCase):
    def test_grpc_moves_to_rag_only_after_opensearch_is_removed(self):
        deps = {name: Distribution(name) for name in set.union(*components.SEEDS.values())}
        deps['pymilvus'].requires = ['grpcio>=1.66']
        deps['milvus-lite'].requires = ['grpcio>=1.50']
        deps['grpcio'] = Distribution('grpcio', ['typing-extensions'])
        deps['typing-extensions'] = Distribution('typing-extensions')
        deps['core'] = Distribution('core', ['typing-extensions'])
        deps['opensearch-py'] = Distribution('opensearch-py', ['opensearch-protobufs'])
        deps['opensearch-protobufs'] = Distribution('opensearch-protobufs', ['grpcio'])
        self.assertNotIn('grpcio', components.dependency_groups(deps, set())['rag'])
        del deps['opensearch-py'], deps['opensearch-protobufs']
        group = components.dependency_groups(deps, set())['rag']
        self.assertIn('grpcio', group)
        self.assertNotIn('typing-extensions', group)

    def test_transitive_shared_and_protected_dependencies_stay_in_core(self):
        deps = {name: Distribution(name) for name in set.union(*components.SEEDS.values())}
        deps.update({name: Distribution(name) for name in ['numpy', 'shared', 'native', 'native-child', 'core', 'core-child']})
        deps['spacy'].requires = ['numpy', 'shared', 'native', 'core-child']
        deps['core'].requires = ['core-child', 'shared']
        deps['native'].requires = ['native-child']
        groups = components.dependency_groups(deps, {'numpy'})
        self.assertIn('native-child', groups['rag'])
        for names in groups.values():
            self.assertFalse(names & {'numpy', 'shared', 'core-child'})

    def test_default_host_matches_existing_ffmpeg_modelscope_host(self):
        self.assertIn(components.DEFAULT_BASE_URL, (ROOT / 'backend/core/systemdeps/ffmpeg.go').read_text())
        self.assertTrue(components.DEFAULT_BASE_URL.endswith('/resolve/master/'))

    def test_refuses_removing_a_core_requirement(self):
        deps = {name: Distribution(name) for name in set.union(*components.SEEDS.values())}
        deps['core'] = Distribution('core', ['spacy'])
        with self.assertRaisesRegex(RuntimeError, 'required by core'):
            components.dependency_groups(deps, set())

    def test_real_record_split_preserves_console_entrypoint_and_shared_libraries(self):
        with tempfile.TemporaryDirectory() as temp:
            runtime = Path(temp) / 'runtime'
            site = runtime / 'deps/python/algorithm/lib/python3.11/site-packages'
            site.mkdir(parents=True)
            binary = site / '../../../bin/milvus-lite'
            binary.parent.mkdir(parents=True)
            binary.write_text('entrypoint stays in base')
            for name in set.union(*components.SEEDS.values()) | {'numpy', 'dashscope'}:
                module = name.replace('-', '_')
                (site / module).mkdir()
                (site / module / '__init__.py').write_text('# test module\n')
                dist = site / f'{module}-1.0.dist-info'
                dist.mkdir()
                (dist / 'METADATA').write_text(f'Metadata-Version: 2.1\nName: {name}\nVersion: 1.0\n')
                (dist / 'LICENSE').write_text('keep license')
                (dist / 'RECORD').write_text('\n'.join(f'{path},,' for path in [
                    f'{module}/__init__.py', f'{dist.name}/METADATA', f'{dist.name}/RECORD',
                    f'{dist.name}/LICENSE', '../../../bin/milvus-lite']))
            cache = site / 'spacy/__pycache__'
            cache.mkdir()
            (cache / '__init__.cpython-311.pyc').write_bytes(b'cached bytecode')
            with patch.object(components.sysconfig, 'get_path', return_value=str(site)), contextlib.redirect_stdout(io.StringIO()):
                catalog = components.split(runtime, Path(temp) / 'output', 'https://cdn.example/components')
            self.assertTrue(binary.is_file())
            self.assertTrue((site / 'numpy/__init__.py').is_file())
            self.assertFalse((site / 'spacy').exists())
            self.assertTrue((site / 'dashscope/__init__.py').is_file())
            self.assertEqual(set(catalog['components']), {'rag'})
            for ident, item in catalog['components'].items():
                archive = Path(temp) / 'output' / item['filename']
                self.assertEqual(components.digest(archive.read_bytes()), item['sha256'])
                with zipfile.ZipFile(archive) as bundle:
                    manifest = json.loads(bundle.read('bundle-manifest.json'))
                    self.assertEqual(manifest['id'], ident)
                    self.assertTrue(any(n.endswith('/LICENSE') for n in bundle.namelist()))
                    self.assertFalse(any('bin/' in n or '..' in n for n in bundle.namelist()))
                self.assertTrue(item['url'].startswith('https://cdn.example/components/'))


if __name__ == '__main__':
    unittest.main()
