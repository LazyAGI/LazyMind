import hashlib
import importlib
import io
import logging
import os
from pathlib import Path
import plistlib
import subprocess
import tempfile
import threading
import traceback
import unittest
from unittest.mock import patch


TOKEN = 'fixture-runner-token-not-a-credential'
CODE = 'ABCD-EFGH'
GH_KEYCHAIN_READ = ['/usr/bin/security', 'find-generic-password', '-s', 'gh:github.com', '-wa', 'fixture-user']
GIT_KEYCHAIN_READ = ['/usr/bin/git', 'credential-osxkeychain', 'get']
GIT_QUERY = 'protocol=https\nhost=github.com\n\n'
GH_ABSENT = (44, '', 'fixture item not found')
GIT_ABSENT = (0, '', '')


class CredentialProbeCommands:
    def __init__(self, *, gh=GH_ABSENT, git=GIT_ABSENT):
        self.responses = {tuple(GH_KEYCHAIN_READ): gh, tuple(GIT_KEYCHAIN_READ): git}
        self.calls = []

    def __call__(self, args, **kwargs):
        args = [str(value) for value in args]
        if kwargs.get('shell') or tuple(args) not in self.responses:
            raise AssertionError('Credential probe must use an exact approved read command')
        expected_input = GIT_QUERY if args == GIT_KEYCHAIN_READ else None
        if kwargs.get('input') != expected_input:
            raise AssertionError('Credential probe input does not match the approved GitHub target')
        self.calls.append(args)
        code, stdout, stderr = self.responses[tuple(args)]
        return subprocess.CompletedProcess(args, code, stdout, stderr)


class CredentialProbeCommandsTest(unittest.TestCase):
    def test_absence_preserves_each_command_protocol(self):
        commands = CredentialProbeCommands()
        gh = commands(GH_KEYCHAIN_READ)
        git = commands(GIT_KEYCHAIN_READ, input=GIT_QUERY)
        self.assertEqual((gh.returncode, gh.stdout), (44, ''))
        # git's osxkeychain get maps errSecItemNotFound to success with no fields.
        self.assertEqual((git.returncode, git.stdout), (0, ''))

    def test_git_store_and_erase_are_rejected_even_with_valid_target_input(self):
        commands = CredentialProbeCommands(git=(0, f'password={TOKEN}\n', ''))
        for action in ['store', 'erase']:
            with self.subTest(action=action), self.assertRaises(AssertionError):
                commands(['/usr/bin/git', 'credential-osxkeychain', action], input=GIT_QUERY)
        self.assertEqual(commands.calls, [])

    def test_other_write_commands_and_extra_arguments_are_rejected(self):
        commands = CredentialProbeCommands()
        for args in [
            ['/usr/bin/security', 'add-generic-password', '-s', 'gh:github.com'],
            ['/usr/bin/security', 'delete-generic-password', '-s', 'gh:github.com'],
            ['gh', 'auth', 'login'], ['gh', 'auth', 'refresh'], ['gh', 'auth', 'logout'],
            ['/usr/bin/git', 'config', '--global', 'credential.helper', 'fixture'],
            [*GIT_KEYCHAIN_READ, 'store'],
        ]:
            with self.subTest(args=args), self.assertRaises(AssertionError):
                commands(args, input=GIT_QUERY)
        self.assertEqual(commands.calls, [])

    def test_wrong_host_account_input_or_shell_is_rejected(self):
        commands = CredentialProbeCommands()
        other_host = GH_KEYCHAIN_READ.copy()
        other_host[3] = 'gh:example.test'
        other_account = GH_KEYCHAIN_READ.copy()
        other_account[-1] = 'different-user'
        cases = [
            (other_host, {}), (other_account, {}),
            (GIT_KEYCHAIN_READ, {'input': GIT_QUERY.replace('github.com', 'example.test')}),
            (GIT_KEYCHAIN_READ, {'input': GIT_QUERY.replace('https', 'http')}),
            (GIT_KEYCHAIN_READ, {'input': GIT_QUERY + f'password={TOKEN}\n'}),
            (GIT_KEYCHAIN_READ, {}),
            (GH_KEYCHAIN_READ, {'input': TOKEN}),
            (GIT_KEYCHAIN_READ, {'input': GIT_QUERY, 'shell': True}),
        ]
        for args, kwargs in cases:
            with self.subTest(args=args, options=tuple(kwargs)), self.assertRaises(AssertionError):
                commands(args, **kwargs)
        self.assertEqual(commands.calls, [])


