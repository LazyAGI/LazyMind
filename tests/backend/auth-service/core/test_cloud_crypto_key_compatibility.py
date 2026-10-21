import importlib.util
import os
from pathlib import Path
import unittest
from unittest.mock import patch

from cryptography.exceptions import InvalidTag


SOURCE = (
    Path(__file__).resolve().parents[4]
    / 'backend/auth-service/core/cloud_crypto.py'
)
SPEC = importlib.util.spec_from_file_location('cloud_crypto_key_compatibility', SOURCE)
crypto = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(crypto)

# Independent vectors generated with Node AES-256-GCM and the original Desktop HMAC.
DEVICE_KEY = 'h7WpwdOhjknp0SaBxDAy0KTFhe6hV9TYvOnyUkm08P0'
LEGACY_KEY = 'dev-ragscan-secret-key-change-me'
DEVICE_CIPHERTEXT = (
    'AAECAwQFBgcICQoLJcPudRzy2YwJO7LTArxqCrFqBzEXPobMlXZkl8JEpPcfrSSN8FVSl'
    'PGh18FGdiqSFu5X6t69WZsEkS0OPEvZ4_SAbAXJeo4ddBuENg'
)
LEGACY_CIPHERTEXT = (
    'AAECAwQFBgcICQoLv62VhpgHI-DFd0aWv62v2w4sAYPT9C4O2HrJkWHwFZOHyBH0xLeUQi'
    'OQz_BC-wx9tgPXikY0iA3DRooy2ZKjDuJPDr05GeFEMZDcAg'
)
PAYLOAD = {'client_id': 'fixture-app', 'client_secret': 'fixture-secret'}


class CloudCryptoKeyCompatibilityTest(unittest.TestCase):
    def test_device_key_reads_both_historical_key_sources(self):
        with patch.dict(os.environ, {'LAZYMIND_AUTH_CLOUD_SECRET_KEY': DEVICE_KEY}):
            for ciphertext in (DEVICE_CIPHERTEXT, LEGACY_CIPHERTEXT):
                with self.subTest(ciphertext=ciphertext):
                    self.assertEqual(crypto.decrypt_json(ciphertext), PAYLOAD)

    def test_new_credentials_use_the_device_key(self):
        with patch.dict(os.environ, {'LAZYMIND_AUTH_CLOUD_SECRET_KEY': DEVICE_KEY}):
            ciphertext = crypto.encrypt_json(PAYLOAD)
            self.assertEqual(crypto.decrypt_json(ciphertext), PAYLOAD)
        with patch.dict(os.environ, {'LAZYMIND_AUTH_CLOUD_SECRET_KEY': LEGACY_KEY}):
            with self.assertRaises(InvalidTag):
                crypto.decrypt_json(ciphertext)

    def test_legacy_runtime_reproduces_old_desktop_read_failure(self):
        with patch.dict(os.environ, {'LAZYMIND_AUTH_CLOUD_SECRET_KEY': LEGACY_KEY}):
            self.assertEqual(crypto.decrypt_json(LEGACY_CIPHERTEXT), PAYLOAD)
            with self.assertRaises(InvalidTag):
                crypto.decrypt_json(DEVICE_CIPHERTEXT)

    def test_other_device_key_still_cannot_read_device_credentials(self):
        with patch.dict(os.environ, {'LAZYMIND_AUTH_CLOUD_SECRET_KEY': 'fixture-other-device'}):
            with self.assertRaises(InvalidTag):
                crypto.decrypt_json(DEVICE_CIPHERTEXT)


if __name__ == '__main__':
    unittest.main()
