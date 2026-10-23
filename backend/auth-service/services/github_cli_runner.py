import base64
import hashlib
import json
import os
from pathlib import Path
import plistlib
import queue
import re
import shutil
import stat
import subprocess
import sys
import threading
import time
import uuid

import yaml


GH_VERSION = '2.102.1'
AUTH_TIMEOUT = 900
PROFILE = '(version 1) (allow default) (deny process-exec (literal "/usr/bin/security"))'
DEVICE_URL = 'https://github.com/login/device'


class GitHubCLIError(RuntimeError):
    def __init__(self, code):
        self.code = code
        super().__init__(code)


class GitHubCLIRunner:
    def __init__(self, *, binary_path, expected_sha256, runtime_dir):
        self.binary = Path(binary_path)
        self.digest = expected_sha256
        self.root = Path(runtime_dir)
        self._lock = threading.Lock()

    @classmethod
    def from_runtime(cls):
        runtime = Path(os.environ.get('LAZYMIND_RUNTIME_RESOURCES_ROOT') or Path(__file__).resolve().parents[4])
        binary = Path(os.environ.get('LAZYMIND_GITHUB_CLI_PATH') or runtime / 'bin/gh')
        try:
            digest = binary.with_name('gh.sha256').read_text().strip()
        except OSError:
            digest = ''
        state = Path(os.environ.get('LAZYMIND_STATE_SQLITE_DIR') or Path.home() / '.lazymind/auth-state')
        return cls(binary_path=binary, expected_sha256=digest, runtime_dir=state / 'github-cli')

    @staticmethod
    def _command(args, **kwargs):
        try:
            return subprocess.run(args, capture_output=True, text=True, timeout=35, **kwargs)
        except (OSError, subprocess.SubprocessError):
            raise GitHubCLIError('GITHUB_CLI_UNAVAILABLE') from None

    def probe(self):
        token = os.environ.get('GH_TOKEN') or os.environ.get('GITHUB_TOKEN')
        if token:
            return {'access_token': token}
        if sys.platform != 'darwin':
            raise GitHubCLIError('GITHUB_CLI_PLATFORM_UNSUPPORTED')
        host_home = os.environ.get('LAZYMIND_HOST_HOME') or os.environ.get('HOME')
        if not host_home and not os.environ.get('GH_CONFIG_DIR'):
            raise GitHubCLIError('GITHUB_CREDENTIAL_UNREADABLE')
        config_dir = Path(os.environ.get('LAZYMIND_GITHUB_HOST_CONFIG_DIR') or os.environ.get('GH_CONFIG_DIR') or (
            Path(os.environ['XDG_CONFIG_HOME']) / 'gh'
            if os.environ.get('XDG_CONFIG_HOME') and not os.environ.get('LAZYMIND_HOST_HOME')
            else Path(host_home or Path.home()) / '.config/gh'
        ))
        # Runtime services have a private HOME. Only read-only credential probes use the host HOME;
        # the isolated authorization process keeps its service HOME and RAM-backed GH_CONFIG_DIR.
        probe_env = dict(os.environ)
        if host_home:
            probe_env['HOME'] = host_home
        try:
            path = config_dir / 'hosts.yml'
            config = yaml.safe_load(path.read_text()) if path.exists() else {}
            host = (config or {}).get('github.com') or {}
            if not isinstance(host, dict):
                raise ValueError()
            user = host.get('user') or ''
            users = host.get('users') or {}
            if not isinstance(user, str) or not isinstance(users, dict):
                raise ValueError()
            if not user and len(users) > 1:
                raise GitHubCLIError('GITHUB_ACCOUNT_SELECTION_REQUIRED')
            if not user and users:
                user = next(iter(users))
            # Plaintext credentials may exist from an earlier gh login. Read, never rewrite them.
            configured = (users.get(user) or {}).get('oauth_token') or host.get('oauth_token')
            if configured:
                if not isinstance(configured, str):
                    raise ValueError()
                return {'access_token': configured}
        except GitHubCLIError:
            raise
        except (OSError, ValueError, TypeError, AttributeError, yaml.YAMLError):
            raise GitHubCLIError('GITHUB_CREDENTIAL_UNREADABLE') from None
        result = self._command(['/usr/bin/security', 'find-generic-password', '-s', 'gh:github.com', '-wa', user],
                               env=probe_env)
        if result.returncode == 0:
            try:
                token = result.stdout.strip()
                if token.startswith('go-keyring-base64:'):
                    token = base64.b64decode(token.split(':', 1)[1], validate=True).decode()
                elif token.startswith('go-keyring-encoded:'):
                    token = bytes.fromhex(token.split(':', 1)[1]).decode()
                if not token:
                    raise ValueError()
                return {'access_token': token}
            except ValueError:
                raise GitHubCLIError('GITHUB_CREDENTIAL_UNREADABLE') from None
        if result.returncode != 44:
            raise GitHubCLIError('GITHUB_CREDENTIAL_UNREADABLE')
        result = self._command(
            ['/usr/bin/git', 'credential-osxkeychain', 'get'],
            input='protocol=https\nhost=github.com\n\n',
            env=probe_env,
        )
        if result.returncode != 0:
            raise GitHubCLIError('GITHUB_CREDENTIAL_UNREADABLE')
        if not result.stdout.strip():
            return None
        try:
            fields = dict(line.split('=', 1) for line in result.stdout.splitlines() if line)
            token = fields['password']
            if not token:
                raise ValueError()
            return {'access_token': token}
        except (KeyError, ValueError):
            raise GitHubCLIError('GITHUB_CREDENTIAL_UNREADABLE') from None

    def _verify_binary(self):
        if sys.platform != 'darwin':
            raise GitHubCLIError('GITHUB_CLI_PLATFORM_UNSUPPORTED')
        try:
            valid = re.fullmatch('[0-9a-f]{64}', self.digest) and (
                hashlib.sha256(self.binary.read_bytes()).hexdigest() == self.digest
            )
        except OSError:
            valid = False
        if not valid:
            raise GitHubCLIError('GITHUB_CLI_INTEGRITY_MISMATCH')
        result = self._command([str(self.binary), '--version'])
        if result.returncode or not result.stdout.startswith(f'gh version {GH_VERSION} '):
            raise GitHubCLIError('GITHUB_CLI_VERSION_UNSUPPORTED')

    @staticmethod
    def _environment(config):
        env = {k: v for k, v in os.environ.items() if not k.startswith(('GH_', 'GITHUB_', 'GIT_CONFIG_'))}
        for key in ('GIT_EXEC_PATH', 'GIT_ASKPASS', 'SSH_ASKPASS'):
            env.pop(key, None)
        env.update(
            GH_CONFIG_DIR=str(config), GH_CACHE_DIR=str(config / 'cache'), GH_PROMPT_DISABLED='1',
            GH_NO_UPDATE_NOTIFIER='1', GH_NO_EXTENSION_UPDATE_NOTIFIER='1', GH_BROWSER='/usr/bin/true',
            GIT_CONFIG_GLOBAL=str(config / 'gitconfig'), GIT_CONFIG_NOSYSTEM='1', GIT_TERMINAL_PROMPT='0',
            NO_COLOR='1',
        )
        return env

    def _cli(self, args, env):
        return self._command(['/usr/bin/sandbox-exec', '-p', PROFILE, str(self.binary), *args], env=env)

    def _prepare_root(self):
        self.root.mkdir(mode=0o700, parents=True, exist_ok=True)
        info = self.root.lstat()
        if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.getuid() or info.st_mode & 0o077:
            raise GitHubCLIError('GITHUB_CLI_STORAGE_UNSAFE')

    def _image_for_device(self, device):
        result = self._command(['/usr/bin/hdiutil', 'info', '-plist'])
        if result.returncode:
            raise GitHubCLIError('GITHUB_CLI_RECOVERY_REQUIRED')
        try:
            matching = [(image, entry) for image in plistlib.loads(result.stdout.encode()).get('images', [])
                        for entry in image.get('system-entities', []) if entry.get('dev-entry') == device]
            if len(matching) > 1:
                raise ValueError()
            return matching[0] if matching else None
        except (ValueError, TypeError, AttributeError):
            raise GitHubCLIError('GITHUB_CLI_RECOVERY_REQUIRED') from None

    def _image_identity(self, image):
        pid = image.get('hdid-pid')
        if (image.get('image-path') != 'ram://65536' or image.get('owner-uid') != os.getuid()
                or not isinstance(pid, int) or pid <= 0):
            raise GitHubCLIError('GITHUB_CLI_RECOVERY_REQUIRED')
        result = self._command(['/bin/ps', '-p', str(pid), '-o', 'lstart='],
                               env=dict(os.environ, LC_ALL='C', TZ='UTC'))
        started_at = result.stdout.strip()
        if result.returncode or not started_at:
            raise GitHubCLIError('GITHUB_CLI_RECOVERY_REQUIRED')
        return {'pid': pid, 'started_at': started_at}

    def recover(self):
        import fcntl
        self._prepare_root()
        for directory in self.root.glob('session-*'):
            if not re.fullmatch('session-[0-9a-f]{32}', directory.name) or directory.is_symlink():
                raise GitHubCLIError('GITHUB_CLI_RECOVERY_REQUIRED')
            lease_path = directory / 'lease.json'
            try:
                fd = os.open(lease_path, os.O_RDWR | os.O_NOFOLLOW)
                with os.fdopen(fd, 'r+') as lease:
                    fcntl.flock(lease.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
                    info = os.fstat(lease.fileno())
                    if info.st_uid != os.getuid() or info.st_mode & 0o077:
                        raise ValueError()
                    data = json.load(lease)
                    device, volume = data.get('device'), data.get('volume')
                    mount = directory / 'ram'
                    if not device:
                        if mount.is_mount():
                            raise ValueError()
                        shutil.rmtree(directory)
                        continue
                    if not re.fullmatch('/dev/disk[0-9]+', device) or volume != 'LMgh-' + directory.name[8:24]:
                        raise ValueError()
                    matching = self._image_for_device(device)
                    if matching:
                        image, entry = matching
                        actual_mount = entry.get('mount-point')
                        if data.get('identity') != self._image_identity(image):
                            raise ValueError()
                        if actual_mount:
                            if Path(actual_mount).resolve() != mount.resolve():
                                raise ValueError()
                        elif mount.is_mount():
                            raise ValueError()
                        self._cleanup(device, mount, bool(actual_mount))
                    elif mount.is_mount():
                        raise ValueError()
                    shutil.rmtree(directory)
            except BlockingIOError:
                raise GitHubCLIError('GITHUB_CLI_BUSY') from None
            except GitHubCLIError:
                raise
            except (OSError, ValueError, TypeError, KeyError):
                raise GitHubCLIError('GITHUB_CLI_RECOVERY_REQUIRED') from None

    def _cleanup(self, device, mount, mounted):
        if mounted:
            # macOS filesystem metadata services can briefly keep a newly mounted volume busy.
            for delay in (0, 0.25, 1, 2):
                if delay:
                    time.sleep(delay)
                if self._command(['/sbin/umount', str(mount)]).returncode == 0:
                    break
            else:
                raise GitHubCLIError('GITHUB_CLI_CLEANUP_FAILED')
        if device and self._command(['/usr/bin/hdiutil', 'detach', device]).returncode:
            raise GitHubCLIError('GITHUB_CLI_CLEANUP_FAILED')

    def authorize(self, *, cancel_event, on_device):
        self._verify_binary()
        with self._lock:
            if cancel_event.is_set():
                raise GitHubCLIError('GITHUB_AUTH_CANCELLED')
            return self._authorize(cancel_event, on_device)

    def _authorize(self, cancel_event, on_device):
        import fcntl
        blocked = self._command(['/usr/bin/sandbox-exec', '-p', PROFILE, '/usr/bin/security', 'help'])
        if blocked.returncode != 71:
            raise GitHubCLIError('GITHUB_CLI_ISOLATION_UNAVAILABLE')
        self.recover()
        directory = self.root / ('session-' + uuid.uuid4().hex)
        directory.mkdir(mode=0o700)
        mount = directory / 'ram'
        mount.mkdir(mode=0o700)
        volume = 'LMgh-' + directory.name[8:24]
        fd = os.open(directory / 'lease.json', os.O_RDWR | os.O_CREAT | os.O_EXCL, 0o600)
        device, mounted, process, identity = None, False, None, None
        with os.fdopen(fd, 'w+') as lease:
            fcntl.flock(lease.fileno(), fcntl.LOCK_EX)

            def save_lease():
                lease.seek(0)
                json.dump({'device': device, 'volume': volume, 'identity': identity}, lease)
                lease.truncate()
                lease.flush()
                os.fsync(lease.fileno())
            save_lease()
            try:
                attached = self._command(['/usr/bin/hdiutil', 'attach', '-nomount', 'ram://65536'])
                if attached.returncode or not re.fullmatch('/dev/disk[0-9]+', attached.stdout.strip()):
                    raise GitHubCLIError('GITHUB_CLI_STORAGE_UNAVAILABLE')
                device = attached.stdout.strip()
                save_lease()
                image = self._image_for_device(device)
                if image is None:
                    raise GitHubCLIError('GITHUB_CLI_STORAGE_UNAVAILABLE')
                identity = self._image_identity(image[0])
                save_lease()
                if self._command(['/sbin/newfs_hfs', '-v', volume, device]).returncode:
                    raise GitHubCLIError('GITHUB_CLI_STORAGE_UNAVAILABLE')
                if self._command(['/sbin/mount_hfs', device, str(mount)]).returncode:
                    raise GitHubCLIError('GITHUB_CLI_STORAGE_UNAVAILABLE')
                mounted = True
                mount.chmod(0o700)
                config = mount / 'gh'
                config.mkdir(mode=0o700)
                env = self._environment(config)
                empty = self._cli(['auth', 'token', '--hostname', 'github.com'], env)
                if empty.returncode == 0 or empty.stdout.strip():
                    raise GitHubCLIError('GITHUB_CLI_ISOLATION_UNAVAILABLE')
                if cancel_event.is_set():
                    raise GitHubCLIError('GITHUB_AUTH_CANCELLED')
                process = subprocess.Popen(
                    ['/usr/bin/sandbox-exec', '-p', PROFILE, str(self.binary), 'auth', 'login',
                     '--hostname', 'github.com', '--git-protocol', 'https', '--web',
                     '--skip-ssh-key', '--clipboard=false', '--insecure-storage'],
                    stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                    text=True, env=env, cwd=directory, pass_fds=(lease.fileno(),),
                )
                self._wait_login(process, cancel_event, on_device)
                exported = self._cli(['auth', 'token', '--hostname', 'github.com'], env)
                token = exported.stdout.strip()
                if exported.returncode or not token or len(token) > 4096 or any(c.isspace() for c in token):
                    raise GitHubCLIError('GITHUB_CLI_OUTPUT_INVALID')
            except (OSError, subprocess.SubprocessError):
                raise GitHubCLIError('GITHUB_CLI_UNAVAILABLE') from None
            finally:
                if process is not None and process.poll() is None:
                    process.kill()
                    process.wait(timeout=15)
                self._cleanup(device, mount, mounted)
                shutil.rmtree(directory)
        return {'access_token': token}

    @staticmethod
    def _wait_login(process, cancel_event, on_device):
        deadline = time.monotonic() + AUTH_TIMEOUT
        stopped = threading.Event()
        lines = queue.Queue(maxsize=32)

        def read_lines():
            while not stopped.is_set():
                line = process.stdout.readline(4097)
                while not stopped.is_set():
                    try:
                        lines.put(line, timeout=0.1)
                        break
                    except queue.Full:
                        continue
                if not line:
                    return

        def watch():
            while not stopped.wait(0.1):
                if cancel_event.is_set() or time.monotonic() >= deadline:
                    if process.poll() is None:
                        process.kill()
                    return
        reader = threading.Thread(target=read_lines, daemon=True)
        watcher = threading.Thread(target=watch, daemon=True)
        reader.start()
        watcher.start()
        try:
            while True:
                if cancel_event.is_set():
                    raise GitHubCLIError('GITHUB_AUTH_CANCELLED')
                if time.monotonic() >= deadline:
                    raise GitHubCLIError('GITHUB_AUTH_TIMEOUT')
                try:
                    line = lines.get(timeout=0.1)
                except queue.Empty:
                    continue
                if not line:
                    break
                code = re.search(r'one-time code:\s*([A-Z0-9]{4}-[A-Z0-9]{4})', line)
                if code:
                    on_device(code.group(1), DEVICE_URL, AUTH_TIMEOUT)
            try:
                result = process.wait(timeout=max(0.1, deadline - time.monotonic()))
            except subprocess.TimeoutExpired:
                raise GitHubCLIError('GITHUB_AUTH_TIMEOUT') from None
            if cancel_event.is_set():
                raise GitHubCLIError('GITHUB_AUTH_CANCELLED')
            if result:
                raise GitHubCLIError('GITHUB_AUTH_TIMEOUT' if time.monotonic() >= deadline else 'GITHUB_AUTH_FAILED')
        finally:
            stopped.set()
            if process.poll() is None:
                process.kill()
                process.wait(timeout=15)
            reader.join(timeout=1)
            watcher.join(timeout=1)
            process.stdout.close()
