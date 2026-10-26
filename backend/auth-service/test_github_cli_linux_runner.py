"""Linux runner contract; fake proc metadata is not evidence of a real tmpfs mount."""
import hashlib
import importlib
import io
import os
from pathlib import Path
import subprocess
import tempfile
import threading
import traceback
import unittest
from unittest.mock import patch


TOKEN = 'fixture-linux-gh-token-not-a-credential'
CODE = 'ABCD-EFGH'


class Process:
    def __init__(self, code=0):
        self.stdout = io.StringIO(f'! First copy your one-time code: {CODE}\n')
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


class GitHubCLILinuxRunnerTest(unittest.TestCase):
    def setUp(self):
        # A missing proposed runner is an unimplemented interface, not a claimed behavioral red.
        self.module = importlib.import_module('services.github_cli_linux_runner')
        self.temp = tempfile.TemporaryDirectory(prefix='github-linux-contract-')
        self.addCleanup(self.temp.cleanup)
        self.directory = Path(self.temp.name)
        self.root = self.directory / 'sessions'
        self.root.mkdir(mode=0o700)
        self.binary = self.directory / 'gh'
        self.binary.write_bytes(b'fixture binary; never executed')
        self.binary.chmod(0o755)
        self.runner = self.module.LinuxGitHubCLIRunner(
            binary_path=self.binary,
            expected_sha256=hashlib.sha256(self.binary.read_bytes()).hexdigest(),
            runtime_dir=self.root,
        )
        self.swap = '0\n'
        self.filesystem = 'tmpfs'
        self.mount_options = 'rw,nosuid,nodev,noexec'
        self.mount_visible = True
        self.core_pattern = 'core\n'
        self.commands = []
        self.logins = []
        self.process = Process()
        self.version = 'gh version 2.102.1 (fixture)\n'
        self.unexpected_credential = False
        original_read = Path.read_text

        def read(path, *args, **kwargs):
            if str(path) == '/proc/self/mountinfo':
                if not self.mount_visible:
                    return ''
                mount = str(self.root).replace(' ', '\\040')
                return f'99 1 0:99 / {mount} {self.mount_options} - {self.filesystem} tmpfs rw,mode=700\n'
            if str(path) == '/sys/fs/cgroup/memory.swap.max':
                if self.swap is None:
                    raise FileNotFoundError('fixture missing cgroup contract')
                return self.swap
            if str(path) == '/proc/sys/kernel/core_pattern':
                if self.core_pattern is None:
                    raise FileNotFoundError('fixture missing core dump contract')
                return self.core_pattern
            return original_read(path, *args, **kwargs)

        self.read_patch = patch.object(Path, 'read_text', read)
        self.read_patch.start()
        self.addCleanup(self.read_patch.stop)
        self.core_limit = patch('resource.getrlimit', return_value=(0, 0))
        self.core_limit_mock = self.core_limit.start()
        self.addCleanup(self.core_limit.stop)

    def command(self, args, **kwargs):
        args = [str(value) for value in args]
        self.commands.append((args, kwargs))
        self.assertEqual(args[0], str(self.binary))
        if args[1:] == ['--version']:
            return subprocess.CompletedProcess(args, 0, self.version, '')
        self.assertEqual(args[1:], ['auth', 'token', '--hostname', 'github.com'])
        if self.logins or self.unexpected_credential:
            return subprocess.CompletedProcess(args, 0, TOKEN, '')
        return subprocess.CompletedProcess(args, 1, '', '')

    def login(self, args, **kwargs):
        self.logins.append(([str(value) for value in args], kwargs))
        config = Path(kwargs['env']['GH_CONFIG_DIR'])
        self.assertTrue(config.is_dir())
        self.assertTrue(config.is_relative_to(self.root))
        (config / 'hosts.yml').write_text(f'fixture: {TOKEN}\n')
        return self.process

    def authorize(self, event=None, on_device=lambda *args: None):
        with patch('sys.platform', 'linux'), \
                patch('subprocess.run', side_effect=self.command), \
                patch('subprocess.Popen', side_effect=self.login):
            return self.runner.authorize(cancel_event=event or threading.Event(), on_device=on_device)

    def assert_no_sessions(self):
        self.assertEqual(list(self.root.glob('session-*')), [])

    def test_web_login_uses_private_environment_and_cleans_before_return(self):
        inherited = {
            'GH_TOKEN': 'fixture-host-token', 'GITHUB_TOKEN': 'fixture-other-host-token',
            'GH_DEBUG': 'api', 'GH_CONFIG_DIR': str(self.directory / 'host-gh'),
            'HOME': str(self.directory / 'host-home'), 'XDG_CONFIG_HOME': '/fixture/shared-config',
            'GIT_CONFIG_COUNT': '1', 'GIT_CONFIG_KEY_0': 'credential.helper',
            'GIT_CONFIG_VALUE_0': 'fixture-helper', 'SSH_AUTH_SOCK': '/fixture/agent',
            'GIT_ASKPASS': 'fixture-askpass', 'DBUS_SESSION_BUS_ADDRESS': 'fixture-shared-bus',
            'LAZYMIND_AUTH_CLOUD_SECRET_KEY': 'fixture-service-key', 'LD_PRELOAD': '/fixture/untrusted.so',
            'PATH': '/usr/bin:/bin',
        }
        devices = []
        with patch.dict(os.environ, inherited, clear=True):
            result = self.authorize(on_device=lambda *args: devices.append(args))
            self.assertEqual(dict(os.environ), inherited)
        self.assertEqual(result['access_token'], TOKEN)
        self.assertEqual(devices[0][:2], (CODE, 'https://github.com/login/device'))
        args, options = self.logins[0]
        self.assertEqual(args[0], str(self.binary))
        for flag in ('--web', '--insecure-storage', '--skip-ssh-key', '--clipboard=false'):
            self.assertIn(flag, args)
        self.assertEqual(options['stdin'], subprocess.DEVNULL)
        self.assertTrue(options['pass_fds'], 'gh must retain the recovery lease while alive')
        env = options['env']
        for name in ('GH_TOKEN', 'GITHUB_TOKEN', 'GH_DEBUG', 'GIT_CONFIG_COUNT',
                     'GIT_CONFIG_KEY_0', 'GIT_CONFIG_VALUE_0', 'GIT_ASKPASS', 'SSH_AUTH_SOCK',
                     'LAZYMIND_AUTH_CLOUD_SECRET_KEY', 'LD_PRELOAD'):
            self.assertNotIn(name, env)
        for _, kwargs in self.commands:
            self.assertNotIn('GH_TOKEN', kwargs['env'])
            self.assertNotIn('LAZYMIND_AUTH_CLOUD_SECRET_KEY', kwargs['env'])
        for name in ('HOME', 'GH_CONFIG_DIR', 'GH_CACHE_DIR', 'XDG_CONFIG_HOME',
                     'XDG_CACHE_HOME', 'XDG_DATA_HOME', 'XDG_RUNTIME_DIR', 'GIT_CONFIG_GLOBAL', 'TMPDIR'):
            self.assertTrue(Path(env[name]).is_relative_to(self.root), name)
        self.assertEqual(env['GIT_CONFIG_NOSYSTEM'], '1')
        self.assertEqual(env['GIT_TERMINAL_PROMPT'], '0')
        self.assertNotEqual(env['DBUS_SESSION_BUS_ADDRESS'], inherited['DBUS_SESSION_BUS_ADDRESS'])
        self.assertIn(str(self.root), env['DBUS_SESSION_BUS_ADDRESS'])
        self.assert_no_sessions()
        self.assertFalse((self.directory / 'host-gh').exists())

    def test_does_not_import_environment_identity_even_if_probe_is_called(self):
        with patch.dict(os.environ, {'GH_TOKEN': TOKEN, 'GITHUB_TOKEN': TOKEN}), \
                patch('subprocess.run') as command:
            self.assertIsNone(self.runner.probe())
        command.assert_not_called()

    def test_rejects_non_tmpfs_without_login(self):
        self.filesystem = 'ext4'
        with self.assertRaises(self.module.GitHubCLIError):
            self.authorize()
        self.assertEqual(self.logins, [])
        self.assertEqual(self.commands, [])

    def test_rejects_missing_mount_without_login(self):
        self.mount_visible = False
        with self.assertRaises(self.module.GitHubCLIError):
            self.authorize()
        self.assertEqual(self.logins, [])
        self.assertEqual(self.commands, [])

    def test_rejects_each_missing_mount_protection(self):
        for missing in ('noexec', 'nosuid', 'nodev'):
            with self.subTest(missing=missing):
                self.mount_options = ','.join(x for x in ('rw', 'noexec', 'nosuid', 'nodev') if x != missing)
                with self.assertRaises(self.module.GitHubCLIError):
                    self.authorize()
                self.assertEqual(self.logins, [])
                self.assertEqual(self.commands, [])

    def test_rejects_enabled_unreadable_or_invalid_swap_limit(self):
        for value in ('max\n', '4096\n', '', 'invalid\n', None):
            with self.subTest(value=value):
                self.swap = value
                with self.assertRaises(self.module.GitHubCLIError):
                    self.authorize()
                self.assertEqual(self.logins, [])
                self.assertEqual(self.commands, [])

    def test_rejects_shared_root_permissions(self):
        self.root.chmod(0o755)
        with self.assertRaises(self.module.GitHubCLIError):
            self.authorize()
        self.assertEqual(self.logins, [])

    def test_rejects_root_owned_by_another_user(self):
        with patch('os.getuid', return_value=os.getuid() + 1), self.assertRaises(self.module.GitHubCLIError):
            self.authorize()
        self.assertEqual(self.logins, [])

    def test_rejects_enabled_core_dump_limits(self):
        for limits in ((-1, -1), (0, -1), (1, 1)):
            with self.subTest(limits=limits):
                self.core_limit_mock.return_value = limits
                with self.assertRaises(self.module.GitHubCLIError):
                    self.authorize()
                self.assertEqual(self.logins, [])
                self.assertEqual(self.commands, [])

    def test_rejects_pipe_or_unreadable_core_dump_pattern(self):
        for pattern in ('|/fixture/core-handler %p\n', None):
            with self.subTest(pattern=pattern):
                self.core_pattern = pattern
                with self.assertRaises(self.module.GitHubCLIError):
                    self.authorize()
                self.assertEqual(self.logins, [])
                self.assertEqual(self.commands, [])

    def test_rejects_symlink_root_without_modifying_target(self):
        original = self.root
        self.root = self.directory / 'linked-root'
        self.root.symlink_to(original, target_is_directory=True)
        self.runner.root = self.root
        marker = original / 'keep'
        marker.write_text('fixture unrelated content')
        with self.assertRaises(self.module.GitHubCLIError):
            self.authorize()
        self.assertEqual(marker.read_text(), 'fixture unrelated content')
        self.assertEqual(self.logins, [])

    def test_integrity_failure_runs_no_binary(self):
        self.binary.write_bytes(b'fixture tampered')
        with self.assertRaises(self.module.GitHubCLIError):
            self.authorize()
        self.assertEqual(self.commands, [])

    def test_wrong_version_never_starts_login(self):
        self.version = 'gh version 0.0.0 (fixture)\n'
        with self.assertRaises(self.module.GitHubCLIError):
            self.authorize()
        self.assertEqual(self.logins, [])

    def test_existing_credential_in_empty_context_fails_closed(self):
        self.unexpected_credential = True
        with self.assertRaises(self.module.GitHubCLIError):
            self.authorize()
        self.assertEqual(self.logins, [])
        self.assert_no_sessions()

    def test_cancel_kills_and_cleans_without_returning_token(self):
        event = threading.Event()
        self.process = Process(None)
        with self.assertRaises(self.module.GitHubCLIError):
            self.authorize(event=event, on_device=lambda *args: event.set())
        self.assertTrue(self.process.killed)
        self.assert_no_sessions()

    def test_failed_login_cleans_and_sanitizes_exception(self):
        self.process = Process(1)
        with self.assertRaises(self.module.GitHubCLIError) as error:
            self.authorize()
        self.assertNotIn(TOKEN, str(error.exception))
        self.assertNotIn(CODE, str(error.exception))
        self.assert_no_sessions()

    def test_cleanup_failure_never_returns_token(self):
        with patch('shutil.rmtree', side_effect=OSError(TOKEN)):
            with self.assertRaises(self.module.GitHubCLIError) as error:
                self.authorize()
        self.assertNotIn(TOKEN, str(error.exception))
        self.assertNotIn(TOKEN, ''.join(traceback.format_exception(error.exception)))

    def test_recovery_cleans_stale_private_directory_without_running_gh(self):
        stale = self.root / ('session-' + 'a' * 32)
        stale.mkdir(mode=0o700)
        (stale / 'hosts.yml').write_text(TOKEN)
        with patch('sys.platform', 'linux'), patch('subprocess.run') as command:
            self.runner.recover()
        command.assert_not_called()
        self.assertFalse(stale.exists())

    def test_recovery_preserves_active_lease_and_directory(self):
        import fcntl
        stale = self.root / ('session-' + 'a' * 32)
        stale.mkdir(mode=0o700)
        (stale / 'hosts.yml').write_text(TOKEN)
        fd = os.open(self.root / 'lease.lock', os.O_CREAT | os.O_RDWR, 0o600)
        try:
            fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
            with patch('sys.platform', 'linux'), self.assertRaises(self.module.GitHubCLIError) as error:
                self.runner.recover()
            self.assertEqual(error.exception.code, 'GITHUB_CLI_BUSY')
            self.assertTrue(stale.exists())
        finally:
            os.close(fd)

    def test_recovery_refuses_symlink_session(self):
        outside = self.directory / 'unrelated'
        outside.mkdir()
        marker = outside / 'keep'
        marker.write_text('untouched')
        (self.root / ('session-' + 'b' * 32)).symlink_to(outside, target_is_directory=True)
        with patch('sys.platform', 'linux'), self.assertRaises(self.module.GitHubCLIError):
            self.runner.recover()
        self.assertEqual(marker.read_text(), 'untouched')


if __name__ == '__main__':
    unittest.main()
