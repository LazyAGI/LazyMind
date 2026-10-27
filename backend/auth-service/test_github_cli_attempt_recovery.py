"""Recovery contract for lost create responses; fake credentials, no network."""
import json
import unittest
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timezone
from types import SimpleNamespace
from unittest.mock import Mock, patch

from fastapi.testclient import TestClient

from api import cloud_oauth
from core.deps import current_user
from core.errors import AppException, ErrorCodes, raise_error
from services.github_cli_auth import GitHubCLIAuthService
import test_github_cli_session as fixtures


class AttemptRecoveryTest(unittest.TestCase):
    def setUp(self):
        self.now = 1000
        self.runner = fixtures.Runner()
        self.executor = fixtures.QueuedExecutor()
        self.connections = Mock()
        self.connections.list_connections.return_value = {'items': []}
        self.connections.save_github_cli_token.return_value = {'connection_id': 'fixture-new'}
        self.service = GitHubCLIAuthService(
            runner=self.runner, connections=self.connections, executor=self.executor,
            clock=lambda: self.now, host_credentials_enabled=False, web_authorization_enabled=True,
        )

    def start(self, attempt='fixture-attempt', owner='owner-a', **kwargs):
        return self.service.start(owner_user_id=owner, attempt_id=attempt, force_web=True, **kwargs)

    def test_lookup_recovers_lost_response_without_another_worker(self):
        created = self.start()
        recovered = self.service.get_attempt('fixture-attempt', owner_user_id='owner-a')
        self.assertEqual(recovered['session_id'], created['session_id'])
        self.assertEqual(recovered['status'], created['status'])
        remaining = (datetime.fromisoformat(recovered['expires_at']) - datetime.now(timezone.utc)).total_seconds()
        self.assertGreater(remaining, 899)
        self.assertLessEqual(remaining, 900)
        self.assertEqual(len(self.executor.pending), 1)
        self.assertEqual(self.runner.auth_calls, 0)

    def test_lookup_unknown_is_read_only_and_not_found(self):
        with self.assertRaises(AppException) as error:
            self.service.get_attempt('fixture-unknown', owner_user_id='owner-a')
        self.assertEqual(error.exception.code, ErrorCodes.GITHUB_CLI_SESSION_NOT_FOUND[1])
        self.assertEqual(self.executor.pending, [])

    def test_other_owner_cannot_read_or_cancel_original_attempt(self):
        original = self.start()
        with self.assertRaises(AppException):
            self.service.get_attempt('fixture-attempt', owner_user_id='owner-b')
        self.service.cancel_attempt('fixture-attempt', owner_user_id='owner-b')
        self.assertEqual(self.service.get(original['session_id'], owner_user_id='owner-a')['status'], 'PROBING')

    def test_cancel_unknown_prevents_late_create_from_starting_authorization(self):
        cancelled = self.service.cancel_attempt('fixture-attempt', owner_user_id='owner-a')
        self.assertEqual(cancelled['status'], 'CANCELLED')
        self.assertEqual(self.start()['status'], 'CANCELLED')
        self.assertEqual(self.executor.pending, [])
        self.connections.save_github_cli_token.assert_not_called()

    def test_cancel_known_attempt_prevents_queued_worker_from_saving(self):
        self.start()
        self.service.cancel_attempt('fixture-attempt', owner_user_id='owner-a')
        self.executor.run()
        self.assertEqual(self.runner.auth_calls, 0)
        self.connections.save_github_cli_token.assert_not_called()

    def test_completed_save_wins_over_attempt_cancellation(self):
        self.start()
        self.executor.run()
        result = self.service.cancel_attempt('fixture-attempt', owner_user_id='owner-a')
        self.assertEqual(result['status'], 'COMPLETED')
        self.assertEqual(result['connection_id'], 'fixture-new')
        self.assertNotIn(fixtures.TOKEN, json.dumps(result))
        self.assertNotIn('user_code', result)

    def test_deduplicated_attempt_alias_can_be_looked_up_and_cancelled(self):
        first = self.start('fixture-first')
        second = self.start('fixture-second')
        self.assertEqual(first['session_id'], second['session_id'])
        self.assertEqual(
            self.service.get_attempt('fixture-second', owner_user_id='owner-a')['session_id'], first['session_id'],
        )
        self.service.cancel_attempt('fixture-second', owner_user_id='owner-a')
        self.assertEqual(self.service.get_attempt('fixture-first', owner_user_id='owner-a')['status'], 'CANCELLED')
        self.assertEqual(len(self.executor.pending), 1)

    def test_expired_attempt_never_restarts_itself(self):
        self.start()
        self.now += 901
        self.assertEqual(self.service.get_attempt('fixture-attempt', owner_user_id='owner-a')['status'], 'EXPIRED')
        self.assertEqual(self.start()['status'], 'EXPIRED')
        self.executor.run()
        self.connections.save_github_cli_token.assert_not_called()

    def test_new_web_authorization_ignores_pending_manual_record(self):
        self.connections.list_connections.return_value = {'items': [
            {'connection_id': 'fixture-manual', 'status': 'PENDING', 'token_source': ''},
        ]}
        self.start()
        self.executor.run()
        self.assertEqual(self.runner.auth_calls, 1)
        self.connections.verify_saved_github_connection.assert_not_called()
        self.connections.save_github_cli_token.assert_called_once()

    def test_cancellation_records_are_bounded_and_expire(self):
        for i in range(256):
            self.service.cancel_attempt(f'fixture-{i}', owner_user_id='owner-a')
        with self.assertRaises(AppException) as error:
            self.service.cancel_attempt('fixture-over-limit', owner_user_id='owner-a')
        self.assertEqual(error.exception.code, ErrorCodes.GITHUB_CLI_BUSY[1])
        self.now += 1201
        self.assertEqual(self.start('fixture-after-retention')['status'], 'PROBING')

    def test_cancel_and_late_create_race_never_runs_a_cancelled_worker(self):
        with ThreadPoolExecutor(max_workers=2) as pool:
            start = pool.submit(self.start)
            cancel = pool.submit(self.service.cancel_attempt, 'fixture-attempt', owner_user_id='owner-a')
            start.result(timeout=2)
            self.assertEqual(cancel.result(timeout=2)['status'], 'CANCELLED')
        while self.executor.pending:
            self.executor.run()
        self.assertEqual(self.runner.auth_calls, 0)
        self.connections.save_github_cli_token.assert_not_called()

    def test_active_session_aliases_are_bounded_and_released_after_retention(self):
        first = self.start()
        for i in range(255):
            self.assertEqual(self.start(f'fixture-alias-{i}')['session_id'], first['session_id'])
        with self.assertRaises(AppException) as error:
            self.start('fixture-alias-over-limit')
        self.assertEqual(error.exception.code, ErrorCodes.GITHUB_CLI_BUSY[1])
        self.assertEqual(len(self.executor.pending), 1)
        self.assertEqual(
            self.service.get_attempt('fixture-alias-254', owner_user_id='owner-a')['session_id'], first['session_id'],
        )
        self.service.cancel_attempt('fixture-attempt', owner_user_id='owner-a')
        self.now += 1201
        with self.assertRaises(AppException) as missing:
            self.service.get_attempt('fixture-alias-254', owner_user_id='owner-a')
        self.assertEqual(missing.exception.code, ErrorCodes.GITHUB_CLI_SESSION_NOT_FOUND[1])
        self.assertEqual(self.start('fixture-new-after-aliases')['status'], 'PROBING')
        self.assertEqual(self.runner.auth_calls, 0)

    def test_attempt_recovery_rejects_empty_owner_and_invalid_ids(self):
        for fn in (self.service.get_attempt, self.service.cancel_attempt):
            for attempt, owner in [('fixture-attempt', ''), ('', 'owner-a'), ('x' * 129, 'owner-a')]:
                with self.subTest(method=fn.__name__, attempt=attempt, owner=owner), self.assertRaises(AppException):
                    fn(attempt, owner_user_id=owner)
        self.assertEqual(self.executor.pending, [])


