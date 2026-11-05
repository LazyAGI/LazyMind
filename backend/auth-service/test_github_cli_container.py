"""Container selection, account ownership and deployment contracts (no real GitHub calls)."""
import importlib
import json
import os
from pathlib import Path
import tempfile
import threading
import unittest
from unittest.mock import Mock, patch

import yaml

from core.errors import AppException, ErrorCodes
from test_github_cli_session import QueuedExecutor, Runner, TOKEN


REPO = Path(__file__).resolve().parents[2]


class GitHubCLIContainerSessionTest(unittest.TestCase):
    def setUp(self):
        self.module = importlib.import_module('services.github_cli_auth')
        self.runner = Runner()
        self.runner.probe = Mock(side_effect=AssertionError('container must never probe host identity'))
        self.executor = QueuedExecutor()
        self.executor.shutdown = Mock()
        self.connections = Mock()
        self.connections.list_connections.return_value = {'items': []}
        self.connections.save_github_cli_token.return_value = {'connection_id': 'fixture-cli', 'status': 'ACTIVE'}
        self.service = self.module.GitHubCLIAuthService(
            runner=self.runner, connections=self.connections, executor=self.executor,
            host_credentials_enabled=False, web_authorization_enabled=True,
        )
        self.addCleanup(self.service.shutdown)

    def test_container_startup_recovers_before_accepting_authorization(self):
        attempted, released = threading.Event(), threading.Event()

        def recover():
            if not released.is_set():
                attempted.set()
                raise self.module.GitHubCLIError('GITHUB_CLI_BUSY')

        self.runner.recover = Mock(side_effect=recover)
        self.service.start_recovery()
        self.assertTrue(attempted.wait(2), 'container web authorization must run startup recovery')
        with self.assertRaises(AppException) as error:
            self.service.start(owner_user_id='owner-a', attempt_id='fixture-during-recovery')
        self.assertEqual(error.exception.code, ErrorCodes.GITHUB_CLI_BUSY[1])
        self.assertEqual(self.executor.pending, [])
        self.runner.probe.assert_not_called()
        self.assertEqual(self.runner.auth_calls, 0)
        self.connections.save_github_cli_token.assert_not_called()
        released.set()
        self.assertTrue(self.service._recovery_ready.wait(3))
        self.assertGreaterEqual(self.runner.recover.call_count, 2)
        self.assertEqual(self.executor.pending, [])
        self.assertEqual(self.runner.auth_calls, 0)
        self.connections.save_github_cli_token.assert_not_called()
        pending = self.service.start(owner_user_id='owner-a', attempt_id='fixture-after-recovery')
        self.assertEqual(pending['status'], 'PROBING')
        self.assertEqual(len(self.executor.pending), 1)

    def test_new_container_connection_uses_web_and_saves_only_current_owner(self):
        waiting = []
        with patch.dict(os.environ, {'GH_TOKEN': 'fixture-server-token', 'GITHUB_TOKEN': 'fixture-server-token'}):
            pending = self.service.start(owner_user_id='owner-a', attempt_id='fixture-attempt')
            self.runner.after_device = lambda event: waiting.append(
                self.service.get(pending['session_id'], owner_user_id='owner-a'))
            self.executor.run()
        self.runner.probe.assert_not_called()
        self.assertEqual(self.runner.auth_calls, 1)
        self.assertEqual(waiting[0]['status'], 'WAITING_USER')
        saved = self.connections.save_github_cli_token.call_args.kwargs
        self.assertEqual(saved['owner_user_id'], 'owner-a')
        self.assertEqual(saved['access_token'], TOKEN)
        self.assertTrue(self.runner.cleaned)
        result = self.service.get(pending['session_id'], owner_user_id='owner-a')
        self.assertEqual(result['status'], 'COMPLETED')
        self.assertNotIn('user_code', result)
        self.assertNotIn(TOKEN, json.dumps([pending, waiting, result]))

    def test_other_owner_cannot_read_or_cancel_session(self):
        pending = self.service.start(owner_user_id='owner-a', attempt_id='fixture-attempt')
        for method in (self.service.get, self.service.cancel):
            with self.subTest(method=method.__name__), self.assertRaises(AppException):
                method(pending['session_id'], owner_user_id='owner-b')
        self.assertNotEqual(self.service.get(pending['session_id'], owner_user_id='owner-a')['status'], 'CANCELLED')

    def test_saved_connection_reuse_keeps_owner_and_skips_cli(self):
        self.connections.list_connections.return_value = {'items': [{'connection_id': 'fixture-saved'}]}
        self.connections.verify_saved_github_connection.return_value = {'connection_id': 'fixture-saved'}
        pending = self.service.start(owner_user_id='owner-a', attempt_id='fixture-reuse')
        self.executor.run()
        self.connections.verify_saved_github_connection.assert_called_once_with('fixture-saved', owner_user_id='owner-a')
        self.assertEqual(self.runner.auth_calls, 0)
        self.runner.probe.assert_not_called()
        self.assertEqual(
            self.service.get(pending['session_id'], owner_user_id='owner-a')['connection_id'], 'fixture-saved',
        )

    def test_explicit_web_skips_saved_connections_and_host_probe(self):
        self.service.start(owner_user_id='owner-a', attempt_id='fixture-web', force_web=True)
        self.executor.run()
        self.connections.list_connections.assert_not_called()
        self.runner.probe.assert_not_called()
        self.assertEqual(self.runner.auth_calls, 1)

    def test_reauthorization_preserves_target_and_never_switches_to_host_identity(self):
        self.connections.get_connection.return_value = {
            'connection_id': 'fixture-target', 'provider': 'github', 'token_source': 'github_cli', 'status': 'ACTIVE',
        }
        self.service.start(
            owner_user_id='owner-a', attempt_id='fixture-reauth', reauthorize_connection_id='fixture-target',
        )
        self.executor.run()
        self.connections.get_connection.assert_called_once_with('fixture-target', user_id='owner-a')
        self.assertEqual(
            self.connections.save_github_cli_token.call_args.kwargs['reauthorize_connection_id'], 'fixture-target',
        )
        self.runner.probe.assert_not_called()

    def test_cleanup_failure_does_not_save(self):
        self.runner.cleanup_error = self.module.GitHubCLIError('GITHUB_CLI_CLEANUP_FAILED')
        pending = self.service.start(owner_user_id='owner-a', attempt_id='fixture-cleanup')
        self.executor.run()
        self.connections.save_github_cli_token.assert_not_called()
        result = self.service.get(pending['session_id'], owner_user_id='owner-a')
        self.assertEqual(result['status'], 'FAILED')
        self.assertNotIn('user_code', result)

    def test_disabled_web_does_not_queue_authorization(self):
        service = self.module.GitHubCLIAuthService(
            runner=self.runner, connections=self.connections, executor=self.executor,
            host_credentials_enabled=False, web_authorization_enabled=False,
        )
        with self.assertRaises(AppException) as error:
            service.start(owner_user_id='owner-a', attempt_id='fixture-disabled')
        self.assertEqual(error.exception.code, ErrorCodes.GITHUB_CLI_DISABLED[1])
        self.assertEqual(self.executor.pending, [])


