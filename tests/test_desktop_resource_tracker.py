"""Housekeeping Python processes must not bootstrap LazyLLM recursively."""
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest


class ResourceTrackerStartupTests(unittest.TestCase):
    def test_only_application_interpreters_install_database_hooks(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            shutil.copyfile(Path(__file__).resolve().parents[1] / 'algorithm/sitecustomize.py', root / 'sitecustomize.py')
            package = root / 'lazymind/common/database'
            package.mkdir(parents=True)
            (package / 'sqlite_proxy.py').write_text('def install_lazyllm_sqlite_proxy():\n print("HOOK_INSTALLED")\n')
            env = {**os.environ, 'PYTHONPATH': str(root), 'LAZYMIND_DATABASE_URL': 'sqliteproxy://test'}
            for code, expected in [('print("application")', True),
                                   ('from multiprocessing.resource_tracker import main;main(0)', False)]:
                with self.subTest(code=code):
                    result = subprocess.run([sys.executable, '-B', '-c', code], env=env,
                                            stdin=subprocess.DEVNULL, capture_output=True, text=True, timeout=10)
                    self.assertEqual(result.returncode, 0, result.stderr)
                    self.assertEqual('HOOK_INSTALLED' in result.stdout, expected)


if __name__ == '__main__':
    unittest.main()
