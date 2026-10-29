import asyncio
from concurrent.futures import ThreadPoolExecutor, TimeoutError
import importlib
import json
import os
from pathlib import Path
import plistlib
import subprocess
import sys
import tempfile
import threading
import unittest
from types import SimpleNamespace
from unittest.mock import patch

from core.cloud_crypto import decrypt_json
from core.errors import AppException
from services.cloud_oauth_provider import CloudProviderError
from services.github_cli_auth import GitHubCLIAuthService
from services.github_cli_runner import GitHubCLIError, GitHubCLIRunner
from test_github_cli_connection import FAKE_OLD_TOKEN, FAKE_TOKEN, GitHubConnectionFixture


class GitHubCLIRaceTest(GitHubConnectionFixture, unittest.TestCase):
    def test_stale_token_reader_cannot_overwrite_reconnected_token(self):
        self.seed('cli')
        reached, release = threading.Event(), threading.Event()
        decrypt = self.service._decrypt_payload

        def delayed_decrypt(value, *, field_name):
            result = decrypt(value, field_name=field_name)
            if field_name == 'auth_state' and threading.current_thread().name.startswith('stale-reader'):
                reached.set()
                if not release.wait(5):
                    raise AssertionError('reconnect did not finish')
            return result

        with patch.object(self.service, '_decrypt_payload', side_effect=delayed_decrypt):
            with ThreadPoolExecutor(max_workers=1, thread_name_prefix='stale-reader') as executor:
                pending = executor.submit(self.service.get_access_token, 'cli', user_id='owner-1')
                try:
                    self.assertTrue(reached.wait(5))
                    self.save(reauthorize_connection_id='cli')
                finally:
                    release.set()
                pending.result(timeout=5)
        self.assertEqual(decrypt_json(self.row('cli').auth_state_ciphertext)['access_token'], FAKE_TOKEN)
        self.assertEqual(self.service.get_access_token('cli', user_id='owner-1')['access_token'], FAKE_TOKEN)

    def test_late_invalid_token_result_cannot_expire_new_token(self):
        self.seed('cli')
        reached, release = threading.Event(), threading.Event()
        verify = self.provider.verify_cli_access_token

        def delayed_verify(*, access_token):
            if access_token == FAKE_OLD_TOKEN:
                reached.set()
                if not release.wait(5):
                    raise AssertionError('reconnect did not finish')
                raise CloudProviderError('fixture 401', requires_reauth=True)
            return verify(access_token=access_token)

        with patch.object(self.provider, 'verify_cli_access_token', side_effect=delayed_verify):
            with ThreadPoolExecutor(max_workers=1) as executor:
                pending = executor.submit(self.service.verify_saved_github_connection, 'cli', owner_user_id='owner-1')
                try:
                    self.assertTrue(reached.wait(5))
                    self.save(reauthorize_connection_id='cli')
                finally:
                    release.set()
                with self.assertRaises(AppException):
                    pending.result(timeout=5)
        self.assertEqual(self.row('cli').status, 'ACTIVE')
        self.assertEqual(self.service.get_access_token('cli', user_id='owner-1')['access_token'], FAKE_TOKEN)

    def test_health_check_does_not_rewrite_cli_credentials(self):
        self.seed('cli')
        original = self.row('cli').auth_state_ciphertext
        with patch.object(self.service, '_refresh_oauth_user_token', side_effect=AssertionError('OAuth refresh')):
            self.assertEqual(self.service._refresh_connection_for_health_check('cli'), ('ACTIVE', False))
        self.assertEqual(self.row('cli').auth_state_ciphertext, original)

    def test_invalidation_and_disconnect_preserve_revoked_state(self):
        self.seed('cli')
        self.provider.failure = CloudProviderError('fixture 401', requires_reauth=True)
        reached, release, deleting = threading.Event(), threading.Event(), threading.Event()
        identify = self.service._is_github_cli_connection
        calls = 0

        def delayed_identify(row):
            nonlocal calls
            result = identify(row)
            if threading.current_thread().name.startswith('invalidator'):
                calls += 1
                if calls == 2:
                    reached.set()
                    if not release.wait(5):
                        raise AssertionError('disconnect was not attempted')
            return result

        def disconnect():
            deleting.set()
            return self.service.delete_connection('cli', user_id='owner-1')

        with patch.object(self.service, '_is_github_cli_connection', side_effect=delayed_identify):
            with ThreadPoolExecutor(max_workers=1, thread_name_prefix='invalidator') as invalidator:
                with ThreadPoolExecutor(max_workers=1) as deleter:
                    pending = invalidator.submit(self.service.verify_saved_github_connection,
                                                 'cli', owner_user_id='owner-1')
                    try:
                        self.assertTrue(reached.wait(5))
                        deletion = deleter.submit(disconnect)
                        self.assertTrue(deleting.wait(5))
                        try:
                            deletion.result(timeout=0.3)
                        except TimeoutError:
                            pass  # Correct serialization waits for the invalidator's lock.
                    finally:
                        release.set()
                    with self.assertRaises(AppException):
                        pending.result(timeout=5)
                    deletion.result(timeout=5)
        self.assertEqual(self.row('cli').status, 'REVOKED')
        self.assertFalse(decrypt_json(self.row('cli').auth_state_ciphertext)['access_token'])


