"""Run bounded Python children using the platform's isolated execution backend."""
from __future__ import annotations

import ctypes
import importlib.util
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time


class ContainerEnvironmentError(RuntimeError):
    """A deployment problem; never send this to the model for code repair."""


def validate_environment():
    if sys.platform == 'win32':
        from . import windows
        try:
            windows.validate_environment()
        except (OSError, AttributeError) as exc:
            raise ContainerEnvironmentError('Windows Python 沙箱不可用: ' + str(exc)) from exc
    elif sys.platform == 'darwin':
        from . import macos
        try:
            macos.validate_environment()
        except (OSError, AttributeError) as exc:
            raise ContainerEnvironmentError('macOS Python 沙箱不可用: ' + str(exc)) from exc
    elif sys.platform != 'linux' or not any(Path(p).exists() for p in ('/.dockerenv', '/run/.containerenv')):
        raise ContainerEnvironmentError('执行后端需要 Linux 服务容器、macOS Seatbelt 或 Windows AppContainer')
    missing = [name for name in ('numpy', 'scipy', 'pandas', 'matplotlib', 'seaborn')
               if importlib.util.find_spec(name) is None]
    if missing:
        raise ContainerEnvironmentError('Python 执行环境缺少科学计算依赖: ' + ', '.join(missing))
    if sys.platform in ('win32', 'darwin'):
        return
    try:
        seccomp = ctypes.CDLL('libseccomp.so.2')
        seccomp.seccomp_syscall_resolve_name.restype = ctypes.c_int
        number = seccomp.seccomp_syscall_resolve_name(b'landlock_create_ruleset')
        libc = ctypes.CDLL(None, use_errno=True)
        if number < 0 or libc.syscall(number, 0, 0, 1) < 3:
            raise OSError('Landlock ABI >= 3 required')
    except OSError as exc:
        raise ContainerEnvironmentError('容器内执行需要 libseccomp2 和支持 Landlock ABI >= 3 的 Linux 内核') from exc


def execute(payload, worker: Path, timeout: int, *, label: str) -> dict:
    """Keep only stdin/stdout/stderr open; never inherit Chat credentials or Python state."""
    launcher = Path(__file__).with_name('sandbox.py').resolve()
    with tempfile.TemporaryDirectory(prefix='lazymind-paper-') as directory:
        root = Path(directory)
        if sys.platform != 'win32':
            root.chmod(0o711)
        scratch = root / 'work'
        scratch.mkdir(mode=0o700)
        # The child drops root before executing generated code. Other jobs' scratch is private.
        if sys.platform != 'win32' and os.geteuid() == 0:
            os.chown(scratch, 65534, 65534)
        paths = [root / name for name in ('input.json', 'stdout', 'stderr')]
        paths[0].write_text(json.dumps(payload, ensure_ascii=False, allow_nan=False), encoding='utf-8')
        env = {'PATH': '/usr/local/bin:/usr/bin:/bin', 'HOME': str(scratch), 'TMPDIR': str(scratch),
               'MPLCONFIGDIR': str(scratch / 'matplotlib'), 'MPLBACKEND': 'Agg',
               'OPENBLAS_NUM_THREADS': '1', 'OMP_NUM_THREADS': '1', 'MKL_NUM_THREADS': '1',
               'NUMEXPR_NUM_THREADS': '1', 'LANG': 'C.UTF-8'}
        command = [sys.executable, '-I', '-B', str(launcher), str(worker.resolve()), str(scratch), str(timeout)]
        process = None
        try:
            with paths[0].open('rb') as source, paths[1].open('wb') as out, paths[2].open('wb') as err:
                if sys.platform == 'win32':
                    from . import windows
                    process = windows.start(worker, scratch, timeout, (source, out, err), env)
                elif sys.platform == 'darwin':
                    from . import macos
                    process = macos.start(worker, scratch, timeout, (source, out, err), env)
                else:
                    process = subprocess.Popen(command, stdin=source, stdout=out, stderr=err, env=env,
                                               cwd=scratch, close_fds=True, start_new_session=True)
                deadline = time.monotonic() + timeout
                while process.poll() is None:
                    if sys.platform == 'win32':
                        process.check_storage()
                    elif sys.platform == 'darwin':
                        macos.check_limits(process, scratch)
                    if time.monotonic() >= deadline:
                        raise RuntimeError(f'{label}执行超时（{timeout} 秒）')
                    if any(p.stat().st_size > 16 * 1024 * 1024 for p in paths[1:]):
                        raise RuntimeError(f'{label}输出超过 16 MiB')
                    time.sleep(0.05)
                if sys.platform == 'win32':
                    process.check_storage()  # Include children that exit before the first polling interval.
                elif sys.platform == 'darwin':
                    macos.check_limits(process, scratch)
            if any(p.stat().st_size > 16 * 1024 * 1024 for p in paths[1:]):
                raise RuntimeError(f'{label}输出超过 16 MiB')
            stderr = paths[2].read_text(encoding='utf-8', errors='replace')[-3000:]
            if process.returncode == 78:
                raise ContainerEnvironmentError('Python 沙箱启动失败: ' + stderr)
            try:
                result = json.loads(paths[1].read_text(encoding='utf-8'))
            except ValueError as exc:
                raise RuntimeError(f'{label}没有返回有效 JSON: ' + stderr) from exc
            if not isinstance(result, dict) or process.returncode or 'error' in result:
                detail = result.get('error', stderr) if isinstance(result, dict) else stderr
                if isinstance(result, dict):
                    source = payload.get('code', '').splitlines()
                    for frame in result.get('frames', []):
                        line = frame['line']
                        context = '\n'.join(
                            f'{"→" if index + 1 == line else " "} {index + 1}: {source[index][:500]}'
                            for index in range(max(0, line - 3), min(len(source), line + 2)))
                        detail += f'\nFile "{frame["file"]}", line {line}, in {frame["function"]}\n{context}'
                raise RuntimeError(f'{label}执行失败: ' + str(detail)[-6000:])
            return result
        except OSError as exc:
            raise ContainerEnvironmentError('Python 沙箱执行器不可用: ' + str(exc)) from exc
        finally:
            if process is not None:
                if sys.platform == 'win32':
                    process.close()
                else:
                    try:
                        os.killpg(process.pid, signal.SIGKILL)
                    except ProcessLookupError:
                        pass
                    process.wait(timeout=5)
