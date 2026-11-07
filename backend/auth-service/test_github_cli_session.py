import importlib
import json
import threading
from types import SimpleNamespace
import unittest
from concurrent.futures import Future, ThreadPoolExecutor
from unittest.mock import Mock, patch

from fastapi.testclient import TestClient

from core.errors import AppException, ErrorCodes
from core.cloud_crypto import decrypt_json
from core.deps import current_user
from services.cloud_oauth_provider import CloudProviderError
from test_github_cli_api import product_app
from test_github_cli_connection import GitHubConnectionFixture, FAKE_TOKEN, FAKE_OLD_TOKEN


TOKEN = 'fixture-cli-session-token-not-a-credential'
CODE = 'ABCD-EFGH'


class QueuedExecutor:
    def __init__(self):
        self.pending = []

    def submit(self, fn, *args, **kwargs):
        future = Future()
        self.pending.append((future, fn, args, kwargs))
        return future

    def run(self):
        future, fn, args, kwargs = self.pending.pop(0)
        try:
            future.set_result(fn(*args, **kwargs))
        except Exception as exc:
            future.set_exception(exc)
        return future.result()


class Runner:
    def __init__(self):
        self.existing = {'access_token': TOKEN, 'scope': 'repo'}
        self.probe_error = None
        self.auth_error = None
        self.cleanup_error = None
        self.probe_calls = 0
        self.auth_calls = 0
        self.after_device = None
        self.cleaned = False

    def probe(self):
        self.probe_calls += 1
        if self.probe_error:
            raise self.probe_error
        return self.existing

    def authorize(self, *, cancel_event, on_device):
        self.auth_calls += 1
        try:
            on_device(CODE, 'https://github.com/login/device', 900)
            if self.after_device:
                self.after_device(cancel_event)
            if self.auth_error:
                raise self.auth_error
            return {'access_token': TOKEN, 'scope': 'repo'}
        finally:
            self.cleaned = True
            if self.cleanup_error:
                raise self.cleanup_error