class GitHubCLILifecycleTest(unittest.TestCase):
    def test_startup_retries_live_child_then_recovers_without_login(self):
        attempted, released = threading.Event(), threading.Event()

        def recover():
            attempted.set()
            if not released.is_set():
                raise GitHubCLIError('GITHUB_CLI_BUSY')

        manager = GitHubCLIAuthService(runner=SimpleNamespace(recover=recover), connections=None)
        try:
            manager.start_recovery()
            self.assertTrue(attempted.wait(2))
            with self.assertRaises(AppException):
                manager.start(owner_user_id='owner-1', attempt_id='blocked-until-clean')
            released.set()
            self.assertTrue(manager._recovery_ready.wait(3))
            self.assertEqual(manager._sessions, {})
        finally:
            manager.shutdown()

    def test_default_service_loads_cli_from_packaged_resources(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            binary = root / 'resources/bin/gh'
            binary.parent.mkdir(parents=True)
            binary.write_text('fixture executable')
            binary.with_name('gh.sha256').write_text('a' * 64)
            env = dict(os.environ, LAZYMIND_RUNTIME_ROOT=str(root / 'data'),
                       LAZYMIND_RUNTIME_RESOURCES_ROOT=str(root / 'resources'))
            env.pop('LAZYMIND_GITHUB_CLI_PATH', None)
            source = (
                'from services.github_cli_auth import github_cli_auth_service as service; '
                'import sys; '
                'assert str(service.runner.binary) == sys.argv[1]; '
                'assert service.runner.digest == "a" * 64'
            )
            result = subprocess.run([sys.executable, '-c', source, str(binary)], env=env,
                                    capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)

    def test_application_startup_runs_recovery_without_login(self):
        with patch.dict(os.environ, {'LAZYMIND_AUTH_OPENAPI_EXPORT_ENABLED': '0'}):
            main = importlib.import_module('main')
        with patch.object(main.github_cli_auth_service, 'start_recovery', create=True) as recover:
            with patch.object(main.cloud_oauth_health, 'start'):
                asyncio.run(main._start_cloud_oauth_health_check())
        recover.assert_called_once_with()


class GitHubCLIRecoveryTest(unittest.TestCase):
    def test_unmounted_owned_image_is_detached_and_reused_identity_is_rejected(self):
        for mismatch in ('', 'pid', 'started_at', 'owner'):
            with self.subTest(mismatch=mismatch), tempfile.TemporaryDirectory() as temp:
                root = Path(temp)
                directory = root / ('session-' + 'a' * 32)
                directory.mkdir(mode=0o700)
                mount = directory / 'ram'
                mount.mkdir()
                identity = {'pid': 999999, 'started_at': 'Thu Oct 22 00:00:00 2026'}
                if mismatch == 'pid':
                    identity['pid'] += 1
                if mismatch == 'started_at':
                    identity['started_at'] = 'Wed Oct 21 00:00:00 2026'
                lease = directory / 'lease.json'
                lease.write_text(json.dumps({'device': '/dev/disk999', 'volume': 'LMgh-' + 'a' * 16,
                                             'identity': identity}))
                lease.chmod(0o600)
                image = {'image-path': 'ram://65536', 'owner-uid': os.getuid() + (mismatch == 'owner'),
                         'hdid-pid': 999999, 'system-entities': [{'dev-entry': '/dev/disk999'}]}
                commands = []

                def command(args, commands=commands, image=image, **kwargs):
                    commands.append(args)
                    if args == ['/usr/bin/hdiutil', 'info', '-plist']:
                        output = plistlib.dumps({'images': [image]}).decode()
                    elif args == ['/bin/ps', '-p', '999999', '-o', 'lstart=']:
                        output = 'Thu Oct 22 00:00:00 2026\n'
                    elif args == ['/usr/bin/hdiutil', 'detach', '/dev/disk999']:
                        output = ''
                    else:
                        raise AssertionError(args)
                    return SimpleNamespace(returncode=0, stdout=output)

                runner = GitHubCLIRunner(binary_path=root / 'gh', expected_sha256='', runtime_dir=root)
                with patch.object(runner, '_command', side_effect=command):
                    if mismatch:
                        with self.assertRaises(GitHubCLIError) as raised:
                            runner.recover()
                        self.assertEqual(raised.exception.code, 'GITHUB_CLI_RECOVERY_REQUIRED')
                        self.assertTrue(lease.exists())
                        self.assertFalse(any('detach' in args for args in commands))
                    else:
                        runner.recover()
                        self.assertIn(['/usr/bin/hdiutil', 'detach', '/dev/disk999'], commands)
                        self.assertFalse(directory.exists())
