"""Exercise the real interpreter startup hook without loading algorithm services."""

import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


ALGORITHM = Path(__file__).resolve().parents[2] / 'algorithm'


class SiteCustomizeTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        root = Path(self.temp.name)
        self.marker = root / 'adapter-loaded'
        package = root / 'lazymind' / 'common' / 'database'
        package.mkdir(parents=True)
        for directory in (package, package.parent, package.parent.parent):
            (directory / '__init__.py').write_text('')
        (package / 'sqlite_proxy.py').write_text(
            'from pathlib import Path\n'
            'def install_lazyllm_sqlite_proxy():\n'
            f'    Path({str(self.marker)!r}).touch()\n'
        )
        self.env = dict(os.environ, PYTHONPATH=os.pathsep.join((str(root), str(ALGORITHM))))
        for key in ('LAZYMIND_DATABASE_URL', 'LAZYMIND_CORE_DATABASE_URL',
                    'LAZYMIND_SEGMENT_STORE_URI_OR_PATH'):
            self.env.pop(key, None)

    def run_python(self, command, **kwargs):
        result = subprocess.run(
            [sys.executable, '-B', '-c', command], env=self.env,
            capture_output=True, text=True, timeout=10, **kwargs,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertNotIn('Error in sitecustomize', result.stderr)
        return result

    def test_regular_proxy_process_installs_adapter(self):
        self.env['LAZYMIND_DATABASE_URL'] = 'sqliteproxy://lazyllm'
        self.run_python('pass')
        self.assertTrue(self.marker.exists())

    def test_non_proxy_process_does_not_install_adapter(self):
        self.run_python('pass')
        self.assertFalse(self.marker.exists())

    @unittest.skipIf(os.name == 'nt', 'resource tracker pipe uses POSIX file descriptors')
    def test_real_resource_tracker_exits_without_loading_business_hooks(self):
        self.env['LAZYMIND_CORE_DATABASE_URL'] = 'sqliteproxy://core'
        read_fd, write_fd = os.pipe()
        os.close(write_fd)  # EOF: the tracker must clean up and exit immediately.
        try:
            self.run_python(
                f'from multiprocessing.resource_tracker import main;main({read_fd})',
                pass_fds=(read_fd,),
            )
        finally:
            os.close(read_fd)
        self.assertFalse(self.marker.exists(), 'tracker imported business initialization')


if __name__ == '__main__':
    unittest.main()