class GitHubCLISessionTest(unittest.TestCase):
    def setUp(self):
        # This feature module is deliberately not supplied by the test candidate.
        # Until implementation, these tests report a missing interface, not a behavioral red result.
        self.module = importlib.import_module('services.github_cli_auth')
        self.runner = Runner()
        self.executor = QueuedExecutor()
        self.now = 1000.0
        self.connections = Mock()
        self.connections.list_connections.return_value = {'items': []}
        self.connections.save_github_cli_token.return_value = {
            'connection_id': 'conn-fixture', 'status': 'ACTIVE', 'provider': 'github',
        }
        self.service = self.new_service()

    def new_service(self):
        return self.module.GitHubCLIAuthService(
            runner=self.runner, connections=self.connections,
            executor=self.executor, clock=lambda: self.now,
        )

    def start(self, *, owner='owner-1', attempt='attempt-1', **kwargs):
        return self.service.start(owner_user_id=owner, attempt_id=attempt, **kwargs)

    def get(self, session_id, owner='owner-1'):
        return self.service.get(session_id, owner_user_id=owner)

    def test_reuse_completes_without_web_login_and_never_returns_token(self):
        pending = self.start()
        self.executor.run()
        result = self.get(pending['session_id'])
        self.assertEqual(result['status'], 'COMPLETED')
        self.assertEqual(result['connection_id'], 'conn-fixture')
        self.assertEqual(self.runner.auth_calls, 0)
        self.connections.save_github_cli_token.assert_called_once()
        self.assertNotIn(TOKEN, json.dumps([pending, result]))

    def test_missing_credentials_show_device_code_then_clear_it_before_completion(self):
        self.runner.existing = None
        pending = self.start()
        waiting = []
        self.runner.after_device = lambda event: waiting.append(self.get(pending['session_id']))
        self.connections.save_github_cli_token.side_effect = self.assert_clean_before_save
        self.executor.run()
        self.assertEqual(waiting[0]['status'], 'WAITING_USER')
        self.assertEqual(waiting[0]['user_code'], CODE)
        self.assertEqual(waiting[0]['verification_uri'], 'https://github.com/login/device')
        result = self.get(pending['session_id'])
        self.assertEqual(result['status'], 'COMPLETED')
        self.assertNotIn(CODE, json.dumps(result))
        self.assertNotIn(TOKEN, json.dumps(result))

    def assert_clean_before_save(self, **kwargs):
        self.assertTrue(self.runner.cleaned)
        self.assertEqual(kwargs['owner_user_id'], 'owner-1')
        self.assertEqual(kwargs['access_token'], TOKEN)
        return {'connection_id': 'conn-fixture', 'status': 'ACTIVE', 'provider': 'github'}

    def test_unreadable_invalid_or_insufficient_credentials_do_not_start_login(self):
        for error_code in ['GITHUB_CREDENTIAL_UNREADABLE', 'GITHUB_TOKEN_INVALID', 'GITHUB_SCOPE_MISSING']:
            with self.subTest(code=error_code):
                self.runner.probe_error = self.module.GitHubCLIError(error_code)
                pending = self.start(attempt=error_code)
                self.executor.run()
                result = self.get(pending['session_id'])
                self.assertEqual(result['status'], 'FAILED')
                self.assertEqual(result['error_code'], error_code)
        self.assertEqual(self.runner.auth_calls, 0)
        self.connections.save_github_cli_token.assert_not_called()

    def test_foreign_user_cannot_query_or_cancel_session(self):
        pending = self.start()
        session_id = pending['session_id']
        with self.assertRaises(AppException):
            self.get(session_id, owner='owner-2')
        with self.assertRaises(AppException):
            self.service.cancel(session_id, owner_user_id='owner-2')
        self.executor.run()
        self.assertEqual(self.get(session_id)['status'], 'COMPLETED')

    def test_empty_owner_and_foreign_reconnect_are_rejected_before_credential_probe(self):
        with self.assertRaises(AppException):
            self.start(owner='')
        self.connections.get_connection.side_effect = AppException(*ErrorCodes.FORBIDDEN)
        with self.assertRaises(AppException):
            self.start(reauthorize_connection_id='conn-foreign')
        self.assertEqual(self.executor.pending, [])
        self.assertEqual(self.runner.probe_calls, 0)

    def test_invalid_reused_token_never_triggers_new_authorization(self):
        self.connections.save_github_cli_token.side_effect = AppException(*ErrorCodes.CLOUD_TOKEN_UNAVAILABLE)
        pending = self.start()
        self.executor.run()
        self.assertEqual(self.get(pending['session_id'])['status'], 'FAILED')
        self.assertEqual(self.runner.auth_calls, 0)

    def test_cancel_before_worker_starts_has_no_credential_or_save_side_effect(self):
        pending = self.start()
        self.service.cancel(pending['session_id'], owner_user_id='owner-1')
        self.executor.run()
        self.assertEqual(self.get(pending['session_id'])['status'], 'CANCELLED')
        self.assertEqual(self.runner.probe_calls, 0)
        self.assertEqual(self.runner.auth_calls, 0)
        self.connections.save_github_cli_token.assert_not_called()

    def test_cancel_rejects_late_authorization_result(self):
        self.runner.existing = None
        pending = self.start()

        def cancel(event):
            self.service.cancel(pending['session_id'], owner_user_id='owner-1')
            self.assertTrue(event.is_set())
        self.runner.after_device = cancel
        self.executor.run()
        result = self.get(pending['session_id'])
        self.assertEqual(result['status'], 'CANCELLED')
        self.assertNotIn(CODE, json.dumps(result))
        self.connections.save_github_cli_token.assert_not_called()

    def test_expired_device_flow_cannot_save_late_token(self):
        self.runner.existing = None
        pending = self.start()

        def expire(event):
            self.now += 3600
        self.runner.after_device = expire
        self.executor.run()
        self.assertEqual(self.get(pending['session_id'])['status'], 'EXPIRED')
        self.connections.save_github_cli_token.assert_not_called()

    def test_completed_save_wins_over_late_cancel(self):
        pending = self.start()
        self.executor.run()
        result = self.service.cancel(pending['session_id'], owner_user_id='owner-1')
        self.assertEqual(result['status'], 'COMPLETED')
        self.assertEqual(result['connection_id'], 'conn-fixture')

    def test_repeated_and_concurrent_attempts_only_schedule_one_worker(self):
        with ThreadPoolExecutor(max_workers=4) as pool:
            results = list(pool.map(lambda _: self.start(), range(8)))
        self.assertEqual(len({r['session_id'] for r in results}), 1)
        self.assertEqual(len(self.executor.pending), 1)
        self.executor.run()
        repeated = self.start()
        self.assertEqual(repeated['session_id'], results[0]['session_id'])
        self.assertEqual(repeated['status'], 'COMPLETED')
        self.assertEqual(len(self.executor.pending), 0)

    def test_different_attempts_for_same_owner_and_target_share_one_active_session(self):
        first = self.start(attempt='attempt-first')
        second = self.start(attempt='attempt-second')
        self.assertEqual(first['session_id'], second['session_id'])
        self.assertEqual(len(self.executor.pending), 1)

    def test_same_attempt_identifier_is_scoped_to_owner(self):
        first = self.start(owner='owner-1')
        second = self.start(owner='owner-2')
        self.assertNotEqual(first['session_id'], second['session_id'])

    def test_unknown_session_after_restart_is_not_success_and_does_not_restart_auth(self):
        pending = self.start()
        restarted = self.new_service()
        with self.assertRaises(AppException):
            restarted.get(pending['session_id'], owner_user_id='owner-1')
        self.assertEqual(self.runner.auth_calls, 0)
        self.connections.save_github_cli_token.assert_not_called()

    def test_cleanup_failure_prevents_save_and_sanitizes_failure(self):
        self.runner.existing = None
        self.runner.cleanup_error = RuntimeError(f'fixture failure includes {TOKEN} and {CODE}')
        pending = self.start()
        self.executor.run()
        result = self.get(pending['session_id'])
        self.assertEqual(result['status'], 'FAILED')
        self.assertTrue(result['error_code'])
        self.assertNotIn(TOKEN, json.dumps(result))
        self.assertNotIn(CODE, json.dumps(result))
        self.connections.save_github_cli_token.assert_not_called()


