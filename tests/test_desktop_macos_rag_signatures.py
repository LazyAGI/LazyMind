"""Reject mismatched or unsigned native RAG payloads before publishing."""
import importlib.util
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

SCRIPT = Path(__file__).resolve().parents[1] / 'desktop/scripts/verify-python-components.py'
spec = importlib.util.spec_from_file_location('verify_components', SCRIPT)
verify = importlib.util.module_from_spec(spec)
spec.loader.exec_module(verify)


class MacRAGSignaturesTests(unittest.TestCase):
    def test_adhoc_requires_actual_adhoc_signature_not_just_missing_team(self):
        for signature, accepted in [('Signature=adhoc\nTeamIdentifier=not set\n', True),
                                    ('TeamIdentifier=not set\n', False),
                                    ('Signature=adhoc\nTeamIdentifier=TEAM_A\n', False)]:
            with self.subTest(signature=signature), patch.object(verify.subprocess, 'run', return_value=
                    subprocess.CompletedProcess([], 0, '', signature)):
                if accepted:
                    self.assertEqual(verify.signed_team_id(Path('python'), adhoc=True), 'adhoc')
                else:
                    with self.assertRaisesRegex(RuntimeError, 'Expected ad-hoc'):
                        verify.signed_team_id(Path('python'), adhoc=True)

    def test_signature_requires_a_real_team(self):
        for team in ('', 'TeamIdentifier=not set\n'):
            with self.subTest(team=team), patch.object(verify.subprocess, 'run', return_value=
                    subprocess.CompletedProcess([], 0, '', team)):
                with self.assertRaisesRegex(RuntimeError, 'Missing signing TeamIdentifier'):
                    verify.signed_team_id(Path('python'))

    def test_invalid_signature_fails_even_with_matching_metadata(self):
        with patch.object(verify.subprocess, 'run', side_effect=
                subprocess.CalledProcessError(1, 'codesign')):
            with self.assertRaises(subprocess.CalledProcessError):
                verify.signed_team_id(Path('broken.so'))

    def test_every_native_file_must_match_python_including_extensionless_files(self):
        with tempfile.TemporaryDirectory() as tmp, patch.object(verify.sys, 'platform', 'darwin'):
            root = Path(tmp)
            (root / 'orjson.so').write_bytes(bytes.fromhex('cffaedfe') + b'fixture')
            (root / 'native-helper').write_bytes(bytes.fromhex('cafebabe') + b'fixture')
            (root / 'module.py').write_text('pass\n')
            with patch.object(verify, 'signed_team_id', return_value='TEAM_A') as signed:
                verify.verify_macos_team_id(root)
                self.assertEqual(signed.call_count, 3)  # Python plus both native files.
            with patch.object(verify, 'signed_team_id', side_effect=['TEAM_A', 'TEAM_A', 'TEAM_B']):
                with self.assertRaisesRegex(RuntimeError, 'TeamIdentifier mismatch'):
                    verify.verify_macos_team_id(root)

    def test_empty_payload_cannot_pass(self):
        with tempfile.TemporaryDirectory() as tmp, patch.object(verify.sys, 'platform', 'darwin'), \
                patch.object(verify, 'signed_team_id', return_value='TEAM_A'):
            with self.assertRaisesRegex(RuntimeError, 'no Mach-O'):
                verify.verify_macos_team_id(Path(tmp))


if __name__ == '__main__':
    unittest.main()
