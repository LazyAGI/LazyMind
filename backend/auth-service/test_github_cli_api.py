from types import SimpleNamespace
import importlib
import io
import logging
import os
import unittest
from unittest.mock import Mock, patch

from fastapi.testclient import TestClient

from api import cloud_oauth
from core.deps import current_user
from core.errors import ErrorCodes, raise_error


BASE = '/api/authservice/v1/cloud/github/cli/sessions'
ATTEMPT = '11111111-1111-4111-8111-111111111111'


def product_app():
    # main exports files at import time unless explicitly disabled. No lifespan is started.
    with patch.dict(os.environ, {'LAZYMIND_AUTH_OPENAPI_EXPORT_ENABLED': '0'}):
        return importlib.import_module('main').app


class GitHubCLIAPITest(unittest.TestCase):
    def setUp(self):
        self.service = Mock()
        snapshot = {
            'session_id': 'session-fixture', 'status': 'WAITING_USER',
            'verification_uri': 'https://github.com/login/device', 'user_code': 'ABCD-EFGH',
            'expires_at': '2026-10-22T12:00:00Z',
        }
        self.service.start.return_value = snapshot
        self.service.get.return_value = snapshot
        self.service.cancel.return_value = {'session_id': 'session-fixture', 'status': 'CANCELLED'}
        self.service_patch = patch.object(cloud_oauth, 'github_cli_auth_service', self.service, create=True)
        self.service_patch.start()
        self.addCleanup(self.service_patch.stop)
        self.app = product_app()
        previous = dict(self.app.dependency_overrides)
        def restore_overrides():
            self.app.dependency_overrides.clear()
            self.app.dependency_overrides.update(previous)
        self.addCleanup(restore_overrides)
        self.user = SimpleNamespace(id='owner-1', role=SimpleNamespace(name='system-admin'))
        self.app.dependency_overrides[current_user] = lambda: self.user
        self.client = TestClient(self.app, raise_server_exceptions=False)
        self.addCleanup(self.client.close)

    def test_start_uses_authenticated_owner_not_body_owner(self):
        response = self.client.post(BASE, json={'attempt_id': ATTEMPT, 'owner_user_id': 'owner-2'})
        self.assertEqual(response.status_code, 200)
        self.assertEqual(self.service.start.call_args.kwargs['owner_user_id'], 'owner-1')
        self.assertEqual(self.service.start.call_args.kwargs['attempt_id'], ATTEMPT)
        self.assertIn('no-store', response.headers.get('cache-control', ''))

    def test_poll_and_cancel_pass_authenticated_owner_and_disable_caching(self):
        for method, function in [('get', self.service.get), ('delete', self.service.cancel)]:
            with self.subTest(method=method):
                response = getattr(self.client, method)(BASE + '/session-fixture')
                self.assertEqual(response.status_code, 200)
                self.assertEqual(function.call_args.kwargs['owner_user_id'], 'owner-1')
                self.assertIn('no-store', response.headers.get('cache-control', ''))

    def test_session_endpoints_require_login(self):
        def anonymous():
            raise_error(ErrorCodes.UNAUTHORIZED)
        self.app.dependency_overrides[current_user] = anonymous
        responses = [
            self.client.post(BASE, json={'attempt_id': ATTEMPT}),
            self.client.get(BASE + '/session-fixture'),
            self.client.delete(BASE + '/session-fixture'),
        ]
        self.assertEqual([response.status_code for response in responses], [401, 401, 401])
        self.assertEqual(self.service.mock_calls, [])

    def test_session_endpoints_require_connection_management_permission(self):
        self.user.role.name = 'fixture-no-permission'
        with patch('core.rbac.get_effective_permission_codes', return_value=set()):
            responses = [
                self.client.post(BASE, json={'attempt_id': ATTEMPT}),
                self.client.get(BASE + '/session-fixture'),
                self.client.delete(BASE + '/session-fixture'),
            ]
        self.assertEqual([response.status_code for response in responses], [403, 403, 403])
        self.assertEqual(self.service.mock_calls, [])

    def test_invalid_attempt_identifiers_are_rejected_before_start(self):
        for body in [{}, {'attempt_id': ''}, {'attempt_id': 'x' * 513}]:
            with self.subTest(body=body):
                response = self.client.post(BASE, json=body)
                self.assertIn(response.status_code, [400, 422])
        self.service.start.assert_not_called()

    def test_response_schema_excludes_access_and_refresh_tokens(self):
        self.service.get.return_value = {
            'session_id': 'session-fixture', 'status': 'COMPLETED', 'connection_id': 'conn-fixture',
            'access_token': 'fixture-private-access', 'refresh_token': 'fixture-private-refresh',
        }
        response = self.client.get(BASE + '/session-fixture')
        self.assertEqual(response.status_code, 200)
        self.assertNotIn('fixture-private-access', response.text)
        self.assertNotIn('fixture-private-refresh', response.text)

    def test_existing_manual_oauth_routes_remain_available(self):
        expected = {
            ('POST', '/v1/cloud/{provider}/oauth/authorize-url'),
            ('POST', '/v1/cloud/{provider}/oauth/callback'),
            ('GET', '/v1/cloud/{provider}/oauth/app-credentials'),
            ('PUT', '/v1/cloud/{provider}/oauth/app-credentials'),
        }
        actual = {(method, route.path) for route in cloud_oauth.router.routes for method in route.methods}
        self.assertTrue(expected.issubset(actual))

    def test_failed_start_poll_and_cancel_do_not_leak_upstream_secrets_through_product_errors(self):
        token, code = 'fixture-private-token-error', 'ABCD-EFGH'
        for method, path, service_method in [
            ('post', BASE, self.service.start),
            ('get', BASE + '/session-fixture', self.service.get),
            ('delete', BASE + '/session-fixture', self.service.cancel),
        ]:
            with self.subTest(method=method):
                error = RuntimeError('fixture service failure')
                error.__cause__ = ValueError(f'{token} {code}')
                service_method.side_effect = error
                output = io.StringIO()
                with self.assertLogs(level=logging.DEBUG) as logs, \
                        patch('sys.stdout', output), patch('sys.stderr', output):
                    kwargs = {'json': {'attempt_id': ATTEMPT}} if method == 'post' else {}
                    response = getattr(self.client, method)(path, **kwargs)
                self.assertGreaterEqual(response.status_code, 400)
                self.assertNotEqual(response.status_code, 404)  # Must exercise the implemented failure path.
                body = response.json()
                self.assertIsInstance(body['code'], int)
                combined = response.text + output.getvalue() + '\n'.join(logs.output)
                self.assertNotIn(token, combined)
                self.assertNotIn(code, combined)
                service_method.side_effect = None


if __name__ == '__main__':
    unittest.main()