class FakeProcess:
    def __init__(self, code=0):
        self.stdout = io.StringIO(f'! First copy your one-time code: {CODE}\nfixture diagnostic {TOKEN}\n')
        self.returncode = code
        self.killed = False

    def poll(self):
        return self.returncode

    def wait(self, timeout=None):
        if self.returncode is None:
            raise subprocess.TimeoutExpired('fixture-gh', timeout)
        return self.returncode

    def kill(self):
        self.killed = True
        self.returncode = -9

    def terminate(self):
        self.kill()


class GitHubCLIRunnerTest(unittest.TestCase):
    def setUp(self):
        self.module = importlib.import_module('services.github_cli_runner')
        self.temp = tempfile.TemporaryDirectory(prefix='github-cli-runner-')
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.binary = self.root / 'gh'
        self.binary.write_bytes(b'fixture executable; never run by these unit tests')
        self.binary.chmod(0o700)
        self.digest = hashlib.sha256(self.binary.read_bytes()).hexdigest()
        self.calls = []
        self.login_calls = []
        self.started = False
        self.detach_fails = False
        self.mount_fails = False
        self.security_blocked = True
        self.process = FakeProcess()
        self.runner = self.module.GitHubCLIRunner(
            binary_path=self.binary, expected_sha256=self.digest, runtime_dir=self.root / 'sessions',
        )

    def run_command(self, args, **kwargs):
        args = [str(a) for a in args]
        self.calls.append((args, kwargs))
        code, stdout, stderr = 0, '', ''
        if args[-2:] == ['/usr/bin/security', 'help']:
            code = 71 if self.security_blocked else 0
            stderr = 'Operation not permitted' if self.security_blocked else ''
        elif '/usr/bin/hdiutil' == args[0] and 'attach' in args:
            stdout = '/dev/disk999\n'
        elif args == ['/usr/bin/hdiutil', 'info', '-plist']:
            stdout = plistlib.dumps({'images': [{
                'image-path': 'ram://65536', 'owner-uid': os.getuid(), 'hdid-pid': 999999,
                'system-entities': [{'dev-entry': '/dev/disk999'}],
            }]}).decode()
        elif args == ['/bin/ps', '-p', '999999', '-o', 'lstart=']:
            stdout = 'Thu Oct 22 00:00:00 2026\n'
        elif args[0] == '/sbin/mount_hfs' and self.mount_fails:
            code, stderr = 1, TOKEN
        elif args[0] == '/usr/bin/hdiutil' and 'detach' in args:
            if self.detach_fails:
                code, stderr = 1, TOKEN
        elif '--version' in args:
            stdout = 'gh version 2.102.1 (fixture)\n'
        elif 'auth' in args and 'token' in args:
            code, stdout = (0, TOKEN) if self.started else (1, '')
        else:
            self.assertIn(args[0], ['/sbin/newfs_hfs', '/sbin/mount_hfs', '/sbin/umount'])
        return subprocess.CompletedProcess(args, code, stdout, stderr)

    def start_process(self, args, **kwargs):
        self.started = True
        self.login_calls.append(([str(a) for a in args], kwargs))
        return self.process

    def authorize(self, *, on_device=lambda *args: None, event=None):
        with patch.object(self.module.subprocess, 'run', side_effect=self.run_command), \
                patch.object(self.module.subprocess, 'Popen', side_effect=self.start_process), \
                patch.object(self.module.sys, 'platform', 'darwin'):
            return self.runner.authorize(cancel_event=event or threading.Event(), on_device=on_device)

    def test_login_is_sandboxed_noninteractive_and_uses_only_private_configuration(self):
        devices = []
        original = {
            'GH_TOKEN': 'fixture-original', 'GITHUB_TOKEN': 'fixture-original', 'GH_DEBUG': 'api',
            'GH_CONFIG_DIR': str(self.root / 'original-gh'),
            'GIT_CONFIG_COUNT': '1', 'GIT_CONFIG_KEY_0': 'credential.helper',
            'GIT_CONFIG_VALUE_0': 'fixture-helper', 'PATH': '/usr/bin:/bin',
        }
        with patch.dict(os.environ, original, clear=True):
            result = self.authorize(on_device=lambda *args: devices.append(args))
            self.assertEqual(dict(os.environ), original)
        self.assertEqual(result['access_token'], TOKEN)
        self.assertEqual(devices[0][0], CODE)
        self.assertEqual(devices[0][1], 'https://github.com/login/device')
        self.assertEqual(len(self.login_calls), 1)
        args, kwargs = self.login_calls[0]
        self.assertEqual(args[:2], ['/usr/bin/sandbox-exec', '-p'])
        self.assertIn('(deny process-exec (literal "/usr/bin/security"))', args[2])
        for flag in ['--web', '--insecure-storage', '--skip-ssh-key', '--clipboard=false']:
            self.assertIn(flag, args)
        self.assertIn('github.com', args)
        self.assertIn('https', args)
        self.assertEqual(kwargs['stdin'], subprocess.DEVNULL)
        env = kwargs['env']
        for name in ['GH_TOKEN', 'GITHUB_TOKEN', 'GH_DEBUG', 'GIT_CONFIG_COUNT', 'GIT_CONFIG_KEY_0']:
            self.assertNotIn(name, env)
        self.assertEqual(env['GH_PROMPT_DISABLED'], '1')
        self.assertEqual(env['GIT_CONFIG_NOSYSTEM'], '1')
        self.assertEqual(env['GIT_TERMINAL_PROMPT'], '0')
        self.assertEqual(env['GH_BROWSER'], '/usr/bin/true')
        mount_call = next(args for args, _ in self.calls if args[0] == '/sbin/mount_hfs')
        mount_path = Path(mount_call[-1])
        self.assertTrue(Path(env['GH_CONFIG_DIR']).is_relative_to(mount_path))
        self.assertTrue(Path(env['GIT_CONFIG_GLOBAL']).is_relative_to(mount_path))
        self.assertTrue(any(args[0] == '/sbin/umount' for args, _ in self.calls))
        self.assertTrue(any(args[:2] == ['/usr/bin/hdiutil', 'detach'] for args, _ in self.calls))

    def test_integrity_mismatch_stops_before_running_any_command(self):
        self.binary.write_bytes(b'fixture tampered executable')
        with self.assertRaises(self.module.GitHubCLIError):
            self.authorize()
        self.assertEqual(self.calls, [])
        self.assertEqual(self.login_calls, [])

    def test_missing_sandbox_protection_stops_before_allocating_ram_or_logging_in(self):
        self.security_blocked = False
        with self.assertRaises(self.module.GitHubCLIError):
            self.authorize()
        self.assertEqual(self.login_calls, [])
        self.assertFalse(any('attach' in args for args, _ in self.calls))

    def test_ram_mount_failure_never_falls_back_to_disk_or_starts_login(self):
        self.mount_fails = True
        with self.assertRaises(self.module.GitHubCLIError) as error:
            self.authorize()
        self.assertNotIn(TOKEN, str(error.exception))
        self.assertEqual(self.login_calls, [])
        self.assertTrue(any('detach' in args for args, _ in self.calls))

    def test_detach_failure_does_not_return_a_token(self):
        self.detach_fails = True
        with self.assertRaises(self.module.GitHubCLIError) as error:
            self.authorize()
        self.assertNotIn(TOKEN, str(error.exception))
        self.assertNotIn(CODE, str(error.exception))
        self.assertNotIn(TOKEN, ''.join(traceback.format_exception(error.exception)))

    def test_cancelled_flow_cleans_ram_and_never_exports_a_late_token(self):
        event = threading.Event()
        def cancel(*args):
            event.set()
        with self.assertRaises(self.module.GitHubCLIError):
            self.authorize(event=event, on_device=cancel)
        self.assertTrue(any('detach' in args for args, _ in self.calls))
        after_login_exports = [args for args, _ in self.calls if 'auth' in args and 'token' in args]
        self.assertEqual(len(after_login_exports), 1)  # Only the empty-environment preflight.

    def test_failed_cli_exit_never_exports_token_and_cleans_ram(self):
        self.process = FakeProcess(code=1)
        with self.assertRaises(self.module.GitHubCLIError):
            self.authorize()
        self.assertTrue(any('detach' in args for args, _ in self.calls))
        self.assertEqual(len([args for args, _ in self.calls if 'auth' in args and 'token' in args]), 1)

    def test_cli_timeout_terminates_child_and_cleans_ram(self):
        self.process = FakeProcess(code=None)
        with self.assertRaises(self.module.GitHubCLIError) as error:
            self.authorize()
        self.assertEqual(error.exception.code, 'GITHUB_AUTH_TIMEOUT')
        self.assertTrue(self.process.killed)
        self.assertTrue(any('detach' in args for args, _ in self.calls))

    def test_no_raw_cli_output_is_printed_or_persisted(self):
        output = io.StringIO()
        logs = io.StringIO()
        handler = logging.StreamHandler(logs)
        logger = logging.getLogger()
        previous_level = logger.level
        logger.addHandler(handler)
        logger.setLevel(logging.DEBUG)
        try:
            with patch('sys.stdout', output), patch('sys.stderr', output):
                self.authorize()
        finally:
            logger.removeHandler(handler)
            logger.setLevel(previous_level)
        self.assertNotIn(TOKEN, output.getvalue())
        self.assertNotIn(CODE, output.getvalue())
        self.assertNotIn(TOKEN, logs.getvalue())
        self.assertNotIn(CODE, logs.getvalue())
        for path in self.root.rglob('*'):
            if path.is_file():
                contents = path.read_bytes()
                self.assertNotIn(TOKEN.encode(), contents)
                self.assertNotIn(CODE.encode(), contents)

    def test_keychain_access_denied_is_not_reported_as_no_credential(self):
        denied = subprocess.CompletedProcess([], 36, '', TOKEN)
        with patch.dict(os.environ, {}, clear=True), \
                patch.object(self.module.subprocess, 'run', return_value=denied), \
                self.assertRaises(self.module.GitHubCLIError) as error:
            self.runner.probe()
        self.assertEqual(error.exception.code, 'GITHUB_CREDENTIAL_UNREADABLE')
        self.assertNotIn(TOKEN, str(error.exception))

    def test_explicit_environment_token_probe_does_not_run_login_or_change_environment(self):
        with patch.dict(os.environ, {'GH_TOKEN': TOKEN}, clear=True), \
                patch.object(self.module.subprocess, 'run') as command:
            result = self.runner.probe()
            self.assertEqual(os.environ['GH_TOKEN'], TOKEN)
        self.assertEqual(result['access_token'], TOKEN)
        command.assert_not_called()

    def probe_sources(self, *, gh=GH_ABSENT, git=GIT_ABSENT):
        config = self.root / 'existing-gh'
        config.mkdir(exist_ok=True)
        (config / 'hosts.yml').write_text('github.com:\n  user: fixture-user\n  users:\n    fixture-user: {}\n')
        read = CredentialProbeCommands(gh=gh, git=git)
        before = (config / 'hosts.yml').read_bytes()
        with patch.dict(os.environ, {'HOME': str(self.root), 'GH_CONFIG_DIR': str(config)}, clear=True), \
                patch.object(self.module.subprocess, 'run', side_effect=read):
            result = self.runner.probe()
        self.assertEqual((config / 'hosts.yml').read_bytes(), before)
        return result, read.calls

    def test_existing_gh_login_is_reused_without_falling_through_to_git_or_writing(self):
        result, calls = self.probe_sources(
            gh=(0, TOKEN, ''), git=(0, 'username=fixture-user\npassword=fixture-other-git-token\n', ''),
        )
        self.assertEqual(result['access_token'], TOKEN)
        self.assertEqual(calls, [GH_KEYCHAIN_READ])

    def test_git_osxkeychain_is_reused_when_gh_is_explicitly_absent(self):
        result, calls = self.probe_sources(git=(0, f'username=fixture-user\npassword={TOKEN}\n', ''))
        self.assertEqual(result['access_token'], TOKEN)
        self.assertEqual(calls, [GH_KEYCHAIN_READ, GIT_KEYCHAIN_READ])

    def test_all_sources_explicitly_absent_is_the_only_no_credential_result(self):
        result, calls = self.probe_sources()
        self.assertIsNone(result)
        self.assertEqual(calls, [GH_KEYCHAIN_READ, GIT_KEYCHAIN_READ])

    def test_denied_or_malformed_gh_credential_does_not_silently_choose_git(self):
        for response in [(36, '', TOKEN), (0, 'go-keyring-base64:!!!!', '')]:
            with self.subTest(response=response), self.assertRaises(self.module.GitHubCLIError):
                self.probe_sources(gh=response, git=(0, f'username=fixture-user\npassword={TOKEN}\n', ''))

    def test_denied_or_malformed_git_response_is_not_no_credential(self):
        for response in [(1, '', TOKEN), (0, 'fixture malformed response without fields\n', '')]:
            with self.subTest(response=response), self.assertRaises(self.module.GitHubCLIError):
                self.probe_sources(git=response)

    def test_multiple_gh_accounts_without_active_selection_are_not_silently_chosen(self):
        config = self.root / 'existing-gh'
        config.mkdir()
        (config / 'hosts.yml').write_text('github.com:\n  users:\n    fixture-one: {}\n    fixture-two: {}\n')
        with patch.dict(os.environ, {'HOME': str(self.root), 'GH_CONFIG_DIR': str(config)}, clear=True), \
                patch.object(self.module.subprocess, 'run') as command, \
                self.assertRaises(self.module.GitHubCLIError) as error:
            self.runner.probe()
        self.assertEqual(error.exception.code, 'GITHUB_ACCOUNT_SELECTION_REQUIRED')
        command.assert_not_called()


if __name__ == '__main__':
    unittest.main()
