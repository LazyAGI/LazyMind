import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

from services.github_cli_runner import GitHubCLIRunner


class GitHubCLIDesktopContextTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.host = self.root / 'host'
        self.service = self.root / 'service'
        self.host.mkdir()
        self.service.mkdir()
        self.config = self.host / 'custom-config/gh'
        self.config.mkdir(parents=True)
        (self.config / 'hosts.yml').write_text('github.com:\n  user: fixture-user\n')
        self.patch = patch.dict(os.environ, {
            'HOME': str(self.service), 'XDG_CONFIG_HOME': str(self.service / 'config'),
            'LAZYMIND_HOST_HOME': str(self.host), 'LAZYMIND_GITHUB_HOST_CONFIG_DIR': str(self.config),
        }, clear=True)
        self.patch.start()
        self.addCleanup(self.patch.stop)
        self.runner = GitHubCLIRunner(binary_path=self.root / 'gh', expected_sha256='', runtime_dir=self.root / 'state')

    def test_selected_host_gh_account_is_read_without_changing_service_home(self):
        def run(args, **kwargs):
            self.assertEqual(args, [
                '/usr/bin/security', 'find-generic-password', '-s', 'gh:github.com', '-wa', 'fixture-user',
            ])
            self.assertEqual(kwargs.get('env', {}).get('HOME'), str(self.host))
            return subprocess.CompletedProcess(args, 0, 'fixture-gh-token', '')
        with patch.object(self.runner, '_command', side_effect=run):
            self.assertEqual(self.runner.probe(), {'access_token': 'fixture-gh-token'})
        self.assertEqual(os.environ['HOME'], str(self.service))
        self.assertEqual(os.environ['XDG_CONFIG_HOME'], str(self.service / 'config'))

    def test_git_keychain_fallback_uses_host_home_and_only_the_github_get_protocol(self):
        commands = []

        def run(args, **kwargs):
            self.assertEqual(kwargs.get('env', {}).get('HOME'), str(self.host))
            commands.append(args)
            if args[0] == '/usr/bin/security':
                return subprocess.CompletedProcess(args, 44, '', '')
            self.assertEqual(args, ['/usr/bin/git', 'credential-osxkeychain', 'get'])
            self.assertEqual(kwargs['input'], 'protocol=https\nhost=github.com\n\n')
            return subprocess.CompletedProcess(args, 0, 'password=fixture-git-token\n', '')
        with patch.object(self.runner, '_command', side_effect=run):
            self.assertEqual(self.runner.probe(), {'access_token': 'fixture-git-token'})
        self.assertEqual(len(commands), 2)

    def test_host_default_config_is_used_when_the_runtime_has_no_config_marker(self):
        os.environ.pop('LAZYMIND_GITHUB_HOST_CONFIG_DIR')
        default = self.host / '.config/gh'
        default.mkdir(parents=True)
        (default / 'hosts.yml').write_text('github.com:\n  user: fixture-user\n  oauth_token: fixture-file-token\n')
        with patch.object(self.runner, '_command', side_effect=AssertionError('unexpected credential command')):
            self.assertEqual(self.runner.probe(), {'access_token': 'fixture-file-token'})

    def test_new_authorization_keeps_service_home_and_private_ram_configuration(self):
        ram = self.root / 'ram/gh'
        env = self.runner._environment(ram)
        self.assertEqual(env['HOME'], str(self.service))
        self.assertEqual(env['GH_CONFIG_DIR'], str(ram))
        self.assertNotEqual(env['GH_CONFIG_DIR'], str(self.config))
