import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from services import github_cli_runner


class GitHubCLIRuntimePathsTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        env = patch.dict(os.environ, {}, clear=True)
        env.start()
        self.addCleanup(env.stop)

    def metadata(self, binary):
        binary.parent.mkdir(parents=True, exist_ok=True)
        binary.with_name('gh.sha256').write_text('fixture-digest\n')

    def test_docker_layout_initializes_without_optional_cli_configuration(self):
        with patch.object(github_cli_runner, '__file__', '/app/services/github_cli_runner.py'):
            with patch.object(github_cli_runner.GitHubCLIRunner, '_command') as command:
                runner = github_cli_runner.GitHubCLIRunner.from_runtime()
        self.assertTrue(runner.binary.is_absolute())
        self.assertEqual(runner.root, Path.home() / '.lazymind/auth-state/github-cli')
        command.assert_not_called()

    def test_explicit_binary_works_in_shallow_layout_without_runtime_root(self):
        binary = self.root / 'custom/gh'
        self.metadata(binary)
        os.environ['LAZYMIND_GITHUB_CLI_PATH'] = str(binary)
        with patch.object(github_cli_runner, '__file__', '/app/services/github_cli_runner.py'):
            runner = github_cli_runner.GitHubCLIRunner.from_runtime()
        self.assertEqual(runner.binary, binary)
        self.assertEqual(runner.digest, 'fixture-digest')

    def test_explicit_runtime_root_preserves_bundled_binary_location(self):
        runtime = self.root / 'resources'
        self.metadata(runtime / 'bin/gh')
        os.environ['LAZYMIND_RUNTIME_RESOURCES_ROOT'] = str(runtime)
        with patch.object(github_cli_runner, '__file__', '/app/services/github_cli_runner.py'):
            runner = github_cli_runner.GitHubCLIRunner.from_runtime()
        self.assertEqual(runner.binary, runtime / 'bin/gh')
        self.assertEqual(runner.digest, 'fixture-digest')

    def test_packaged_layout_preserves_automatic_runtime_location(self):
        runtime = self.root / 'runtime'
        self.metadata(runtime / 'bin/gh')
        module = runtime / 'app/backend/auth-service/services/github_cli_runner.py'
        with patch.object(github_cli_runner, '__file__', str(module)):
            runner = github_cli_runner.GitHubCLIRunner.from_runtime()
        self.assertEqual(runner.binary, runtime / 'bin/gh')
        self.assertEqual(runner.digest, 'fixture-digest')


if __name__ == '__main__':
    unittest.main()
