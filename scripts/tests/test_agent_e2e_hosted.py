"""Verify hosted Agent installation and sandbox setup."""
import contextlib
import io
import json
import os
from pathlib import Path
import sys
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).parents[1]))
import agent_e2e_hosted as hosted


class HostedAgentInstallTests(unittest.TestCase):
    def test_windows_install_only_prepares_selected_agents(self):
        for selected in (*hosted.e2e.AGENTS, 'all'):
            with (
                self.subTest(agent=selected),
                tempfile.TemporaryDirectory() as tmp,
                patch.object(hosted.platform, 'IS_WINDOWS', True),
                patch.object(hosted.shutil, 'which', return_value='node.exe'),
                patch.object(hosted, 'install_npm_agent', side_effect=lambda p, agent, node: [node, agent]) as npm,
                patch.object(hosted, 'install_workbuddy', return_value=(['node.exe', 'workbuddy'], 'digest')) as buddy,
                patch.object(hosted.subprocess, 'run') as init_profile,
                patch.object(hosted.e2e, 'version', return_value='test-version'),
                patch.object(hosted, 'setup_codex_windows_sandbox', return_value=Path(tmp) / 'codex-home') as sandbox,
            ):
                root = Path(tmp)
                p = SimpleNamespace(root=root, output=root / 'results')
                exported = root / 'github-env'
                with patch.dict(os.environ, {'GITHUB_ENV': str(exported)}):
                    hosted.install_agents(p, selected)

                expected = set(hosted.e2e.AGENTS) if selected == 'all' else {selected}
                commands = json.loads((root / 'agent-commands.json').read_text())
                versions = json.loads((p.output / 'installed-agents.json').read_text())
                self.assertEqual(set(commands), expected)
                self.assertEqual(set(versions['versions']), expected)
                self.assertEqual({call.args[1] for call in npm.call_args_list}, expected - {'workbuddy'})
                self.assertEqual(buddy.call_count, int('workbuddy' in expected))
                self.assertEqual(init_profile.call_count, int('deepseek-harness' in expected))
                self.assertEqual(sandbox.call_count, int('codex' in expected))
                self.assertEqual('LAZYMIND_CI_CODEX_HOME=' in exported.read_text(), 'codex' in expected)


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
