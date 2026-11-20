"""Real Alembic upgrade/downgrade on isolated SQLite or PostgreSQL schemas.

Use AUTH_SERVICE_TEST_DB_DRIVER=postgres and AUTH_SERVICE_TEST_POSTGRES_DSN to
exercise the same migration contract against PostgreSQL; no implicit skip.
"""
import os
import subprocess
import sys
import uuid
from pathlib import Path

import pytest
from models import CloudAuthConnection, Role, User
from sqlalchemy import inspect
from sqlalchemy.orm import Session
from test_github_star_deferred import isolated_database_engine

AUTH_ROOT = Path(__file__).resolve().parents[4] / 'backend' / 'auth-service'
BASELINE = 'b7c8d9e0f1a2'
TABLE = 'github_star_intents'
MIGRATE = '''
import sys
from alembic import command
from alembic.config import Config
config = Config('alembic.ini')
getattr(command, sys.argv[1])(config, sys.argv[2])
'''


@pytest.fixture
def migration_database(tmp_path):
    with isolated_database_engine(tmp_path) as engine:
        yield engine.url.render_as_string(hide_password=False), engine


def test_new_intent_table_round_trip_preserves_existing_users_and_connections(migration_database):
    url, engine = migration_database
    env = dict(os.environ, LAZYMIND_DATABASE_URL=url)

    def migrate(direction, revision):
        result = subprocess.run([sys.executable, '-c', MIGRATE, direction, revision], cwd=AUTH_ROOT,
                                env=env, capture_output=True, text=True, timeout=60, check=False)
        assert result.returncode == 0, result.stderr

    migrate('upgrade', BASELINE)
    assert TABLE not in inspect(engine).get_table_names()
    owner_id = uuid.uuid4()
    with Session(engine) as db:
        role = Role(name='migration-fixture-role')
        db.add(User(id=owner_id, username='migration-fixture-owner', password_hash='fixture-hash', role=role))
        db.add(CloudAuthConnection(
            connection_id='conn-before-deferred', owner_user_id=str(owner_id), provider='github',
            auth_mode='oauth_user', provider_account_id='10001', display_name='fixture-user',
            credential_ciphertext='fixture-encrypted-credential', auth_state_ciphertext='fixture-encrypted-state',
        ))
        db.commit()

    def assert_old_data():
        with Session(engine) as db:
            assert db.get(User, owner_id).username == 'migration-fixture-owner'
            connection = db.get(CloudAuthConnection, 'conn-before-deferred')
            assert connection.owner_user_id == str(owner_id)
            assert connection.credential_ciphertext == 'fixture-encrypted-credential'
            assert connection.auth_state_ciphertext == 'fixture-encrypted-state'

    migrate('upgrade', 'head')
    inspector = inspect(engine)
    assert TABLE in inspector.get_table_names(), 'new deferred Star migration is missing'
    columns = {column['name'] for column in inspector.get_columns(TABLE)}
    assert {'intent_id', 'owner_user_id', 'repository', 'status', 'created_at', 'expires_at',
            'attempt_count', 'next_attempt_at', 'rate_limit_not_before', 'credential_fingerprint'} <= columns
    unique_columns = [set(item['column_names']) for item in inspector.get_unique_constraints(TABLE)]
    unique_columns += [set(item['column_names']) for item in inspector.get_indexes(TABLE) if item['unique']]
    assert {'owner_user_id', 'repository'} in unique_columns
    assert_old_data()
    migrate('upgrade', 'head')
    assert_old_data()
    migrate('downgrade', BASELINE)
    assert TABLE not in inspect(engine).get_table_names()
    assert_old_data()
    migrate('upgrade', 'head')
    assert TABLE in inspect(engine).get_table_names()
    assert_old_data()