class GitHubCLIContainerRuntimeTest(unittest.TestCase):
    def test_factory_selects_linux_only_with_explicit_container_configuration(self):
        module = importlib.import_module('services.github_cli_runner')
        with tempfile.TemporaryDirectory() as temp:
            binary = Path(temp) / 'gh'
            binary.with_name('gh.sha256').write_text('a' * 64)
            env = {'LAZYMIND_GITHUB_CLI_CONTAINER_ENABLED': '1', 'LAZYMIND_GITHUB_CLI_PATH': str(binary)}
            with patch.dict(os.environ, env, clear=True), patch('sys.platform', 'linux'):
                runner = module.GitHubCLIRunner.from_runtime()
            self.assertEqual(type(runner).__name__, 'LinuxGitHubCLIRunner')
            self.assertEqual(runner.binary, binary)
            self.assertEqual(runner.digest, 'a' * 64)
            self.assertEqual(runner.root, Path('/run/lazymind-github-cli'))
            for platform, enabled in [('darwin', '1'), ('win32', '1'), ('linux', ''), ('linux', '0')]:
                with self.subTest(platform=platform, enabled=enabled), \
                        patch.dict(os.environ, {**env, 'LAZYMIND_GITHUB_CLI_CONTAINER_ENABLED': enabled}, clear=True), \
                        patch('sys.platform', platform):
                    self.assertNotEqual(type(module.GitHubCLIRunner.from_runtime()).__name__, 'LinuxGitHubCLIRunner')

    def test_singleton_enables_container_web_but_never_host_probe(self):
        module = importlib.import_module('services.github_cli_auth')
        try:
            for workers, expected in [('1', True), ('2', False)]:
                with self.subTest(workers=workers), patch('sys.platform', 'linux'), patch.dict(os.environ, {
                    'LAZYMIND_GITHUB_CLI_CONTAINER_ENABLED': '1', 'WEB_CONCURRENCY': workers,
                }, clear=True):
                    configured = importlib.reload(module).github_cli_auth_service
                    self.assertEqual(configured.web_authorization_enabled, expected)
                    self.assertFalse(configured.host_credentials_enabled)
        finally:
            importlib.reload(module)

    def test_compose_declares_private_memory_mount_no_swap_and_single_worker(self):
        service = yaml.safe_load((REPO / 'docker-compose.yml').read_text())['services']['auth-service']
        mounts = service.get('tmpfs') or []
        selected = [entry for entry in mounts if str(entry).split(':', 1)[0] == '/run/lazymind-github-cli']
        self.assertEqual(len(selected), 1)
        options = set(selected[0].split(':', 1)[1].split(','))
        self.assertTrue({'rw', 'noexec', 'nosuid', 'nodev', 'mode=0700'} <= options)
        self.assertTrue(any(item.startswith('size=') for item in options))
        self.assertEqual(service['mem_limit'], service['memswap_limit'])
        self.assertIn('512m', str(service['mem_limit']))
        self.assertEqual(service['ulimits']['core'], {'soft': 0, 'hard': 0})
        self.assertEqual(str(service['environment']['WEB_CONCURRENCY']), '1')
        self.assertEqual(str(service['environment']['LAZYMIND_GITHUB_CLI_CONTAINER_ENABLED']), '1')
        self.assertNotIn('--reload', service['command'])
        workers = service['command'].index('--workers')
        self.assertEqual(str(service['command'][workers + 1]), '1')
        for volume in service.get('volumes', []):
            self.assertNotIn('.config/gh', str(volume))
            self.assertNotIn('.gitconfig', str(volume))


if __name__ == '__main__':
    unittest.main()
