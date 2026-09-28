from concurrent.futures import ThreadPoolExecutor
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

import yaml


REPO = Path(__file__).resolve().parents[1]
SCRIPT = REPO / 'scripts/init-user-env-key.sh'
KEY = 'synthetic-user-env-encryption-key-for-tests'


@unittest.skipIf(os.name == 'nt', 'initializer runs in a Linux container')
class UserEnvKeyInitTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.output = self.root / 'core'
        self.key_file = self.output / 'user-env.key'
        self.env = os.environ.copy()
        for name in ('LAZYMIND_USER_ENV_SECRET_KEY', 'LAZYMIND_USER_ENV_SECRET_KEY_INPUT_FILE'):
            self.env.pop(name, None)

    def run_init(self, **values):
        return subprocess.run(
            ['sh', str(SCRIPT), str(self.output)], env={**self.env, **values},
            capture_output=True, text=True, timeout=10,
        )

    def assert_success(self, result):
        self.assertEqual(result.returncode, 0, result.stderr)
        key = self.key_file.read_text().strip()
        self.assertNotIn(key, result.stdout + result.stderr)
        self.assertEqual(self.key_file.stat().st_mode & 0o777, 0o600)
        return key

    def test_generates_once_and_reuses_after_restart(self):
        key = self.assert_success(self.run_init())
        self.assertRegex(key, r'^[0-9a-f]{64}$')
        before = self.key_file.stat()
        self.assertEqual(key, self.assert_success(self.run_init()))
        self.assertEqual(before.st_ino, self.key_file.stat().st_ino)
        self.assertEqual(before.st_mtime_ns, self.key_file.stat().st_mtime_ns)

    def test_reuses_legacy_mount_file_without_touching_siblings(self):
        self.output.mkdir(mode=0o755)
        self.key_file.write_text(KEY + '\n')
        sibling = self.output / 'existing-data'
        sibling.write_text('keep')
        original = self.key_file.read_bytes()
        self.assertEqual(KEY, self.assert_success(self.run_init()))
        self.assertEqual(self.key_file.read_bytes(), original)
        self.assertEqual(sibling.read_text(), 'keep')
        self.assertEqual(self.output.stat().st_mode & 0o777, 0o755)

    def test_concurrent_initializers_reuse_one_key_and_clean_temporary_files(self):
        with ThreadPoolExecutor(max_workers=6) as pool:
            results = list(pool.map(lambda _: self.run_init(), range(6)))
        for result in results:
            self.assert_success(result)
        self.assertEqual(list(self.output.iterdir()), [self.key_file])

    def test_direct_key_is_persisted_and_survives_without_override(self):
        self.assertEqual(KEY, self.assert_success(self.run_init(LAZYMIND_USER_ENV_SECRET_KEY=KEY)))
        self.assertEqual(KEY, self.assert_success(self.run_init()))
        self.assertEqual(KEY, self.assert_success(self.run_init(LAZYMIND_USER_ENV_SECRET_KEY=KEY)))

    def test_file_input_takes_precedence(self):
        source = self.root / 'input-key'
        source.write_text(KEY + '\n')
        result = self.run_init(
            LAZYMIND_USER_ENV_SECRET_KEY_INPUT_FILE=str(source),
            LAZYMIND_USER_ENV_SECRET_KEY='different-synthetic-key-value-for-tests',
        )
        self.assertEqual(KEY, self.assert_success(result))

    def test_explicit_key_change_is_rejected_without_rotation(self):
        saved = self.assert_success(self.run_init())
        original = self.key_file.read_bytes()
        result = self.run_init(LAZYMIND_USER_ENV_SECRET_KEY=KEY)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('differs from the persisted key', result.stderr)
        self.assertNotIn(saved, result.stdout + result.stderr)
        self.assertNotIn(KEY, result.stdout + result.stderr)
        self.assertEqual(self.key_file.read_bytes(), original)

    def test_invalid_input_does_not_replace_existing_key(self):
        self.assert_success(self.run_init())
        original = self.key_file.read_bytes()
        source = self.root / 'input-key'
        for value in ('', 'short', ' ' * 64, 'x' * 4098, KEY + '\n' + KEY):
            with self.subTest(value_length=len(value)):
                source.write_text(value)
                result = self.run_init(LAZYMIND_USER_ENV_SECRET_KEY_INPUT_FILE=str(source))
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(self.key_file.read_bytes(), original)

    def test_missing_explicit_file_does_not_generate_a_fallback_key(self):
        result = self.run_init(LAZYMIND_USER_ENV_SECRET_KEY_INPUT_FILE=str(self.root / 'missing'))
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(self.key_file.exists())

    def test_invalid_existing_key_is_not_regenerated_even_with_explicit_input(self):
        self.output.mkdir()
        self.key_file.write_text('broken')
        for values in ({}, {'LAZYMIND_USER_ENV_SECRET_KEY': KEY}):
            self.assertNotEqual(self.run_init(**values).returncode, 0)
            self.assertEqual(self.key_file.read_text(), 'broken')

    def test_symlink_and_directory_keys_are_rejected(self):
        self.output.mkdir()
        source = self.root / 'other-key'
        source.write_text(KEY)
        source.chmod(0o644)
        self.key_file.symlink_to(source)
        self.assertNotEqual(self.run_init().returncode, 0)
        self.assertEqual(source.stat().st_mode & 0o777, 0o644)
        self.key_file.unlink()
        self.key_file.mkdir()
        self.assertNotEqual(self.run_init().returncode, 0)


class UserEnvKeyComposeTest(unittest.TestCase):
    def test_default_startup_initializes_key_before_read_only_consumers(self):
        compose = yaml.safe_load((REPO / 'docker-compose.yml').read_text())
        services = compose['services']
        initializer = services['user-env-key-init']
        self.assertEqual(initializer['network_mode'], 'none')
        self.assertTrue(initializer['read_only'])
        self.assertNotIn('profiles', initializer)
        self.assertIn('./data/core:/run/secrets/user-env', initializer['volumes'])
        self.assertEqual(initializer['command'], ['/bin/sh', '/usr/local/bin/init-user-env-key.sh'])
        for name in ('core', 'core-dev'):
            with self.subTest(service=name):
                service = services[name]
                self.assertEqual(service['depends_on']['user-env-key-init']['condition'], 'service_completed_successfully')
                self.assertEqual(service['environment']['LAZYMIND_USER_ENV_SECRET_KEY_FILE'], '/run/secrets/user-env/user-env.key')
                self.assertIn('./data/core:/run/secrets/user-env:ro', service['volumes'])
                self.assertNotIn('LAZYMIND_USER_ENV_SECRET_KEY', service['environment'])
        self.assertEqual(compose['secrets']['user-env-key-input']['file'], '${LAZYMIND_USER_ENV_SECRET_KEY_FILE:-/dev/null}')


if __name__ == '__main__':
    unittest.main()
