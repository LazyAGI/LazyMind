"""Exercise real migrations across an abrupt process exit, without application data."""
from contextlib import closing
import os
from pathlib import Path
import sqlite3
import subprocess
import sys
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[4]
AUTH_ROOT = ROOT / 'backend' / 'auth-service'
MIGRATE = '''
import os
import sys
from alembic import command
from alembic.config import Config
from sqlalchemy import event
from sqlalchemy.engine import Engine
if sys.argv[2] == 'crash':
    @event.listens_for(Engine, 'before_cursor_execute')
    def interrupt(connection, cursor, statement, parameters, context, executemany):
        if 'ADD COLUMN scope' in statement:
            os._exit(71)
config = Config('alembic.ini')
command.upgrade(config, sys.argv[1])
'''


class MigrationAtomicityTests(unittest.TestCase):
    def test_interrupted_sqlite_upgrade_rolls_back_and_can_restart(self):
        with tempfile.TemporaryDirectory() as directory:
            database = Path(directory) / 'auth.db'
            env = dict(os.environ, LAZYMIND_DATABASE_URL=f'sqlite:///{database.as_posix()}')

            def migrate(revision, mode='normal'):
                return subprocess.run(
                    [sys.executable, '-c', MIGRATE, revision, mode],
                    cwd=AUTH_ROOT, env=env, capture_output=True, text=True, timeout=60,
                )

            initial = migrate('c9d0d3d31d10')
            self.assertEqual(initial.returncode, 0, initial.stderr)
            with closing(sqlite3.connect(database)) as connection:
                before = connection.execute('PRAGMA table_info(cloud_auth_connections)').fetchall()
            interrupted = migrate('head', 'crash')
            self.assertEqual(interrupted.returncode, 71, interrupted.stderr)
            with closing(sqlite3.connect(database)) as connection:
                self.assertEqual(connection.execute('SELECT version_num FROM alembic_version').fetchone(),
                                 ('c9d0d3d31d10',))
                self.assertEqual(connection.execute('PRAGMA table_info(cloud_auth_connections)').fetchall(), before)
            restarted = migrate('head')
            self.assertEqual(restarted.returncode, 0, restarted.stderr)
            with closing(sqlite3.connect(database)) as connection:
                columns = [row[1] for row in connection.execute('PRAGMA table_info(cloud_auth_connections)')]
                self.assertIn('owner_user_id', columns)
                self.assertIn('client_id', columns)
            repeated = migrate('head')
            self.assertEqual(repeated.returncode, 0, repeated.stderr)
