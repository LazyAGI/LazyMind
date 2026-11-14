"""Read configured helpers through real Git, using only isolated fake credentials."""
import json
import os
from pathlib import Path
import shlex
import shutil
import subprocess
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

from services import github_cli_runner as runner_module


TOKEN = 'fixture-configured-helper-token-not-a-credential'
QUERY = 'protocol=https\nhost=github.com\n\n'


class GitHubConfiguredCredentialsTest(unittest.TestCase):
    def setUp(self):
        self.git = shutil.which('git')
        self.assertIsNotNone(self.git, 'Git is required for credential configuration regressions')
        self.temp = tempfile.TemporaryDirectory(prefix='github-git-credentials-')
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.host, self.service = self.root / 'host', self.root / 'service'
        self.host.mkdir()
        self.service.mkdir()
        self.xdg = self.host / 'custom-xdg'
        self.xdg.mkdir()
        self.config = self.host / '.gitconfig'
        self.config.write_text('')
        self.git_cwd = self.host
        self.runner = runner_module.GitHubCLIRunner(
            binary_path=self.root / 'unused-gh', expected_sha256='', runtime_dir=self.root / 'state',
        )
        self.commands = []
        self.environment = {
            'PATH': os.path.dirname(self.git) + os.pathsep + '/usr/bin:/bin',
            'TMPDIR': str(self.root),
            'HOME': str(self.service), 'USERPROFILE': str(self.host),
            'LAZYMIND_HOST_HOME': str(self.host),
            'XDG_CONFIG_HOME': str(self.service / 'config'),
            'LAZYMIND_HOST_XDG_CONFIG_HOME': str(self.xdg),
            'LAZYMIND_GITHUB_HOST_CONFIG_DIR': str(self.host / 'gh'),
            'GIT_CONFIG_NOSYSTEM': '1', 'GIT_TERMINAL_PROMPT': '1', 'GCM_INTERACTIVE': '1',
            'GIT_ASKPASS': 'fixture-askpass-must-not-run', 'SSH_ASKPASS': 'fixture-ssh-askpass-must-not-run',
        }

    @staticmethod
    def helper(fields):
        commands = '; '.join('echo ' + field for field in fields.splitlines())
        return '!f() { test $1 = get || exit 97; ' + commands + '; }; f'

    def probe(self, platform):
        original = self.runner._command

        def command(args, **kwargs):
            self.commands.append(args)
            if args[0] == '/usr/bin/security':
                return subprocess.CompletedProcess(args, 44, '', '')
            if args == ['/usr/bin/git', 'credential-osxkeychain', 'get']:
                # The old implementation must not read the real Keychain during red tests.
                return subprocess.CompletedProcess(args, 0, '', '')
            self.assertEqual(args[-2:], ['credential', 'fill'])
            self.assertEqual(kwargs['input'], QUERY)
            self.assertFalse(kwargs.get('shell'))
            self.assertEqual(kwargs['env']['GIT_TERMINAL_PROMPT'], '0')
            self.assertEqual(kwargs['env']['GCM_INTERACTIVE'], '0')
            self.assertNotIn('GIT_ASKPASS', kwargs['env'])
            self.assertNotIn('SSH_ASKPASS', kwargs['env'])
            return original(args, cwd=self.git_cwd, **kwargs)

        before = self.config.read_bytes()
        with patch.dict(os.environ, self.environment, clear=True), \
                patch.object(runner_module, 'sys', SimpleNamespace(platform=platform)), \
                patch.object(self.runner, '_command', side_effect=command):
            environment_before = dict(os.environ)
            result = self.runner.probe()
            self.assertEqual(dict(os.environ), environment_before)
        self.assertEqual(self.config.read_bytes(), before)
        return result

    def test_custom_helper_is_reused_on_mac_and_windows(self):
        self.config.write_text('[credential]\n\thelper = ' + json.dumps(self.helper(
            f'username=fixture-user\npassword={TOKEN}\n',
        )) + '\n')
        for platform in ('darwin', 'win32'):
            with self.subTest(platform=platform):
                self.assertEqual(self.probe(platform), {'access_token': TOKEN})

    def test_store_helper_reads_existing_credentials_without_rewriting_them(self):
        credentials = self.host / 'existing credentials'
        content = f'https://fixture-user:{TOKEN}@github.com\n'
        credentials.write_text(content)
        self.config.write_text('[credential]\n\thelper = ' + json.dumps(
            'store --file=' + shlex.quote(str(credentials)),
        ) + '\n')
        for platform in ('darwin', 'win32'):
            with self.subTest(platform=platform):
                self.assertEqual(self.probe(platform), {'access_token': TOKEN})
                self.assertEqual(credentials.read_text(), content)

    def test_git_resolves_named_platform_helpers_from_its_exec_path(self):
        helpers = self.root / 'git-helpers'
        helpers.mkdir()
        self.environment['GIT_EXEC_PATH'] = str(helpers)
        for name in ('osxkeychain', 'manager', 'manager-core'):
            # Exercise Git's dispatch with a fake executable, never the real credential manager.
            helper = helpers / ('git-credential-' + name)
            helper.write_text(
                '#!/bin/sh\n'
                'test "$1" = get || exit 97\n'
                f'printf "%s\\n" username=fixture-user password={TOKEN}\n',
            )
            helper.chmod(0o700)
            self.config.write_text(f'[credential]\n\thelper = {name}\n')
            for platform in ('darwin', 'win32'):
                with self.subTest(platform=platform, helper=name):
                    self.assertEqual(self.probe(platform), {'access_token': TOKEN})

    def test_multiple_helpers_can_supply_username_and_password_in_order(self):
        marker = self.root / 'unexpected-third-helper'
        self.config.write_text('[credential]\n' + '\n'.join([
            '\thelper = ' + json.dumps(self.helper('username=fixture-user')),
            '\thelper = ' + json.dumps(self.helper(f'password={TOKEN}')),
            '\thelper = ' + json.dumps(f'!touch {marker}'),
        ]) + '\n')
        for platform in ('darwin', 'win32'):
            with self.subTest(platform=platform):
                self.assertEqual(self.probe(platform), {'access_token': TOKEN})
                self.assertFalse(marker.exists())

    def test_github_specific_helper_overrides_generic_and_ignores_other_hosts(self):
        self.config.write_text(
            '[credential]\n\thelper = ' + json.dumps(self.helper('quit=1')) + '\n'
            '[credential "https://other.example"]\n\thelper = ' + json.dumps(self.helper('quit=1')) + '\n'
            '[credential "https://github.com"]\n\thelper =\n\thelper = ' + json.dumps(self.helper(
                f'username=fixture-user\npassword={TOKEN}',
            )) + '\n',
        )
        for platform in ('darwin', 'win32'):
            with self.subTest(platform=platform):
                self.assertEqual(self.probe(platform), {'access_token': TOKEN})

    def test_included_configuration_and_configured_username_are_respected(self):
        included = self.host / 'included-config'
        content = '[credential "https://github.com"]\n\tusername = fixture-user\n\thelper = ' + json.dumps(
            self.helper(f'password={TOKEN}'),
        ) + '\n'
        included.write_text(content)
        self.config.write_text('[include]\n\tpath = ' + json.dumps(str(included)) + '\n')
        for platform in ('darwin', 'win32'):
            with self.subTest(platform=platform):
                self.assertEqual(self.probe(platform), {'access_token': TOKEN})
                self.assertEqual(included.read_text(), content)

    def test_configured_username_selects_matching_stored_account(self):
        credentials = self.host / 'two-accounts'
        credentials.write_text(
            'https://other-user:fixture-other-token@github.com\n'
            f'https://fixture-user:{TOKEN}@github.com\n',
        )
        self.config.write_text(
            '[credential "https://github.com"]\n\tusername = fixture-user\n\thelper = '
            + json.dumps(f'store --file={credentials}') + '\n',
        )
        for platform in ('darwin', 'win32'):
            with self.subTest(platform=platform):
                self.assertEqual(self.probe(platform), {'access_token': TOKEN})

    def test_conditional_include_is_evaluated_in_the_git_working_directory(self):
        self.git_cwd = self.host / 'repository'
        subprocess.run(
            [self.git, 'init', '-q', str(self.git_cwd)], check=True, capture_output=True,
            env=dict(self.environment, HOME=str(self.host)),
        )
        included = self.host / 'repository-credentials'
        included.write_text('[credential]\n\thelper = ' + json.dumps(self.helper(
            f'username=fixture-user\npassword={TOKEN}',
        )) + '\n')
        self.config.write_text(
            '[includeIf "gitdir:' + str(self.git_cwd.resolve()) + '/"]\n\tpath = '
            + json.dumps(str(included)) + '\n',
        )
        for platform in ('darwin', 'win32'):
            with self.subTest(platform=platform):
                self.assertEqual(self.probe(platform), {'access_token': TOKEN})

    def test_explicit_git_global_configuration_is_preserved(self):
        config = self.host / 'selected-global-config'
        config.write_text('[credential]\n\thelper = ' + json.dumps(self.helper(
            f'username=fixture-user\npassword={TOKEN}',
        )) + '\n')
        self.environment['GIT_CONFIG_GLOBAL'] = str(config)
        for platform in ('darwin', 'win32'):
            with self.subTest(platform=platform):
                self.assertEqual(self.probe(platform), {'access_token': TOKEN})

    def test_git_reads_the_selected_system_configuration(self):
        system_config = self.root / 'isolated-system-config'
        content = '[credential]\n\thelper = ' + json.dumps(self.helper(
            f'username=fixture-user\npassword={TOKEN}',
        )) + '\n'
        system_config.write_text(content)
        self.environment.pop('GIT_CONFIG_NOSYSTEM')
        self.environment['GIT_CONFIG_SYSTEM'] = str(system_config)
        for platform in ('darwin', 'win32'):
            with self.subTest(platform=platform):
                self.assertEqual(self.probe(platform), {'access_token': TOKEN})
                self.assertEqual(system_config.read_text(), content)

    def test_host_xdg_configuration_survives_the_private_service_home(self):
        git_config = self.xdg / 'git/config'
        git_config.parent.mkdir()
        git_config.write_text('[credential]\n\thelper = ' + json.dumps(self.helper(
            f'username=fixture-user\npassword={TOKEN}',
        )) + '\n')
        for platform in ('darwin', 'win32'):
            with self.subTest(platform=platform):
                self.assertEqual(self.probe(platform), {'access_token': TOKEN})

    def test_old_runtime_without_xdg_marker_uses_host_default_not_service_config(self):
        self.environment.pop('LAZYMIND_HOST_XDG_CONFIG_HOME')
        host_config = self.host / '.config/git/config'
        host_config.parent.mkdir(parents=True)
        host_config.write_text('[credential]\n\thelper = ' + json.dumps(self.helper(
            f'username=fixture-user\npassword={TOKEN}',
        )) + '\n')
        service_config = self.service / 'config/git/config'
        service_config.parent.mkdir(parents=True)
        service_config.write_text('[credential]\n\thelper = ' + json.dumps(self.helper('quit=1')) + '\n')
        for platform in ('darwin', 'win32'):
            with self.subTest(platform=platform):
                self.assertEqual(self.probe(platform), {'access_token': TOKEN})

    def test_direct_runner_keeps_the_callers_custom_xdg_configuration(self):
        self.environment.pop('LAZYMIND_HOST_HOME')
        self.environment.pop('LAZYMIND_HOST_XDG_CONFIG_HOME')
        self.environment.update(HOME=str(self.host), XDG_CONFIG_HOME=str(self.xdg))
        config = self.xdg / 'git/config'
        config.parent.mkdir()
        config.write_text('[credential]\n\thelper = ' + json.dumps(self.helper(
            f'username=fixture-user\npassword={TOKEN}',
        )) + '\n')
        for platform in ('darwin', 'win32'):
            with self.subTest(platform=platform):
                self.assertEqual(self.probe(platform), {'access_token': TOKEN})

    def test_missing_credentials_and_partial_helpers_do_not_request_interaction(self):
        askpass = self.root / 'askpass'
        marker = self.root / 'askpass-was-run'
        askpass.write_text(f'#!/bin/sh\ntouch {marker}\n')
        askpass.chmod(0o700)
        self.environment.update(GIT_ASKPASS=str(askpass), SSH_ASKPASS=str(askpass))
        for partial in (False, True):
            self.config.write_text(
                '[core]\n\taskPass = ' + json.dumps(str(askpass)) + '\n'
                '[credential]\n\tinteractive = true\n'
                + ('\thelper = ' + json.dumps(self.helper('username=fixture-user')) + '\n' if partial else ''),
            )
            for platform in ('darwin', 'win32'):
                with self.subTest(platform=platform, partial=partial):
                    self.assertIsNone(self.probe(platform))
                    self.assertFalse(marker.exists())

    def test_helper_errors_and_explicit_quit_are_not_silent_absence(self):
        for helper in (f'!echo {TOKEN} >&2; exit 42', self.helper('quit=1')):
            self.config.write_text('[credential]\n\thelper = ' + json.dumps(helper) + '\n')
            for platform in ('darwin', 'win32'):
                with self.subTest(platform=platform, explicit_quit='quit=1' in helper):
                    with self.assertRaises(runner_module.GitHubCLIError) as error:
                        self.probe(platform)
                    self.assertEqual(error.exception.code, 'GITHUB_CREDENTIAL_UNREADABLE')
                    self.assertNotIn(TOKEN, str(error.exception))


if __name__ == '__main__':
    unittest.main()
