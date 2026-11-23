"""OS primitives for the CI harness; only manage this invocation's files/processes."""
from __future__ import annotations

import contextlib
import errno
import os
import shlex
import signal
import subprocess
import sys
import time
from pathlib import Path

IS_WINDOWS = os.name == 'nt'


def executable_name(name):
    return name + '.exe' if IS_WINDOWS else name


def connector_command(path):
    """Explicit Python transport fixtures/wrappers are portable executable commands."""
    path = str(path)
    return [sys.executable, path] if Path(path).suffix.lower() == '.py' else [path]


def shell_command(arguments):
    if IS_WINDOWS:
        return '& ' + ' '.join("'" + str(arg).replace("'", "''") + "'" for arg in arguments)
    return shlex.join(map(str, arguments))


@contextlib.contextmanager
def file_lock(path, *, blocking=True, timeout=300):
    """Lock the same byte on Windows, or the whole file with flock on POSIX."""
    with Path(path).open('a+b') as stream:
        if IS_WINDOWS:
            import msvcrt
            if stream.seek(0, os.SEEK_END) == 0:
                stream.write(b'\0')
                stream.flush()
            deadline = time.monotonic() + timeout
            while True:
                stream.seek(0)
                try:
                    msvcrt.locking(stream.fileno(), msvcrt.LK_NBLCK, 1)
                    break
                except OSError as error:
                    if error.errno not in (errno.EACCES, errno.EAGAIN, errno.EDEADLK):
                        raise
                    if not blocking:
                        raise BlockingIOError(errno.EWOULDBLOCK, 'CI file is already locked') from error
                    if time.monotonic() >= deadline:
                        raise TimeoutError('CI file lock timed out') from error
                    time.sleep(.05)
            try:
                yield
            finally:
                stream.seek(0)
                msvcrt.locking(stream.fileno(), msvcrt.LK_UNLCK, 1)
        else:
            import fcntl
            fcntl.flock(stream, fcntl.LOCK_EX | (0 if blocking else fcntl.LOCK_NB))
            try:
                yield
            finally:
                fcntl.flock(stream, fcntl.LOCK_UN)


class ProcessTree:
    """Own a POSIX process group or Windows kill-on-close Job Object."""
    def __init__(self, command, **kwargs):
        self.job = None
        if IS_WINDOWS:
            kwargs['creationflags'] = subprocess.CREATE_NEW_PROCESS_GROUP
        else:
            kwargs['start_new_session'] = True
        self.process = subprocess.Popen(command, **kwargs)
        if IS_WINDOWS:
            try:
                self._create_windows_job()
            except Exception:
                self.process.terminate()
                self.process.wait(timeout=8)
                for stream in (self.process.stdin, self.process.stdout, self.process.stderr):
                    if stream:
                        stream.close()
                raise

    def _create_windows_job(self):
        import ctypes
        from ctypes import wintypes

        class BasicLimits(ctypes.Structure):
            _fields_ = [('PerProcessUserTimeLimit', ctypes.c_longlong), ('PerJobUserTimeLimit', ctypes.c_longlong),
                        ('LimitFlags', wintypes.DWORD), ('MinimumWorkingSetSize', ctypes.c_size_t),
                        ('MaximumWorkingSetSize', ctypes.c_size_t), ('ActiveProcessLimit', wintypes.DWORD),
                        ('Affinity', ctypes.c_size_t), ('PriorityClass', wintypes.DWORD),
                        ('SchedulingClass', wintypes.DWORD)]

        class IOCounters(ctypes.Structure):
            _fields_ = [(name, ctypes.c_ulonglong) for name in ('ReadOperationCount', 'WriteOperationCount',
                         'OtherOperationCount', 'ReadTransferCount', 'WriteTransferCount', 'OtherTransferCount')]

        class ExtendedLimits(ctypes.Structure):
            _fields_ = [('BasicLimitInformation', BasicLimits), ('IoInfo', IOCounters),
                        ('ProcessMemoryLimit', ctypes.c_size_t), ('JobMemoryLimit', ctypes.c_size_t),
                        ('PeakProcessMemoryUsed', ctypes.c_size_t), ('PeakJobMemoryUsed', ctypes.c_size_t)]

        kernel = ctypes.WinDLL('kernel32', use_last_error=True)
        kernel.CreateJobObjectW.argtypes = (ctypes.c_void_p, wintypes.LPCWSTR)
        kernel.CreateJobObjectW.restype = wintypes.HANDLE
        kernel.SetInformationJobObject.argtypes = (wintypes.HANDLE, ctypes.c_int, ctypes.c_void_p, wintypes.DWORD)
        kernel.SetInformationJobObject.restype = wintypes.BOOL
        kernel.AssignProcessToJobObject.argtypes = (wintypes.HANDLE, wintypes.HANDLE)
        kernel.AssignProcessToJobObject.restype = wintypes.BOOL
        kernel.CloseHandle.argtypes = (wintypes.HANDLE,)
        kernel.CloseHandle.restype = wintypes.BOOL
        job = kernel.CreateJobObjectW(None, None)
        if not job:
            raise OSError('Unable to create a Windows CI Job Object')
        limits = ExtendedLimits()
        limits.BasicLimitInformation.LimitFlags = 0x2000  # JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
        if not kernel.SetInformationJobObject(job, 9, ctypes.byref(limits), ctypes.sizeof(limits)):
            kernel.CloseHandle(job)
            raise OSError('Unable to set Windows CI process cleanup policy')
        if not kernel.AssignProcessToJobObject(job, wintypes.HANDLE(int(self.process._handle))):
            kernel.CloseHandle(job)
            raise OSError('Unable to assign the Windows Agent process to its CI Job Object')
        self.job, self.kernel = job, kernel

    def stop(self, *, force=False):
        process = self.process
        if IS_WINDOWS:
            if force:
                self.close()
            elif process.poll() is None:
                try:
                    process.send_signal(signal.CTRL_BREAK_EVENT)
                except OSError:
                    process.terminate()
            return
        try:
            os.killpg(process.pid, signal.SIGKILL if force else signal.SIGTERM)
        except ProcessLookupError:
            pass
        except PermissionError:
            if process.poll() is None:
                process.kill() if force else process.terminate()

    def close(self):
        if IS_WINDOWS:
            if self.job:
                self.kernel.CloseHandle(self.job)
                self.job = None
        else:
            self.stop()
