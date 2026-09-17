"""Desktop Python startup must not load application adapters in resource trackers."""

import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


REPO = Path(__file__).resolve().parents[3]


class PythonStartupHookTests(unittest.TestCase):
    def run_interpreter(self, tracker=False, database='sqliteproxy://acceptance'):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / 'sitecustomize.py').write_text(
                (REPO / 'algorithm/sitecustomize.py').read_text()
            )
            package = root
            for part in ('lazymind', 'common', 'database'):
                package /= part
                package.mkdir()
                (package / '__init__.py').write_text('')
            marker = root / 'adapter-loaded'
            (package / 'sqlite_proxy.py').write_text(
                'from pathlib import Path\n'
                'def install_lazyllm_sqlite_proxy():\n'
                f'    Path({str(marker)!r}).write_text("loaded")\n'
            )
            environment = os.environ.copy()
            for key in (
                'LAZYMIND_DATABASE_URL', 'LAZYMIND_CORE_DATABASE_URL',
                'LAZYMIND_SEGMENT_STORE_URI_OR_PATH',
            ):
                environment.pop(key, None)
            environment.update({
                'PYTHONPATH': str(root),
                'LAZYMIND_DATABASE_URL': database,
                'PYTHONDONTWRITEBYTECODE': '1',
            })
            read_fd, write_fd = os.pipe()
            os.close(write_fd)
            try:
                command = (
                    'from multiprocessing.resource_tracker import main;'
                    f'main({read_fd})'
                ) if tracker else 'print("application ready")'
                result = subprocess.run(
                    [sys.executable, '-c', command], env=environment,
                    pass_fds=(read_fd,), capture_output=True, text=True,
                    timeout=10,
                )
                self.assertEqual(result.returncode, 0, result.stderr)
                return marker.exists()
            finally:
                os.close(read_fd)

    def test_application_installs_sqlite_proxy(self):
        self.assertTrue(self.run_interpreter())

    def test_postgres_does_not_install_sqlite_proxy(self):
        self.assertFalse(self.run_interpreter(database='postgresql://acceptance'))

    def test_resource_tracker_does_not_load_application_adapter(self):
        self.assertFalse(self.run_interpreter(tracker=True))


if __name__ == '__main__':
    unittest.main()
