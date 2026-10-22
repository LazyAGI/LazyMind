"""Regression checks for public evidence integrity, not live Runtime tests."""
import hashlib
import json
from pathlib import Path
import shutil
import tempfile
import unittest

from verify_rc_evidence import PUBLIC, verify


class PublicEvidenceTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name) / 'public'
        shutil.copytree(PUBLIC, self.root)

    def rewrite(self, name, edit):
        path = self.root / name
        data = json.loads(path.read_text()); edit(data)
        path.write_text(json.dumps(data))
        manifest = json.loads((self.root / 'manifest.json').read_text())
        manifest['files'][name] = hashlib.sha256(path.read_bytes()).hexdigest()
        (self.root / 'manifest.json').write_text(json.dumps(manifest))

    def test_published_evidence_is_self_contained(self):
        self.assertEqual(verify(self.root)['result'], 'PASS')

    def test_modified_file_without_updated_hash_is_rejected(self):
        path = self.root / 'normal.json'
        path.write_text(path.read_text() + ' ')
        with self.assertRaisesRegex(ValueError, 'public hash mismatch'):
            verify(self.root)

    def test_manifest_cannot_omit_a_required_negative(self):
        path = self.root / 'manifest.json'
        data = json.loads(path.read_text()); del data['files']['brief-missing.json']
        path.write_text(json.dumps(data))
        with self.assertRaisesRegex(ValueError, 'required evidence cases'):
            verify(self.root)

    def test_hash_update_does_not_hide_false_zero_launch_claim(self):
        self.rewrite('normal.json', lambda d: d['records']['result']['values'].update({'/launch_count': 0}))
        with self.assertRaisesRegex(ValueError, 'launch count'):
            verify(self.root)

    def test_hash_update_does_not_hide_recovery_session_change(self):
        self.rewrite('recovery.json', lambda d: d['records']['before']['values'].update({'/session_id': 'another-session'}))
        with self.assertRaisesRegex(ValueError, 'same-session recovery'):
            verify(self.root)


if __name__ == '__main__':
    unittest.main()
