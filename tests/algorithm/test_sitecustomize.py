"""Exercise interpreter startup without loading application dependencies."""
import importlib.util
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest import mock


SOURCE = Path(__file__).resolve().parents[2] / "algorithm" / "sitecustomize.py"


def _prepare_hook_sandbox(root):
    (root / "sitecustomize.py").write_bytes(SOURCE.read_bytes())
    package = root / "lazymind" / "common" / "database"
    package.mkdir(parents=True)
    for folder in [package, package.parent, package.parent.parent]:
        (folder / "__init__.py").touch()
    (package / "sqlite_proxy.py").write_text(
        "import os\nfrom pathlib import Path\n"
        "Path(os.environ['HOOK_MARKER']).write_text('imported')\n"
        "def install_lazyllm_sqlite_proxy():\n"
        "    Path(os.environ['HOOK_MARKER']).write_text('installed')\n"
    )
    marker = root / "hook-marker"
    env = {**os.environ, "PYTHONPATH": str(root), "HOOK_MARKER": str(marker),
           "LAZYMIND_DATABASE_URL": "sqliteproxy://test"}
    return marker, env


class SiteCustomizeTest(unittest.TestCase):
    def test_resource_tracker_command_is_detected(self):
        spec = importlib.util.spec_from_file_location("test_sitecustomize_module", SOURCE)
        module = importlib.util.module_from_spec(spec)
        database_env = {
            "LAZYMIND_DATABASE_URL": "",
            "LAZYMIND_CORE_DATABASE_URL": "",
            "LAZYMIND_SEGMENT_STORE_URI_OR_PATH": "",
        }
        with mock.patch.dict(os.environ, database_env):
            spec.loader.exec_module(module)

        tracker_args = [
            sys.executable,
            "-c",
            "from multiprocessing.resource_tracker import main;main(123)",
        ]
        with mock.patch.object(sys, "orig_argv", tracker_args):
            self.assertTrue(module._is_resource_tracker())
        with mock.patch.object(sys, "orig_argv", [sys.executable, "-c", "pass"]):
            self.assertFalse(module._is_resource_tracker())

    def test_application_keeps_hook(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            marker, env = _prepare_hook_sandbox(root)
            application = subprocess.run(
                [sys.executable, "-B", "-c", "pass"], env=env, cwd=root,
                capture_output=True, timeout=10,
            )
            self.assertEqual(application.returncode, 0, application.stderr.decode())
            self.assertEqual(marker.read_text(), "installed")

    @unittest.skipUnless(os.name == "posix", "test requires POSIX descriptor passing")
    def test_tracker_skips_business_imports(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            marker, env = _prepare_hook_sandbox(root)
            reader, writer = os.pipe()
            os.close(writer)
            try:
                tracker = subprocess.run(
                    [sys.executable, "-B", "-c",
                     f"from multiprocessing.resource_tracker import main;main({reader})"],
                    env=env, cwd=root, pass_fds=(reader,),
                    capture_output=True, timeout=10,
                )
            finally:
                os.close(reader)
            self.assertEqual(tracker.returncode, 0, tracker.stderr.decode())
            self.assertFalse(marker.exists(), "tracker imported business code")


if __name__ == "__main__":
    unittest.main()
