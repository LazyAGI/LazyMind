from pathlib import Path
from contextlib import closing, contextmanager
import json
import runpy
import sqlite3
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch


ROOT = Path(__file__).resolve().parents[2]
FIXTURES = ROOT / 'local/local-runtime-manager/testdata/update-format'
OWNER = runpy.run_path(str(FIXTURES / 'producer.py'))
GENERATOR = runpy.run_path(str(ROOT / 'desktop/scripts/generate-update-format.py'))


class UpdateFormatLazyLLMLifecycleTest(unittest.TestCase):
    def test_complete_service_owner_matches_capture_and_supported_recipe(self):
        with tempfile.TemporaryDirectory(prefix='lazymind-service-format-') as directory:
            filename = Path(directory) / 'lazyllm.db'
            subprocess.run([sys.executable, '-I', '-B', str(FIXTURES / 'lazyllm_lifecycle.py'),
                            str(ROOT), str(filename)], check=True, capture_output=True,
                           text=True, timeout=60)
            captured = json.loads((FIXTURES / 'lazyllm-service.json').read_text())['shape']
            self.assertEqual(OWNER['snapshot'](filename, 'lazyllm'), captured)
            recipe = json.loads((ROOT / 'local/local-runtime-manager/update-format-v2.json').read_text())
            with closing(sqlite3.connect(filename)) as db, db:
                service_sha = GENERATOR['shape_hash'](db)[0]
            with closing(sqlite3.connect(':memory:')) as db, db:
                empty_sha = GENERATOR['shape_hash'](db)[0]
            self.assertIn(service_sha, recipe.get('lazyllmVariants', []))
            self.assertIn(empty_sha, recipe.get('lazyllmVariants', []))


@contextmanager
def retained_connections():
    connect = sqlite3.connect
    connections = []

    def retain(*args, **kwargs):
        db = connect(*args, **kwargs)
        connections.append(db)
        return db

    with patch.object(sqlite3, 'connect', side_effect=retain):
        try:
            yield connections
        finally:
            for db in connections:
                db.close()


class UpdateFormatSQLiteConnectionTest(unittest.TestCase):
    def assert_connections_closed(self, connections):
        self.assertTrue(connections)
        for db in connections:
            with self.assertRaises(sqlite3.ProgrammingError):
                db.execute('SELECT 1')

    def test_snapshot_closes_connections_on_success_and_error(self):
        for family in ('segments', 'core'):
            with self.subTest(family=family), tempfile.TemporaryDirectory() as directory:
                filename = Path(directory) / 'snapshot.db'
                with closing(sqlite3.connect(filename)) as db, db:
                    db.execute('CREATE TABLE sample (id INTEGER)')
                with retained_connections() as connections:
                    if family == 'core':
                        with self.assertRaisesRegex(sqlite3.OperationalError, 'no such table: schema_migrations'):
                            OWNER['snapshot'](filename, family)
                    else:
                        shape = OWNER['snapshot'](filename, family)
                        self.assertEqual(shape['schema'][0]['name'], 'sample')
                    self.assert_connections_closed(connections)

    def test_lifecycle_closes_connections_on_success_and_hash_errors(self):
        captured = json.loads((FIXTURES / 'lazyllm-service.json').read_text())['shape']
        connect = sqlite3.connect
        shape_hash = GENERATOR['shape_hash']

        def produce(arguments, **_kwargs):
            # Isolate the subprocess boundary; hashing and inspection use real SQLite.
            with closing(connect(arguments[-1])) as db, db:
                for entry in sorted(captured['schema'], key=lambda entry: entry['type'] != 'table'):
                    db.execute(entry['sql'])

        for fail_on in (None, 1, 2):
            with self.subTest(fail_on=fail_on):
                calls = 0

                def hash_or_fail(db, fail_on=fail_on):
                    nonlocal calls
                    result = shape_hash(db)
                    calls += 1
                    if calls == fail_on:
                        raise RuntimeError('shape inspection failed')
                    return result

                with patch.object(subprocess, 'run', side_effect=produce), \
                        patch.dict(GENERATOR, shape_hash=hash_or_fail), retained_connections() as connections:
                    case = UpdateFormatLazyLLMLifecycleTest()
                    if fail_on is None:
                        case.test_complete_service_owner_matches_capture_and_supported_recipe()
                    else:
                        with self.assertRaisesRegex(RuntimeError, 'shape inspection failed'):
                            case.test_complete_service_owner_matches_capture_and_supported_recipe()
                    self.assert_connections_closed(connections)


if __name__ == '__main__':
    unittest.main()
