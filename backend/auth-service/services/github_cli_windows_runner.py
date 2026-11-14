"""Windows authorization using the official CLI flow with an in-memory handoff."""
import json
import os
import subprocess
import threading

from services.github_cli_runner import GitHubCLIError, GitHubCLIRunner


class WindowsGitHubCLIRunner(GitHubCLIRunner):
    supported_platform = 'win32'
    version_prefix = 'lazymind github-auth-helper'

    @staticmethod
    def _command(args, **kwargs):
        return GitHubCLIRunner._command(args, creationflags=subprocess.CREATE_NO_WINDOW, **kwargs)

    @staticmethod
    def _helper_environment():
        allowed = {
            'SYSTEMROOT', 'WINDIR', 'SYSTEMDRIVE', 'COMSPEC', 'TEMP', 'TMP',
            'HTTP_PROXY', 'HTTPS_PROXY', 'ALL_PROXY', 'NO_PROXY', 'SSL_CERT_FILE', 'SSL_CERT_DIR',
        }
        env = {key: value for key, value in os.environ.items() if key.upper() in allowed}
        env.update(NO_COLOR='1', LANG='C', LC_ALL='C')
        return env

    def recover(self):
        # No persistent session or token files are created. The parent's stdin
        # pipe also terminates an active helper when the service exits.
        return None

    def authorize(self, *, cancel_event, on_device):
        env = self._helper_environment()
        self._verify_binary(env=env)
        with self._lock:
            if cancel_event.is_set():
                raise GitHubCLIError('GITHUB_AUTH_CANCELLED')
            process, reader = None, None
            payload, invalid = [], threading.Event()
            try:
                process = subprocess.Popen(
                    [str(self.binary)], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                    text=True, encoding='utf-8', env=env, creationflags=subprocess.CREATE_NO_WINDOW,
                )

                def read_token():
                    try:
                        value = process.stdout.read(8193)
                        if len(value) > 8192:
                            raise ValueError()
                        payload.append(value)
                    except (OSError, ValueError):
                        invalid.set()
                        if process.poll() is None:
                            process.kill()

                reader = threading.Thread(target=read_token, name='github-token-handoff', daemon=True)
                reader.start()
                self._wait_login(process, cancel_event, on_device, output=process.stderr)
                reader.join(timeout=1)
                if invalid.is_set() or reader.is_alive() or len(payload) != 1:
                    raise GitHubCLIError('GITHUB_CLI_OUTPUT_INVALID')
                try:
                    result = json.loads(payload[0])
                    token = result['access_token']
                    if (not isinstance(result, dict) or set(result) != {'access_token'}
                            or not isinstance(token, str) or not token or len(token) > 4096
                            or not token.isascii()
                            or any(ord(character) <= 32 or ord(character) == 127 for character in token)):
                        raise ValueError()
                except (KeyError, TypeError, ValueError):
                    raise GitHubCLIError('GITHUB_CLI_OUTPUT_INVALID') from None
                return {'access_token': token}
            except (OSError, subprocess.SubprocessError, UnicodeError):
                raise GitHubCLIError('GITHUB_CLI_UNAVAILABLE') from None
            finally:
                if process is not None:
                    if process.poll() is None:
                        process.kill()
                        process.wait(timeout=15)
                    if reader is not None:
                        reader.join(timeout=1)
                    for stream in (process.stdin, process.stdout, process.stderr):
                        if stream is not None and not stream.closed:
                            stream.close()
