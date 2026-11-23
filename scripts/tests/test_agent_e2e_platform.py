"""Exercise real locks, subprocess pipes, shell quoting and owned-child cleanup."""
import json
import os
import shutil
import subprocess
import sys
import tempfile
import time
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parents[1]))
import agent_e2e as runner
import agent_e2e_platform as platform


class PlatformProcessTests(unittest.TestCase):
    def test_lock_blocks_another_process_and_is_released_after_context_exit(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / 'shared.lock'
            script = (f'import sys;sys.path.insert(0,{str(Path(__file__).parents[1])!r});'
                      'from agent_e2e_platform import file_lock;'
                      f'path={str(path)!r}\n'
                      'try:\n with file_lock(path,blocking=False): pass\n'
                      'except BlockingIOError:sys.exit(17)\n')
            with platform.file_lock(path):
                blocked = subprocess.run([sys.executable, '-c', script], capture_output=True, timeout=10, check=False)
                self.assertEqual(blocked.returncode, 17, blocked.stderr.decode())
            released = subprocess.run([sys.executable, '-c', script], capture_output=True, timeout=10, check=False)
            self.assertEqual(released.returncode, 0, released.stderr.decode())

    def test_finished_root_cli_does_not_wait_for_an_orphan_holding_its_pipe(self):
        with tempfile.TemporaryDirectory() as tmp:
            code = ("import subprocess,sys,json;"
                    "subprocess.Popen([sys.executable,'-c','import time;time.sleep(30)']);"
                    "sys.stdout.write(json.dumps({'type':'system','session_id':'finished-root'}));"
                    "sys.stdout.flush()")
            start = time.monotonic()
            result = runner.invoke_process([sys.executable, '-c', code], os.environ.copy(), '', Path(tmp), 4)
            self.assertFalse(result['timed_out'])
            self.assertEqual(result['exit_code'], 0)
            self.assertEqual(result['host_session_id'], 'finished-root')
            self.assertLess(time.monotonic() - start, 4)

    def test_both_pipe_streams_and_utf8_final_event_are_drained(self):
        with tempfile.TemporaryDirectory() as tmp:
            code = ("import sys,json;sys.stderr.write('diagnostic\\n');sys.stderr.flush();"
                    "sys.stdout.buffer.write((json.dumps({'type':'system','session_id':'本次测试'},ensure_ascii=False)+'\\r\\n').encode());"
                    "sys.stdout.buffer.flush()")
            result = runner.invoke_process([sys.executable, '-c', code], os.environ.copy(), '', Path(tmp), 5)
            self.assertFalse(result['timed_out'])
            self.assertEqual(result['host_session_id'], '本次测试')

    def test_shell_argument_roundtrip_preserves_spaces_unicode_and_metacharacters(self):
        shell = (shutil.which('pwsh') or shutil.which('powershell')) if platform.IS_WINDOWS else '/bin/sh'
        if not shell:
            self.skipTest('PowerShell is not installed')
        values = ['space here', "apostrophe's", '分号;原样', '$(echo must-stay-literal)']
        command = platform.shell_command([sys.executable, '-c', 'import json,sys;print(json.dumps(sys.argv[1:]))', *values])
        prefix = [shell, '-NoProfile', '-Command'] if platform.IS_WINDOWS else [shell, '-c']
        result = subprocess.run([*prefix, command], capture_output=True, text=True, encoding='utf-8', check=True, timeout=15)
        self.assertEqual(json.loads(result.stdout.strip()), values)


if __name__ == '__main__':
    unittest.main()
