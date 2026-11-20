"""
Pytest fixtures for auth-service tests.
Sets env before any app import so DB uses a local SQLite test database.
"""
import os
import sys
from datetime import datetime, timezone
from pathlib import Path

# Must set env before importing app
_test_dir = os.path.dirname(os.path.abspath(__file__))
_db_path = Path(_test_dir) / '.auth_service_test.sqlite3'
os.environ['LAZYMIND_DATABASE_URL'] = f'sqlite:///{_db_path}'
os.environ['LAZYMIND_JWT_SECRET'] = 'test-secret-7f4b0f9a2c6d8e1b3a5c7d9f'
os.environ['LAZYMIND_JWT_TTL_MINUTES'] = '60'
os.environ['LAZYMIND_JWT_REFRESH_TTL_DAYS'] = '7'
os.environ['LAZYMIND_AUTH_API_PERMISSIONS_FILE'] = os.path.join(_test_dir, 'api_permissions_test.json')
os.environ['LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN'] = 'test-internal-token'

# Add auth-service to path (run from project root: pytest tests/backend/auth-service/)
_root = os.path.dirname(os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__)))))
_auth_svc = os.path.join(_root, 'backend', 'auth-service')
if _auth_svc not in sys.path:
    sys.path.insert(0, _auth_svc)

import pytest
from sqlalchemy import event
from fastapi.testclient import TestClient

from main import app
from core.database import engine


@event.listens_for(engine, 'connect')
def _sqlite_register_now(dbapi_connection, _):
    dbapi_connection.create_function(
        'now',
        0,
        lambda: datetime.now(timezone.utc).strftime('%Y-%m-%d %H:%M:%S'),
    )


@pytest.fixture
def client():
    engine.dispose()
    if _db_path.exists():
        _db_path.unlink()
    with TestClient(app) as test_client:
        yield test_client