class GitHubCLISessionStorageTest(GitHubConnectionFixture, unittest.TestCase):
    def setUp(self):
        super().setUp()
        self.module = importlib.import_module('services.github_cli_auth')
        self.runner = Runner()
        self.runner.existing = {'access_token': FAKE_TOKEN}
        self.executor = QueuedExecutor()
        self.manager = self.module.GitHubCLIAuthService(
            runner=self.runner, connections=self.service, executor=self.executor, clock=lambda: 1000.0,
        )

    def test_saved_connection_is_verified_and_reused_before_host_credentials(self):
        for source in ['manual_oauth', 'github_cli']:
            with self.subTest(source=source):
                connection_id = f'conn-saved-{source}'
                self.seed(connection_id, source=source, owner=source)
                pending = self.manager.start(owner_user_id=source, attempt_id='reuse-saved')
                self.executor.run()
                result = self.manager.get(pending['session_id'], owner_user_id=source)
                self.assertEqual(result['status'], 'COMPLETED')
                self.assertEqual(result['connection_id'], connection_id)
                self.assertEqual(self.runner.probe_calls, 0)
                self.assertEqual(self.runner.auth_calls, 0)
                state = decrypt_json(self.row(connection_id).auth_state_ciphertext)
                self.assertEqual(state['access_token'], FAKE_OLD_TOKEN)
                self.assertEqual(state.get('token_source', ''), 'github_cli' if source == 'github_cli' else '')
        self.assertEqual(self.provider.profile_calls, [FAKE_OLD_TOKEN, FAKE_OLD_TOKEN])

    def test_multiple_saved_github_identities_require_selection(self):
        self.seed('conn-first')
        self.seed('conn-second', account='10002')
        pending = self.manager.start(owner_user_id='owner-1', attempt_id='multiple-accounts')
        self.executor.run()
        result = self.manager.get(pending['session_id'], owner_user_id='owner-1')
        self.assertEqual(result['status'], 'FAILED')
        self.assertEqual(result['error_code'], 'GITHUB_ACCOUNT_SELECTION_REQUIRED')
        self.assertEqual(self.runner.probe_calls, 0)
        self.assertEqual(self.runner.auth_calls, 0)

    def test_saved_connection_verification_failure_never_falls_through_to_host_or_web_auth(self):
        for index, failure in enumerate([
            CloudProviderError('fixture network', retryable=True),
            CloudProviderError('fixture forbidden', provider_code='403'),
            CloudProviderError('fixture invalid', provider_code='401', requires_reauth=True),
        ]):
            with self.subTest(failure=failure.provider_code):
                owner = f'owner-failure-{index}'
                connection_id = f'conn-failure-{index}'
                self.seed(connection_id, owner=owner)
                self.provider.failure = failure
                pending = self.manager.start(owner_user_id=owner, attempt_id='existing-failure')
                self.executor.run()
                result = self.manager.get(pending['session_id'], owner_user_id=owner)
                self.assertEqual(result['status'], 'FAILED')
                self.assertEqual(self.runner.probe_calls, 0)
                self.assertEqual(self.runner.auth_calls, 0)
                self.assertEqual(
                    decrypt_json(self.row(connection_id).auth_state_ciphertext)['access_token'], FAKE_OLD_TOKEN,
                )
                if failure.retryable:
                    self.assertEqual(self.row(connection_id).status, 'ACTIVE')

    def test_cancel_during_real_commit_agrees_with_persisted_token_and_session_result(self):
        self.seed('conn-cli')
        pending = self.manager.start(
            owner_user_id='owner-1', attempt_id='commit-race', reauthorize_connection_id='conn-cli',
        )
        entered, release, cancelling = threading.Event(), threading.Event(), threading.Event()
        original_commit = self.sessions.class_.commit

        def commit(db):
            entered.set()
            if not release.wait(3):
                raise AssertionError('fixture did not release commit')
            return original_commit(db)

        def cancel():
            cancelling.set()
            return self.manager.cancel(pending['session_id'], owner_user_id='owner-1')
        with patch.object(self.sessions.class_, 'commit', commit), ThreadPoolExecutor(max_workers=2) as threads:
            worker = threads.submit(self.executor.run)
            try:
                self.assertTrue(entered.wait(3), 'authorization never reached the real commit boundary')
                cancellation = threads.submit(cancel)
                self.assertTrue(cancelling.wait(3))
            finally:
                release.set()
            worker.result(timeout=3)
            cancelled = cancellation.result(timeout=3)
        result = self.manager.get(pending['session_id'], owner_user_id='owner-1')
        self.assertEqual(cancelled['status'], result['status'])
        self.assertIn(result['status'], ['CANCELLED', 'COMPLETED'])
        expected = FAKE_OLD_TOKEN if result['status'] == 'CANCELLED' else FAKE_TOKEN
        self.assertEqual(decrypt_json(self.row('conn-cli').auth_state_ciphertext)['access_token'], expected)
        self.assertEqual(self.service.get_access_token('conn-cli', user_id='owner-1')['access_token'], expected)

    def test_disconnect_route_cancels_pending_authorization_and_rejects_late_result(self):
        from api import cloud_oauth as api
        self.seed('conn-cli')
        self.runner.existing = None
        pending = self.manager.start(
            owner_user_id='owner-1', attempt_id='disconnect-race', reauthorize_connection_id='conn-cli',
        )
        app = product_app()
        user = SimpleNamespace(id='owner-1', role=SimpleNamespace(name='system-admin'))

        def disconnect(event):
            with patch.object(api, 'cloud_oauth_service', self.service), \
                    patch.object(api, 'github_cli_auth_service', self.manager, create=True), \
                    patch.dict(app.dependency_overrides, {current_user: lambda: user}):
                client = TestClient(app)
                try:
                    response = client.delete('/api/authservice/v1/cloud/connections/conn-cli')
                finally:
                    client.close()
            self.assertEqual(response.status_code, 200)
            self.assertTrue(event.is_set())
        self.runner.after_device = disconnect
        self.executor.run()
        result = self.manager.get(pending['session_id'], owner_user_id='owner-1')
        self.assertEqual(result['status'], 'CANCELLED')
        self.assertEqual(self.row('conn-cli').status, 'REVOKED')
        with self.assertRaises(AppException):
            self.service.get_access_token('conn-cli', user_id='owner-1')


if __name__ == '__main__':
    unittest.main()
