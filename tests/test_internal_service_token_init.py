from concurrent.futures import ThreadPoolExecutor
import os
from pathlib import Path
import subprocess
import tempfile
import unittest


REPO = Path(__file__).resolve().parents[1]
SCRIPT = REPO / 'scripts/init-internal-service-token.sh'


@unittest.skipIf(os.name == 'nt', 'initializer runs in a Linux container')
class InternalServiceTokenInitTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.output = self.root / 'secrets'
        self.env = os.environ.copy()
        for key in ('LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN', 'LAZYMIND_INTERNAL_SERVICE_TOKEN_INPUT_FILE'):
            self.env.pop(key, None)

    def run_init(self, **values):
        return subprocess.run(
            ['sh', str(SCRIPT), str(self.output)], env={**self.env, **values},
            capture_output=True, text=True, timeout=10,
        )

    def assert_success(self, result):
        self.assertEqual(result.returncode, 0, result.stderr)
        token = (self.output / 'token').read_text().strip()
        self.assertNotIn(token, result.stdout + result.stderr)
        self.assertEqual((self.output / 'token').stat().st_mode & 0o777, 0o444)
        return token

    def test_generates_once_and_reuses_after_restart(self):
        first = self.assert_success(self.run_init())
        self.assertRegex(first, r'^[0-9a-f]{64}$')
        self.assertEqual(first, self.assert_success(self.run_init()))

    def test_concurrent_initializers_share_one_token(self):
        with ThreadPoolExecutor(max_workers=4) as pool:
            results = list(pool.map(lambda _: self.run_init(), range(4)))
        for result in results:
            self.assert_success(result)
        self.assertEqual(list(self.output.iterdir()), [self.output / 'token'])

    def test_file_override_and_existing_direct_variable_have_precedence(self):
        self.assert_success(self.run_init())
        source = self.root / 'input-token'
        source.write_text('file-override-fixture-token\n')
        values = {'LAZYMIND_INTERNAL_SERVICE_TOKEN_INPUT_FILE': str(source)}
        self.assertEqual(self.assert_success(self.run_init(**values)), 'file-override-fixture-token')
        values['LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN'] = 'direct-override-fixture-token'
        self.assertEqual(self.assert_success(self.run_init(**values)), 'direct-override-fixture-token')
        self.assertEqual(self.assert_success(self.run_init()), 'direct-override-fixture-token')

    def test_invalid_explicit_file_does_not_replace_the_saved_token(self):
        saved = self.assert_success(self.run_init())
        source = self.root / 'invalid-input'
        for value in ('', 'short', 'x' * 4097, 'first-line-fixture\nsecond-line-fixture'):
            source.write_text(value)
            result = self.run_init(LAZYMIND_INTERNAL_SERVICE_TOKEN_INPUT_FILE=str(source))
            self.assertNotEqual(result.returncode, 0)
            self.assertEqual((self.output / 'token').read_text().strip(), saved)
        result = self.run_init(LAZYMIND_INTERNAL_SERVICE_TOKEN_INPUT_FILE=str(self.root / 'missing'))
        self.assertNotEqual(result.returncode, 0)

    def test_corrupt_persisted_token_is_not_silently_regenerated(self):
        self.output.mkdir()
        (self.output / 'token').write_text('invalid')
        self.assertNotEqual(self.run_init().returncode, 0)
        self.assertEqual((self.output / 'token').read_text(), 'invalid')


if __name__ == '__main__':
    unittest.main()
