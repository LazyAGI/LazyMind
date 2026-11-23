"""Verify sandbox setup uses the supported RPC and fails closed."""
import contextlib
import io
import os
from pathlib import Path
import sys
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).parents[1]))
import agent_e2e_hosted as hosted


class HostedSandboxSetupTests(unittest.TestCase):
    def test_setup_waits_for_completion_and_rejects_failure(self):
        for success in (True, False):
            with self.subTest(success=success), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                script = root / 'setup-fixture.py'
                script.write_text('''import json,sys
for line in sys.stdin:
 request=json.loads(line); method=request['method']; ident=request.get('id')
 if method=='initialized': continue
 if method=='initialize': result={}
 elif method=='windowsSandbox/readiness': result={'status':'updateRequired'}
 elif method=='windowsSandbox/setupStart':
  assert request['params']['mode']=='elevated'
  print(json.dumps({'id':ident,'result':{'started':True}}),flush=True)
  print(json.dumps({'method':'windowsSandbox/setupCompleted','params':{'success':SUCCESS,'error':None if SUCCESS else 'fixture failure'}}),flush=True)
  continue
 else: raise AssertionError(method)
 print(json.dumps({'id':ident,'result':result}),flush=True)
'''.replace('SUCCESS', repr(success)), encoding='utf-8')
                args = SimpleNamespace(root=root, home=root / 'connector', output=root)
                with patch.object(hosted.e2e, 'clean_env', return_value=os.environ.copy()), contextlib.redirect_stdout(io.StringIO()):
                    if success:
                        self.assertEqual(hosted.setup_codex_windows_sandbox(args, [sys.executable, str(script)]), root / 'codex-home')
                    else:
                        with self.assertRaisesRegex(RuntimeError, 'sandbox setup failed'):
                            hosted.setup_codex_windows_sandbox(args, [sys.executable, str(script)])


if __name__ == '__main__':
    unittest.main()
