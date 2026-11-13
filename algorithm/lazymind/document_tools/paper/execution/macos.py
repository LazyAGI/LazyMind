"""Seatbelt isolation and parent-enforced budgets for native macOS Python jobs."""
from __future__ import annotations

import ctypes
import errno
from functools import cache
import json
import os
from pathlib import Path
import stat
import subprocess
import sys
import sysconfig


FILE_LIMIT = 16 * 1024 ** 2
STORAGE_LIMIT = 64 * 1024 ** 2
MEMORY_LIMIT = 1024 ** 3
ENTRY_LIMIT = 1024
PROC_PIDTASKINFO = 4


@cache
def _sandbox_api():
    library = ctypes.CDLL('/usr/lib/libsandbox.dylib', use_errno=True)
    library.sandbox_init.argtypes = [ctypes.c_char_p, ctypes.c_uint64, ctypes.POINTER(ctypes.c_char_p)]
    library.sandbox_init.restype = ctypes.c_int
    library.sandbox_free_error.argtypes = [ctypes.c_char_p]
    library.sandbox_free_error.restype = None
    return library


class TaskInfo(ctypes.Structure):
    # PROC_PIDTASKINFO from <sys/proc_info.h>.
    _fields_ = [(name, ctypes.c_uint64) for name in (
        'virtual_size', 'resident_size', 'total_user', 'total_system', 'threads_user', 'threads_system',
    )] + [(name, ctypes.c_int32) for name in (
        'policy', 'faults', 'pageins', 'cow_faults', 'messages_sent', 'messages_received',
        'syscalls_mach', 'syscalls_unix', 'context_switches', 'thread_count', 'running_count', 'priority',
    )]


@cache
def _process_info():
    function = ctypes.CDLL('/usr/lib/libproc.dylib', use_errno=True).proc_pidinfo
    function.argtypes = [ctypes.c_int, ctypes.c_int, ctypes.c_uint64, ctypes.c_void_p, ctypes.c_int]
    function.restype = ctypes.c_int
    return function


def validate_environment():
    _sandbox_api()
    _process_info()


def _profile(scratch: Path) -> str:
    # Grant scientific packages and Python's runtime, never application/data roots.
    roots = {
        '/System', '/usr/lib', '/usr/share/locale', '/Library/Fonts', '/private/var/db/timezone',
        *(sysconfig.get_path(name) for name in ('stdlib', 'platstdlib', 'purelib', 'platlib')),
    }
    readable = '\n'.join(f'    (subpath {json.dumps(str(Path(root).resolve()), ensure_ascii=False)})'
                         for root in sorted(roots))
    return f'''(version 1)
(deny default)
(allow file-read-metadata)
(allow sysctl-read)
(allow signal (target self))
(allow mach-lookup (global-name "com.apple.system.opendirectoryd.libinfo"))
(allow file-read* file-map-executable
{readable})
(allow file-read* (literal "/dev/urandom") (literal "/dev/random") (literal "/etc/localtime"))
(allow file-read* file-write* (literal "/dev/null") (subpath {json.dumps(str(scratch.resolve()), ensure_ascii=False)}))
'''


def start(worker: Path, scratch: Path, timeout: int, streams, env):
    profile = scratch.parent / 'sandbox.sb'
    profile.write_text(_profile(scratch), encoding='utf-8')
    command = [sys.executable, '-I', '-B', str(Path(__file__).resolve()),
               str(worker.resolve()), str(profile), str(timeout)]
    return subprocess.Popen(command, stdin=streams[0], stdout=streams[1], stderr=streams[2],
                            cwd=scratch, env=env, close_fds=True, start_new_session=True)


def check_limits(process, scratch: Path):
    """Polling bounds resident memory and scratch storage, allowing brief overshoot."""
    info = TaskInfo()
    size = ctypes.sizeof(info)
    if _process_info()(process.pid, PROC_PIDTASKINFO, 0, ctypes.byref(info), size) != size:
        if process.poll() is None:
            raise OSError(ctypes.get_errno() or errno.EIO, '无法读取 macOS Python 沙箱内存用量')
    elif info.resident_size > MEMORY_LIMIT:
        raise RuntimeError('macOS Python 沙箱内存超过 1 GiB')
    pending, total, count = [scratch], 0, 0
    while pending:
        with os.scandir(pending.pop()) as entries:
            for entry in entries:
                count += 1
                if count > ENTRY_LIMIT:
                    raise RuntimeError('macOS Python 沙箱临时文件数量超过限制')
                try:
                    info = entry.stat(follow_symlinks=False)
                except FileNotFoundError:
                    continue
                if stat.S_ISLNK(info.st_mode):
                    raise RuntimeError('macOS Python 沙箱临时目录不允许符号链接')
                if stat.S_ISDIR(info.st_mode):
                    pending.append(entry.path)
                else:
                    if info.st_size > FILE_LIMIT:
                        raise RuntimeError('macOS Python 沙箱临时文件超过 16 MiB')
                    total += info.st_size
                    if total > STORAGE_LIMIT:
                        raise RuntimeError('macOS Python 沙箱临时存储合计超过 64 MiB')


def _restrict(profile: str, timeout: int):
    import resource

    resource.setrlimit(resource.RLIMIT_CORE, (0, 0))
    resource.setrlimit(resource.RLIMIT_CPU, (timeout, timeout))
    resource.setrlimit(resource.RLIMIT_FSIZE, (FILE_LIMIT, FILE_LIMIT))
    resource.setrlimit(resource.RLIMIT_NOFILE, (64, 64))
    library = _sandbox_api()
    error = ctypes.c_char_p()
    if library.sandbox_init(profile.encode('utf-8'), 0, ctypes.byref(error)):
        try:
            detail = error.value.decode('utf-8', errors='replace') if error.value else 'sandbox_init failed'
        finally:
            if error.value:
                library.sandbox_free_error(error)
        raise OSError(detail)


if __name__ == '__main__':
    worker, profile, timeout = sys.argv[1:]
    try:
        # Load the trusted runner before restricting access to application sources.
        program = compile(Path(worker).read_text(encoding='utf-8'), worker, 'exec')
        _restrict(Path(profile).read_text(encoding='utf-8'), int(timeout))
    except Exception as exc:
        print(f'{type(exc).__name__}: {exc}', file=sys.stderr)
        sys.exit(78)
    sys.argv = [worker]
    exec(program, {'__name__': '__main__', '__file__': worker})
