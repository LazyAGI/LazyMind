"""Release compatibility probe against a real persisted Milvus Lite index."""

from contextlib import closing
import hashlib
import importlib.util
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


PROBE = Path(__file__).with_name('verify-update-format.py')
QUERY = [1.0, 0.0]
EXPECTED_ID = 2


def snapshot(root):
    return {
        path.relative_to(root).as_posix(): (
            hashlib.sha256(path.read_bytes()).hexdigest() if path.is_file() else None
        )
        for path in root.rglob('*')
    }


@unittest.skipUnless(importlib.util.find_spec('milvus_lite'), 'Milvus Lite test dependencies are not installed')
class ReleasedIndexProbeTests(unittest.TestCase):
    def setUp(self):
        from milvus_lite.engine.collection import Collection
        from milvus_lite.schema.types import CollectionSchema, DataType, FieldSchema

        temporary = tempfile.TemporaryDirectory(prefix='lazymind-index-fixture-')
        self.addCleanup(temporary.cleanup)
        self.fixture = Path(temporary.name) / 'collection'
        schema = CollectionSchema([
            FieldSchema('id', DataType.INT64, is_primary=True),
            FieldSchema('vector', DataType.FLOAT_VECTOR, dim=2),
        ])
        with closing(Collection('upgrade_fixture', str(self.fixture), schema)) as collection:
            collection.insert([
                {'id': 1, 'vector': [0.0, 1.0]},
                {'id': EXPECTED_ID, 'vector': QUERY},
            ])
            collection.flush()
            collection.create_index('vector', {'index_type': 'FLAT', 'metric_type': 'COSINE'})
            collection.load()
            self.assertEqual(collection.search([QUERY], top_k=1, anns_field='vector')[0][0]['id'], EXPECTED_ID)

        indexes = list(self.fixture.rglob('*.vector.flat.idx'))
        self.assertEqual(len(indexes), 1, 'Milvus did not persist the FLAT index fixture')
        self.index = indexes[0]
        self.assertGreater(self.index.stat().st_size, 0)
        self.assertTrue((self.fixture / 'manifest.json').is_file())
        self.assertTrue(list(self.fixture.rglob('*.parquet')))

    def run_probe(self):
        self.assertTrue(PROBE.is_file(), 'The release-time read-only index probe has not been implemented')
        return subprocess.run(
            [sys.executable, '-B', str(PROBE), '--worker', 'read-index', '--fixture', str(self.fixture)],
            capture_output=True, text=True, timeout=30,
        )

    def test_reads_persisted_index_without_modifying_old_fixture(self):
        before = snapshot(self.fixture)
        result = self.run_probe()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(result.stdout)['id'], EXPECTED_ID)
        self.assertEqual(snapshot(self.fixture), before)

    def test_query_uses_the_vectors_in_the_persisted_index(self):
        import numpy as np
        from milvus_lite.index.factory import build_index_from_spec, load_index_from_spec
        from milvus_lite.index.spec import IndexSpec
        from milvus_lite.storage.segment import Segment

        original = snapshot(self.fixture)
        segment = Segment.load(str(next(self.fixture.rglob('*.parquet'))), '_default', 'id', 'vector')
        spec = IndexSpec('vector', 'FLAT', 'COSINE', {})
        query = np.asarray([QUERY], dtype=np.float32)

        # Valid FLAT index bytes with reversed vector order make the old on-disk
        # index choose a different primary key from the unchanged Parquet data.
        build_index_from_spec(spec, segment.vectors[::-1].copy()).save(str(self.index))
        changed = snapshot(self.fixture)
        self.assertEqual(set(changed), set(original))
        self.assertEqual([name for name in changed if changed[name] != original[name]],
                         [self.index.relative_to(self.fixture).as_posix()])

        rows, _ = load_index_from_spec(spec, str(self.index), segment.vector_dim).search(query, 1)
        self.assertEqual(segment.pks[int(rows[0, 0])], 1)
        self.assertEqual(snapshot(self.fixture), changed)

        result = self.run_probe()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(result.stdout)['id'], 1)
        self.assertEqual(snapshot(self.fixture), changed)

    def test_missing_or_corrupt_index_is_rejected_without_rebuilding(self):
        for case in ('missing', 'corrupt'):
            with self.subTest(case=case):
                if case == 'missing':
                    self.index.unlink()
                else:
                    self.index.write_bytes(b'broken index')
                before = snapshot(self.fixture)
                self.assertNotEqual(self.run_probe().returncode, 0)
                self.assertEqual(snapshot(self.fixture), before)


if __name__ == '__main__':
    unittest.main()
