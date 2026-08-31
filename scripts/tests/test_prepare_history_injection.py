from __future__ import annotations

import hashlib
import importlib.util
import io
import sys
import tempfile
import unittest
import zipfile
from pathlib import Path


MODULE_PATH = Path(__file__).parents[1] / "prepare_history_injection.py"
SPEC = importlib.util.spec_from_file_location("prepare_history_injection", MODULE_PATH)
prepare_history_injection = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
sys.modules[SPEC.name] = prepare_history_injection
SPEC.loader.exec_module(prepare_history_injection)


class PrepareDockerTest(unittest.TestCase):
    def test_repository_config_declares_five_conversations(self) -> None:
        config_path = MODULE_PATH.parents[1] / "desktop" / "history-injection-package.json"
        config = prepare_history_injection.load_config(config_path)
        self.assertEqual(len(config.conversation_ids), 5)
        self.assertEqual(len(set(config.conversation_ids)), 5)

    def test_verified_cache_avoids_download(self) -> None:
        payload = b"history-injection-package"
        config = prepare_history_injection.PackageConfig(
            "https://example.invalid/package.zip",
            hashlib.sha256(payload).hexdigest(),
            len(payload),
            "history-injection.zip",
            ("conversation-id",),
        )
        with tempfile.TemporaryDirectory() as temporary:
            archive = Path(temporary) / config.runtime_file_name
            archive.write_bytes(payload)
            self.assertTrue(prepare_history_injection.archive_is_valid(archive, config))

            called = False

            def fail_open(_url: str, _timeout: int) -> io.BytesIO:
                nonlocal called
                called = True
                raise AssertionError("verified cache must not be downloaded again")

            if not prepare_history_injection.archive_is_valid(archive, config):
                prepare_history_injection.download_archive(config, archive, attempts=1, open_url=fail_open)
            self.assertFalse(called)

    def test_download_verifies_and_publishes_archive(self) -> None:
        payload = b"downloaded-history-injection-package"
        config = prepare_history_injection.PackageConfig(
            "https://example.invalid/package.zip",
            hashlib.sha256(payload).hexdigest(),
            len(payload),
            "history-injection.zip",
            ("conversation-id",),
        )
        with tempfile.TemporaryDirectory() as temporary:
            archive = Path(temporary) / config.runtime_file_name
            prepare_history_injection.download_archive(
                config,
                archive,
                attempts=1,
                open_url=lambda _url, _timeout: io.BytesIO(payload),
            )
            self.assertEqual(archive.read_bytes(), payload)

    def test_extracts_only_history_injection_subtree(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive = root / "package.zip"
            with zipfile.ZipFile(archive, "w") as package:
                package.writestr("history-injection/ppt/sample.zip", b"bundle")
                package.writestr("ignored.txt", b"ignored")
            output = root / "bundles"
            prepare_history_injection.extract_archive(archive, output, "a" * 64)
            self.assertEqual((output / "ppt" / "sample.zip").read_bytes(), b"bundle")
            self.assertFalse((output / "ignored.txt").exists())
            marker = output / prepare_history_injection.MARKER_NAME
            self.assertEqual(marker.read_text().strip(), "a" * 64)

    def test_rejects_parent_path(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive = root / "package.zip"
            with zipfile.ZipFile(archive, "w") as package:
                package.writestr("history-injection/../escape.zip", b"unsafe")
            with self.assertRaisesRegex(ValueError, "unsafe path"):
                prepare_history_injection.extract_archive(archive, root / "bundles", "b" * 64)
            self.assertFalse((root / "escape.zip").exists())


if __name__ == "__main__":
    unittest.main()
