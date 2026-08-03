import os
import tempfile
import threading
import time
import unittest
from pathlib import Path

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


if __name__ == '__main__':
    unittest.main()
