import contextlib
import io
from pathlib import Path
import runpy
import subprocess
import sys
import tempfile
from unittest.mock import Mock, patch
import unittest
import zipfile

PROBE = runpy.run_path(str(Path(__file__).with_name('verify-update-format.py')))


class EvidenceDiagnosticsTests(unittest.TestCase):
    def test_child_failure_reports_stage_and_status_without_command_or_raw_streams(self):
        secret = 'sensitive-fixture-credential'
        code = f"import sys;print('{secret}');print('ModuleNotFoundError: No module named \\'pyarrow\\' {secret}',file=sys.stderr);sys.exit(23)"
        log = io.StringIO()
        with contextlib.redirect_stderr(log), self.assertRaises(subprocess.CalledProcessError):
            PROBE['probe']('source.create-index', PROBE['output'], [sys.executable, '-c', code])
        self.assertIn('source.create-index', log.getvalue())
        self.assertIn('exit=23', log.getvalue())
        self.assertIn('ModuleNotFoundError', log.getvalue())
        self.assertIn('pyarrow', log.getvalue())
        self.assertNotIn(secret, log.getvalue())
        self.assertNotIn(sys.executable, log.getvalue())

    def test_component_mirror_failure_is_safe_on_both_streams(self):
        tools = PROBE['load_script']('verify-python-components.py')
        opener = Mock()
        opener.open.side_effect = OSError('private-url-with-credentials')
        entry = dict(id='rag', platform='windows', arch='amd64', filename='rag.zip', sizeBytes=1, sha256='a'*64,
                     url='https://first.invalid/rag.zip', fallbackUrl='https://second.invalid/rag.zip')
        with tempfile.TemporaryDirectory() as tmp, patch.object(tools, 'build_opener', return_value=opener):
            log = io.StringIO()
            with contextlib.redirect_stdout(log), contextlib.redirect_stderr(log), self.assertRaises(OSError):
                PROBE['probe']('source.extract-rag', PROBE['extract_overlay'], entry, Path(tmp)/'rag', None, None,
                               {'platform': 'windows', 'arch': 'amd64'}, tools)
            self.assertEqual(opener.open.call_count, 2)
            self.assertIn('using fallback', log.getvalue())
            self.assertNotIn('private-url-with-credentials', log.getvalue())

    def test_timeout_and_bad_zip_do_not_expose_streams_or_archive_members(self):
        for error, expected in [(subprocess.TimeoutExpired(['private-command'], 90, stderr=b'private-text'), 'timeout=90s'),
                                (zipfile.BadZipFile('private-path'), 'BadZipFile')]:
            with self.subTest(expected=expected):
                def fail():
                    raise error
                log = io.StringIO()
                with contextlib.redirect_stderr(log), self.assertRaises(type(error)):
                    PROBE['probe']('target.extract-rag', fail)
                self.assertIn(expected, log.getvalue())
                self.assertNotIn('private', log.getvalue())


if __name__ == '__main__':
    unittest.main()
