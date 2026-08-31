import os
import tempfile
import threading
import time
import unittest
from pathlib import Path
from unittest import mock

from channel_gateway.common.infrastructure.security import JsonCipher


class JsonCipherMasterKeyTest(unittest.TestCase):
    def test_waits_for_concurrent_master_key_writer(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            key_path = Path(temp_dir) / 'master.key'
            key_path.touch()
            expected = os.urandom(32)

            def finish_creation():
                time.sleep(0.1)
                key_path.write_bytes(expected)

            writer = threading.Thread(target=finish_creation)
            writer.start()
            try:
                cipher = JsonCipher(str(key_path))
            finally:
                writer.join()

            self.assertEqual(cipher._master_key, expected)

    def test_recovers_legacy_windows_text_mode_key(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            key_path = Path(temp_dir) / 'master.key'
            expected = b'prefix\n' + bytes(range(25))
            key_path.write_bytes(expected.replace(b'\n', b'\r\n'))

            cipher = JsonCipher(str(key_path))

            self.assertEqual(cipher._master_key, expected)
            self.assertEqual(key_path.read_bytes(), expected)

    def test_does_not_rewrite_unrecognized_invalid_key(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            key_path = Path(temp_dir) / 'master.key'
            invalid = b'x' * 33
            key_path.write_bytes(invalid)

            with self.assertRaisesRegex(RuntimeError, 'master key is invalid'):
                JsonCipher(str(key_path))

            self.assertEqual(key_path.read_bytes(), invalid)

    def test_new_key_is_created_with_binary_flag_when_available(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            key_path = Path(temp_dir) / 'master.key'
            real_open = os.open
            observed_flags = []

            def recording_open(path, flags, mode=0o777):
                observed_flags.append(flags)
                return real_open(path, flags & ~0x8000, mode)

            with mock.patch.object(os, 'O_BINARY', 0x8000, create=True):
                with mock.patch.object(os, 'open', side_effect=recording_open):
                    JsonCipher(str(key_path))

            self.assertTrue(observed_flags[0] & 0x8000)


if __name__ == '__main__':
    unittest.main()
