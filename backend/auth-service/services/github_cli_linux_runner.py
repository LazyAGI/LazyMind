"""Container-only gh authorization with private, non-swappable temporary storage."""
from contextlib import contextmanager
import fcntl
import os
from pathlib import Path
import re
import resource
import shutil
import stat
import subprocess
import sys
import uuid

from services.github_cli_runner import GitHubCLIError, GitHubCLIRunner


class LinuxGitHubCLIRunner(GitHubCLIRunner):
    supported_platform = 'linux'

    def probe(self):
        # A server process's environment or login never identifies an application user.
        return None

    def _validate_storage(self):
        try:
            info = self.root.lstat()
            if (sys.platform != 'linux' or self.root.resolve() != self.root.absolute()
                    or not stat.S_ISDIR(info.st_mode) or info.st_uid != os.getuid()
                    or stat.S_IMODE(info.st_mode) != 0o700):
                raise ValueError()
            mounted = False
            for line in Path('/proc/self/mountinfo').read_text().splitlines():
                before, after = line.split(' - ', 1)
                fields, filesystem = before.split(), after.split()[0]
                mount = re.sub(r'\\([0-7]{3})', lambda match: chr(int(match[1], 8)), fields[4])
                if mount == str(self.root):
                    mounted = filesystem == 'tmpfs' and {'noexec', 'nosuid', 'nodev'} <= set(fields[5].split(','))
                    break
            if not mounted or Path('/sys/fs/cgroup/memory.swap.max').read_text().strip() != '0':
                raise ValueError()
            pattern = Path('/proc/sys/kernel/core_pattern').read_text().strip()
            if resource.getrlimit(resource.RLIMIT_CORE) != (0, 0) or not pattern or pattern.startswith('|'):
                raise ValueError()
        except (OSError, ValueError, IndexError):
            raise GitHubCLIError('GITHUB_CLI_STORAGE_UNSAFE') from None

    @contextmanager
    def _lease(self):
        self._validate_storage()
        fd = None
        try:
            fd = os.open(self.root / 'lease.lock', os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
            info = os.fstat(fd)
            if (not stat.S_ISREG(info.st_mode) or info.st_uid != os.getuid()
                    or stat.S_IMODE(info.st_mode) != 0o600 or info.st_nlink != 1):
                raise GitHubCLIError('GITHUB_CLI_STORAGE_UNSAFE')
            try:
                fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
            except BlockingIOError:
                raise GitHubCLIError('GITHUB_CLI_BUSY') from None
            yield fd
        except OSError:
            raise GitHubCLIError('GITHUB_CLI_STORAGE_UNSAFE') from None
        finally:
            if fd is not None:
                # Do not explicitly unlock: a surviving child must retain this lease.
                os.close(fd)

    def _remove_stale_sessions(self):
        try:
            for directory in self.root.glob('session-*'):
                info = directory.lstat()
                if (not re.fullmatch('session-[0-9a-f]{32}', directory.name)
                        or not stat.S_ISDIR(info.st_mode) or info.st_uid != os.getuid()
                        or stat.S_IMODE(info.st_mode) != 0o700):
                    raise GitHubCLIError('GITHUB_CLI_RECOVERY_REQUIRED')
                shutil.rmtree(directory)
        except OSError:
            raise GitHubCLIError('GITHUB_CLI_RECOVERY_REQUIRED') from None

    def recover(self):
        with self._lease():
            self._remove_stale_sessions()

    @staticmethod
    def _environment(directory):
        # Keep network configuration, but never inherit identity, debug or loader variables.
        allowed = ('HTTP_PROXY', 'HTTPS_PROXY', 'ALL_PROXY', 'NO_PROXY',
                   'http_proxy', 'https_proxy', 'all_proxy', 'no_proxy', 'SSL_CERT_FILE', 'SSL_CERT_DIR')
        env = {key: os.environ[key] for key in allowed if key in os.environ}
        paths = {
            'HOME': directory / 'home', 'GH_CONFIG_DIR': directory / 'gh',
            'GH_CACHE_DIR': directory / 'cache', 'XDG_CONFIG_HOME': directory / 'xdg-config',
            'XDG_CACHE_HOME': directory / 'xdg-cache', 'XDG_DATA_HOME': directory / 'xdg-data',
            'XDG_RUNTIME_DIR': directory / 'xdg-run', 'TMPDIR': directory / 'tmp',
        }
        for key, path in paths.items():
            path.mkdir(mode=0o700)
            env[key] = str(path)
        env.update(
            PATH='/usr/local/bin:/usr/bin:/bin', LANG='C', LC_ALL='C',
            GH_PROMPT_DISABLED='1', GH_NO_UPDATE_NOTIFIER='1', GH_NO_EXTENSION_UPDATE_NOTIFIER='1',
            GH_BROWSER='/usr/bin/true', NO_COLOR='1', GIT_TERMINAL_PROMPT='0', GIT_CONFIG_NOSYSTEM='1',
            GIT_CONFIG_GLOBAL=str(directory / 'gitconfig'),
            DBUS_SESSION_BUS_ADDRESS='unix:path=' + str(directory / 'xdg-run/no-dbus'),
            DBUS_SYSTEM_BUS_ADDRESS='unix:path=' + str(directory / 'xdg-run/no-dbus'),
        )
        return env

    def authorize(self, *, cancel_event, on_device):
        if cancel_event.is_set():
            raise GitHubCLIError('GITHUB_AUTH_CANCELLED')
        with self._lease() as lease:
            self._remove_stale_sessions()
            return self._authorize_private(lease, cancel_event, on_device)

    def _authorize_private(self, lease, cancel_event, on_device):
        directory = self.root / ('session-' + uuid.uuid4().hex)
        process = None
        try:
            directory.mkdir(mode=0o700)
            env = self._environment(directory)
            self._verify_binary(env=env, pass_fds=(lease,))
            empty = self._command([str(self.binary), 'auth', 'token', '--hostname', 'github.com'],
                                  env=env, pass_fds=(lease,))
            if empty.returncode == 0 or empty.stdout.strip():
                raise GitHubCLIError('GITHUB_CLI_ISOLATION_UNAVAILABLE')
            if cancel_event.is_set():
                raise GitHubCLIError('GITHUB_AUTH_CANCELLED')
            process = subprocess.Popen(
                [str(self.binary), 'auth', 'login', '--hostname', 'github.com', '--git-protocol', 'https',
                 '--web', '--skip-ssh-key', '--clipboard=false', '--insecure-storage'],
                stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                text=True, env=env, cwd=directory, pass_fds=(lease,),
            )
            self._wait_login(process, cancel_event, on_device)
            exported = self._command([str(self.binary), 'auth', 'token', '--hostname', 'github.com'],
                                     env=env, pass_fds=(lease,))
            token = exported.stdout.strip()
            if exported.returncode or not token or len(token) > 4096 or any(char.isspace() for char in token):
                raise GitHubCLIError('GITHUB_CLI_OUTPUT_INVALID')
        except (OSError, subprocess.SubprocessError):
            raise GitHubCLIError('GITHUB_CLI_UNAVAILABLE') from None
        finally:
            try:
                if process is not None:
                    if process.poll() is None:
                        process.kill()
                        process.wait(timeout=15)
                    process.stdout.close()
                if directory.exists():
                    shutil.rmtree(directory)
            except (OSError, subprocess.SubprocessError):
                raise GitHubCLIError('GITHUB_CLI_CLEANUP_FAILED') from None
        return {'access_token': token}
