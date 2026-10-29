"""Packaged recovery endpoints must retain their public RBAC path."""
from pathlib import Path
import shutil
import tempfile
import unittest

from extract_api_permissions import extract_from_py_file


class GitHubRecoveryPermissionExportTest(unittest.TestCase):
    def test_packaged_app_layout_exports_full_recovery_paths(self):
        source = Path(__file__).resolve().parents[1] / 'auth-service/api/cloud_oauth.py'
        with tempfile.TemporaryDirectory() as directory:
            packaged = Path(directory) / 'app/api/cloud_oauth.py'
            packaged.parent.mkdir(parents=True)
            shutil.copyfile(source, packaged)
            entries = extract_from_py_file(packaged)
        expected = '/api/authservice/v1/cloud/github/cli/attempts/{attempt_id}'
        for method in ('GET', 'DELETE'):
            self.assertIn({'method': method, 'path': expected, 'permissions': ['model.write']}, entries)
