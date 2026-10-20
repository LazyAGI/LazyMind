import asyncio
import importlib
import json
import os
from types import SimpleNamespace
import unittest
import uuid
from unittest.mock import patch
from urllib.parse import urlencode

from fastapi import FastAPI
from fastapi.responses import JSONResponse
from sqlalchemy import create_engine
from sqlalchemy.orm import sessionmaker
from sqlalchemy.pool import StaticPool

from api.user import router
from core.errors import AppException, error_payload_from_exception
from models import Base, Role, User


user_service_module = importlib.import_module('services.user_service')
IDENTITY_PATH = '/api/authservice/user/admin/identity/internal'
INTERNAL_TOKEN = 'test-admin-identity-internal-token'


class InternalAdminIdentityTest(unittest.TestCase):
    def setUp(self):
        self.engine = create_engine(
            'sqlite://', connect_args={'check_same_thread': False}, poolclass=StaticPool,
        )
        Base.metadata.create_all(self.engine)
        self.session = sessionmaker(bind=self.engine)
        self.admin_id = uuid.uuid4()
        with self.session() as db:
            role = Role(id=uuid.uuid4(), name='system-admin', built_in=True)
            db.add(role)
            db.add_all([
                User(id=self.admin_id, username='admin', display_name='Admin Display',
                     password_hash='synthetic-test-hash', role_id=role.id,
                     tenant_id='tenant-fixture', disabled=False, source='init'),
                User(id=uuid.uuid4(), username='another-admin',
                     password_hash='synthetic-test-hash', role_id=role.id,
                     tenant_id='tenant-fixture', disabled=False, source='admin'),
            ])
            db.commit()
        self.session_patch = patch.object(user_service_module, 'SessionLocal', self.session)
        self.session_patch.start()
        self.env_patch = patch.dict(os.environ, {'LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN': INTERNAL_TOKEN})
        self.env_patch.start()
        app = FastAPI()
        app.include_router(router, prefix='/api/authservice')

        @app.exception_handler(AppException)
        async def handle_error(_, exc):
            return JSONResponse(status_code=exc.http_code, content=error_payload_from_exception(exc))

        self.app = app

    def tearDown(self):
        self.env_patch.stop()
        self.session_patch.stop()
        self.engine.dispose()

    def lookup(self, token=INTERNAL_TOKEN, tenant_id='tenant-fixture'):
        messages = []

        async def receive():
            return {'type': 'http.request', 'body': b'', 'more_body': False}

        async def send(message):
            messages.append(message)

        scope = {
            'type': 'http', 'asgi': {'version': '3.0'}, 'http_version': '1.1',
            'method': 'GET', 'scheme': 'http', 'path': IDENTITY_PATH, 'root_path': '',
            'query_string': urlencode({'tenant_id': tenant_id}).encode(),
            'headers': [(b'x-lazymind-internal-token', token.encode())] if token is not None else [],
            'server': ('identity.test', 80), 'client': ('127.0.0.1', 1234),
        }
        asyncio.run(self.app(scope, receive, send))
        status = next(item['status'] for item in messages if item['type'] == 'http.response.start')
        body = b''.join(item.get('body', b'') for item in messages if item['type'] == 'http.response.body').decode()
        return SimpleNamespace(status_code=status, text=body, json=lambda: json.loads(body))

    def test_queries_exact_admin_and_returns_only_identity(self):
        response = self.lookup()
        self.assertEqual(response.status_code, 200, response.text)
        self.assertEqual(response.json(), {
            'user_id': str(self.admin_id), 'username': 'admin',
            'tenant_id': 'tenant-fixture', 'role': 'system-admin', 'disabled': False,
        })

    def test_reads_current_uuid_from_user_table(self):
        first = self.lookup()
        self.assertEqual(first.status_code, 200, first.text)
        replacement_id = uuid.uuid4()
        with self.session() as db:
            admin = db.query(User).filter_by(username='admin').one()
            admin.id = replacement_id
            db.commit()
        second = self.lookup()
        self.assertEqual(second.status_code, 200, second.text)
        self.assertNotEqual(first.json()['user_id'], second.json()['user_id'])
        self.assertEqual(second.json()['user_id'], str(replacement_id))

    def test_missing_admin_does_not_select_another_administrator(self):
        with self.session() as db:
            db.query(User).filter_by(username='admin').delete()
            db.commit()
        self.assertEqual(self.lookup().status_code, 404)

    def test_disabled_admin_is_rejected(self):
        with self.session() as db:
            db.query(User).filter_by(username='admin').update({'disabled': True})
            db.commit()
        self.assertEqual(self.lookup().status_code, 403)

    def test_admin_with_non_admin_role_is_rejected(self):
        with self.session() as db:
            role = Role(id=uuid.uuid4(), name='user', built_in=True)
            db.add(role)
            db.query(User).filter_by(username='admin').update({'role_id': role.id})
            db.commit()
        self.assertEqual(self.lookup().status_code, 403)

    def test_other_tenant_is_rejected(self):
        self.assertEqual(self.lookup(tenant_id='other-tenant').status_code, 403)

    def test_missing_or_wrong_internal_token_is_rejected(self):
        for token in (None, 'wrong-test-token'):
            with self.subTest(token=token):
                self.assertEqual(self.lookup(token=token).status_code, 401)


if __name__ == '__main__':
    unittest.main()