class AttemptRecoveryAPITest(unittest.TestCase):
    def setUp(self):
        self.service = Mock()
        self.service.get_attempt.return_value = self.service.cancel_attempt.return_value = {
            'session_id': 'fixture-session', 'status': 'CANCELLED',
            'access_token': 'fixture-never-return-token',
        }
        self.patch = patch.object(cloud_oauth, 'github_cli_auth_service', self.service)
        self.patch.start()
        self.addCleanup(self.patch.stop)
        self.app = fixtures.product_app()
        old = dict(self.app.dependency_overrides)
        self.addCleanup(lambda: (self.app.dependency_overrides.clear(), self.app.dependency_overrides.update(old)))
        self.user = SimpleNamespace(id='owner-a', role=SimpleNamespace(name='system-admin'))
        self.app.dependency_overrides[current_user] = lambda: self.user
        self.client = TestClient(self.app, raise_server_exceptions=False)
        self.addCleanup(self.client.close)
        self.url = '/api/authservice/v1/cloud/github/cli/attempts/fixture-attempt'

    def test_recovery_routes_require_login(self):
        def anonymous():
            raise_error(ErrorCodes.UNAUTHORIZED)
        self.app.dependency_overrides[current_user] = anonymous
        for method in ('get', 'delete'):
            self.assertEqual(getattr(self.client, method)(self.url).status_code, 401)
        self.assertEqual(self.service.mock_calls, [])

    def test_recovery_routes_require_management_permission(self):
        self.user.role.name = 'fixture-no-permission'
        with patch('core.rbac.get_effective_permission_codes', return_value=set()):
            for method in ('get', 'delete'):
                self.assertEqual(getattr(self.client, method)(self.url).status_code, 403)
        self.assertEqual(self.service.mock_calls, [])

    def test_recovery_routes_use_authenticated_owner_and_safe_response_schema(self):
        for method, fn in [('get', self.service.get_attempt), ('delete', self.service.cancel_attempt)]:
            response = getattr(self.client, method)(self.url)
            self.assertEqual(response.status_code, 200)
            fn.assert_called_once_with('fixture-attempt', owner_user_id='owner-a')
            self.assertIn('no-store', response.headers.get('cache-control', ''))
            self.assertNotIn('fixture-never-return-token', response.text)
