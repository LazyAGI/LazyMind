import importlib.util
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('replace_app', Path(__file__).with_name('replace-local-app.py'))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class BundleReplacementTest(unittest.TestCase):
    def test_failed_validation_restores_previous_bundle_then_retry_succeeds(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            source, target, backup = (root / name for name in ('candidate.app', 'installed.app', 'previous.app'))
            source.mkdir(); target.mkdir()
            (source / 'version').write_text('new')
            (target / 'version').write_text('old')

            def reject(_):
                raise ValueError('injected signature validation failure')

            with self.assertRaisesRegex(ValueError, 'signature'):
                module.replace_application(source, target, backup, reject)
            self.assertEqual((target / 'version').read_text(), 'old')
            self.assertEqual((root / 'previous.app.failed' / 'version').read_text(), 'new')
            self.assertFalse(backup.exists())
            module.replace_application(source, target, backup, lambda _: None)
            self.assertEqual((target / 'version').read_text(), 'new')
            self.assertEqual((backup / 'version').read_text(), 'old')


if __name__ == '__main__':
    unittest.main()
