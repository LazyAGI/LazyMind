"""Native Windows execution: per-job AppContainer, no network, one-process Job Object.

Only the Python runtime gets a shared read capability; each job has a distinct
package SID and private writable scratch. No service source or credentials are
passed to the child. Win32 bindings are lazy so other platforms can import this
module. See Microsoft's "Launch an AppContainer" for the startup attribute ABI.
"""
from __future__ import annotations

import ctypes as ct
from contextlib import ExitStack
import logging
import os
from pathlib import Path
import shutil
import subprocess
import sys
import sysconfig
import tempfile
import threading
import uuid


DWORD, WORD, BOOL = ct.c_uint32, ct.c_uint16, ct.c_int32
HANDLE, SIZE_T = ct.c_void_p, ct.c_size_t
PVOID, PWSTR = ct.c_void_p, ct.c_wchar_p
JOB_OBJECT_LIMIT_PROCESS_TIME = 0x2
JOB_OBJECT_LIMIT_ACTIVE_PROCESS = 0x8
JOB_OBJECT_LIMIT_PROCESS_MEMORY = 0x100
JOB_OBJECT_LIMIT_DIE_ON_UNHANDLED_EXCEPTION = 0x400
JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE = 0x2000
LOG = logging.getLogger(__name__)


class StartupInfo(ct.Structure):
    _fields_ = [('cb', DWORD), ('reserved', PWSTR), ('desktop', PWSTR), ('title', PWSTR),
                ('x', DWORD), ('y', DWORD), ('xsize', DWORD), ('ysize', DWORD),
                ('xchars', DWORD), ('ychars', DWORD), ('fill', DWORD), ('flags', DWORD),
                ('show', WORD), ('reserved_size', WORD), ('reserved_data', PVOID),
                ('stdin', HANDLE), ('stdout', HANDLE), ('stderr', HANDLE)]


class StartupInfoEx(ct.Structure):
    _fields_ = [('info', StartupInfo), ('attributes', PVOID)]


class ProcessInfo(ct.Structure):
    _fields_ = [('process', HANDLE), ('thread', HANDLE), ('pid', DWORD), ('tid', DWORD)]


class SidAndAttributes(ct.Structure):
    _fields_ = [('sid', PVOID), ('attributes', DWORD)]


class SecurityCapabilities(ct.Structure):
    _fields_ = [('sid', PVOID), ('capabilities', ct.POINTER(SidAndAttributes)),
                ('count', DWORD), ('reserved', DWORD)]


class BasicLimits(ct.Structure):
    _fields_ = [('process_time', ct.c_int64), ('job_time', ct.c_int64), ('flags', DWORD),
                ('min_working_set', SIZE_T), ('max_working_set', SIZE_T), ('active_processes', DWORD),
                ('affinity', SIZE_T), ('priority', DWORD), ('scheduling', DWORD)]


class ExtendedLimits(ct.Structure):
    _fields_ = [('basic', BasicLimits), ('io', ct.c_uint64 * 6), ('process_memory', SIZE_T),
                ('job_memory', SIZE_T), ('peak_process_memory', SIZE_T), ('peak_job_memory', SIZE_T)]


def _checked(value, function, _arguments):
    if not value:
        raise ct.WinError(ct.get_last_error())
    return value


class WinAPI:
    def __init__(self):
        kernel = ct.WinDLL('kernel32', use_last_error=True)
        base = ct.WinDLL('kernelbase', use_last_error=True)
        security = ct.WinDLL('advapi32', use_last_error=True)
        profiles = ct.WinDLL('userenv', use_last_error=True)

        def bind(library, name, arguments, result=BOOL, checked=True):
            function = getattr(library, name)
            function.argtypes, function.restype = arguments, result
            if checked:
                function.errcheck = _checked
            setattr(self, name, function)

        bind(kernel, 'CloseHandle', [HANDLE])
        bind(kernel, 'LocalFree', [PVOID], PVOID, False)
        bind(kernel, 'GetCurrentProcess', [], HANDLE, False)
        bind(kernel, 'GetSystemDirectoryW', [PWSTR, DWORD], DWORD)
        bind(kernel, 'CreateJobObjectW', [PVOID, PWSTR], HANDLE)
        bind(kernel, 'SetInformationJobObject', [HANDLE, ct.c_int, PVOID, DWORD])
        bind(kernel, 'AssignProcessToJobObject', [HANDLE, HANDLE])
        bind(kernel, 'TerminateJobObject', [HANDLE, DWORD])
        bind(kernel, 'TerminateProcess', [HANDLE, DWORD])
        bind(kernel, 'ResumeThread', [HANDLE], DWORD, False)
        bind(kernel, 'WaitForSingleObject', [HANDLE, DWORD], DWORD, False)
        bind(kernel, 'GetExitCodeProcess', [HANDLE, ct.POINTER(DWORD)])
        bind(kernel, 'DuplicateHandle', [HANDLE, HANDLE, HANDLE, ct.POINTER(HANDLE), DWORD, BOOL, DWORD])
        bind(kernel, 'InitializeProcThreadAttributeList', [PVOID, DWORD, DWORD, ct.POINTER(SIZE_T)], BOOL, False)
        bind(kernel, 'UpdateProcThreadAttribute', [PVOID, DWORD, SIZE_T, PVOID, SIZE_T, PVOID, PVOID])
        bind(kernel, 'DeleteProcThreadAttributeList', [PVOID], None, False)
        bind(kernel, 'CreateProcessW', [PWSTR, PWSTR, PVOID, PVOID, BOOL, DWORD, PVOID, PWSTR,
                                        ct.POINTER(StartupInfoEx), ct.POINTER(ProcessInfo)])
        bind(base, 'DeriveCapabilitySidsFromName',
             [PWSTR, ct.POINTER(PVOID), ct.POINTER(DWORD), ct.POINTER(PVOID), ct.POINTER(DWORD)])
        bind(security, 'ConvertSidToStringSidW', [PVOID, ct.POINTER(PWSTR)])
        bind(security, 'ConvertStringSecurityDescriptorToSecurityDescriptorW',
             [PWSTR, DWORD, ct.POINTER(PVOID), PVOID])
        bind(security, 'GetSecurityDescriptorDacl', [PVOID, ct.POINTER(BOOL), ct.POINTER(PVOID), ct.POINTER(BOOL)])
        bind(security, 'GetSecurityDescriptorSacl', [PVOID, ct.POINTER(BOOL), ct.POINTER(PVOID), ct.POINTER(BOOL)])
        bind(security, 'SetNamedSecurityInfoW', [PWSTR, ct.c_int, DWORD, PVOID, PVOID, PVOID, PVOID], DWORD, False)
        bind(security, 'OpenProcessToken', [HANDLE, DWORD, ct.POINTER(HANDLE)])
        bind(security, 'GetTokenInformation', [HANDLE, ct.c_int, PVOID, DWORD, ct.POINTER(DWORD)], BOOL, False)
        bind(security, 'FreeSid', [PVOID], PVOID, False)
        bind(profiles, 'CreateAppContainerProfile',
             [PWSTR, PWSTR, PWSTR, PVOID, DWORD, ct.POINTER(PVOID)], ct.c_int32, False)
        bind(profiles, 'DeleteAppContainerProfile', [PWSTR], ct.c_int32, False)
        directory = ct.create_unicode_buffer(32768)
        self.GetSystemDirectoryW(directory, len(directory))
        self.system = Path(directory.value)
        self.user_sid = self.current_user_sid()
        self.runtime_sid = self.derive_runtime_sid()
        self.runtime_sid_text = self.sid_text(self.runtime_sid)

    def sid_text(self, sid):
        value = PWSTR()
        self.ConvertSidToStringSidW(sid, ct.byref(value))
        try:
            return value.value
        finally:
            self.LocalFree(ct.cast(value, PVOID))

    def current_user_sid(self):
        token = HANDLE()
        self.OpenProcessToken(self.GetCurrentProcess(), 0x8, ct.byref(token))  # TOKEN_QUERY
        try:
            size = DWORD()
            self.GetTokenInformation(token, 1, None, 0, ct.byref(size))  # TokenUser
            buffer = ct.create_string_buffer(size.value)
            _checked(self.GetTokenInformation(token, 1, buffer, size, ct.byref(size)), None, None)
            return self.sid_text(ct.cast(buffer, ct.POINTER(SidAndAttributes)).contents.sid)
        finally:
            self.CloseHandle(token)

    def derive_runtime_sid(self):
        groups, capabilities = PVOID(), PVOID()
        group_count, count = DWORD(), DWORD()
        self.DeriveCapabilitySidsFromName('LazyMind.Paper.PythonRuntime', ct.byref(groups),
                                          ct.byref(group_count), ct.byref(capabilities), ct.byref(count))
        try:
            if count.value != 1:
                raise OSError('无法生成 Python 运行时的只读 capability')
            # This one SID is retained with the process-wide API instance.
            return ct.cast(capabilities, ct.POINTER(PVOID))[0]
        finally:
            for index in range(group_count.value):
                self.LocalFree(ct.cast(groups, ct.POINTER(PVOID))[index])
            self.LocalFree(groups)
            self.LocalFree(capabilities)

    def secure_directory(self, path, package_sid, *, writable=False):
        access = '0x1301bf' if writable else '0x1200a9'  # Modify or Read/Execute, never WRITE_DAC.
        descriptor = PVOID()
        sddl = (f'D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;{self.user_sid})'
                f'(A;OICI;{access};;;{package_sid})S:(ML;OICI;NW;;;LW)')
        self.ConvertStringSecurityDescriptorToSecurityDescriptorW(sddl, 1, ct.byref(descriptor), None)
        try:
            # Protect the DACL from parent grants; permit low-integrity writes only in this job.
            present, defaulted, dacl, sacl = BOOL(), BOOL(), PVOID(), PVOID()
            self.GetSecurityDescriptorDacl(descriptor, ct.byref(present), ct.byref(dacl), ct.byref(defaulted))
            self.GetSecurityDescriptorSacl(descriptor, ct.byref(present), ct.byref(sacl), ct.byref(defaulted))
            error = self.SetNamedSecurityInfoW(str(path), 1, 0x80000004 | 0x10, None, None, dacl, sacl)
            if error:
                raise ct.WinError(error)
        finally:
            self.LocalFree(descriptor)


_lock = threading.Lock()
_validation_lock = threading.Lock()
_api = None
_validated = False


def _runtime_entries():
    """Grant Python libraries, DLLs and .pth dependencies, never the service's PYTHONPATH."""
    entries = {}
    roots = {Path(sys.prefix), Path(sys.base_prefix), Path(sys.executable).parent,
             Path(getattr(sys, '_base_executable', None) or sys.executable).parent}
    for root in roots:
        entries[root] = False
        for child in root.iterdir():
            if child.is_file() and child.suffix.lower() in {'.exe', '.dll', '.pyd', '.zip', '.cfg', '._pth'}:
                entries[child] = False
        for name in ('Lib', 'DLLs'):
            if (root / name).is_dir():
                entries[root / name] = True
    for key in ('purelib', 'platlib'):
        site = Path(sysconfig.get_path(key))
        if site.is_dir():
            entries[site] = True
            for pth in site.glob('*.pth'):
                for line in pth.read_text(encoding='utf-8-sig').splitlines():
                    line = line.strip()
                    if line and not line.startswith(('#', 'import ', 'import\t')):
                        target = (site / line).resolve()
                        if target.is_dir():
                            entries[target] = True
    return entries


def _prepare_runtime():
    """Prepare a read-only capability once, without granting access to application data."""
    global _api
    with _lock:
        if _api is not None:
            return
        api = WinAPI()
        try:
            for path, recursive in _runtime_entries().items():
                rights = '(OI)(CI)(RX)' if recursive else '(RX)'
                try:
                    completed = subprocess.run([str(api.system / 'icacls.exe'), str(path), '/grant',
                                                f'*{api.runtime_sid_text}:{rights}', '/Q'],
                                               capture_output=True, creationflags=0x08000000, timeout=60)
                except subprocess.TimeoutExpired as exc:
                    raise OSError('Windows 沙箱的 Python 依赖权限准备超时') from exc
                # System-wide Python may already be readable by AppContainers while
                # its ACL is owned by an administrator. The actual launch still fails closed.
                if completed.returncode not in (0, 5):
                    raise OSError('无法授予 Windows 沙箱读取 Python 依赖的权限: ' + str(path))
        except BaseException:
            api.LocalFree(api.runtime_sid)
            raise
        _api = api


def validate_environment():
    """Probe the real restricted interpreter once; setup errors must not trigger model repair."""
    global _validated
    with _validation_lock:
        if _validated:
            return
        _prepare_runtime()
        from . import container
        with tempfile.TemporaryDirectory(prefix='paper-windows-probe-') as directory:
            worker = Path(directory) / 'probe.py'
            worker.write_text('import json, numpy, scipy, pandas, matplotlib, seaborn\n'
                              'print(json.dumps({"ready": True}))\n', encoding='utf-8')
            try:
                result = container.execute({}, worker, 60, label='Windows 沙箱环境探测')
            except (OSError, RuntimeError) as exc:
                raise OSError('受限 Python 无法启动或读取科学计算依赖: ' + str(exc)) from exc
            if result != {'ready': True}:
                raise OSError('Windows 沙箱环境探测返回异常')
        _validated = True


class Process:
    """Own both kernel handles and the profile until the child has stopped."""
    def __init__(self, api, profile, sid):
        self.api, self.profile, self.sid = api, profile, sid
        self.job = None
        self.info = ProcessInfo()
        self.returncode = None
        self.closed = False

    def poll(self):
        status = self.api.WaitForSingleObject(self.info.process, 0)
        if status == 0x102:  # WAIT_TIMEOUT; use wait state, since 259 can be a real exit code.
            return None
        if status != 0:
            raise ct.WinError(ct.get_last_error())
        result = DWORD()
        self.api.GetExitCodeProcess(self.info.process, ct.byref(result))
        self.returncode = result.value
        return self.returncode

    def close(self):
        if self.closed:
            return
        self.closed = True
        # Every resource is released even if terminating the child or closing one
        # handle fails. Register in reverse order: thread, process, job, SID, profile.
        with ExitStack() as cleanup:
            cleanup.callback(self._delete_profile)
            cleanup.callback(self.api.FreeSid, self.sid)
            for handle in (self.job, self.info.process, self.info.thread):
                if handle:
                    cleanup.callback(self.api.CloseHandle, handle)
            if self.info.process:
                # Also covers a failure to assign the suspended process to its job.
                try:
                    if self.poll() is None:
                        try:
                            self.api.TerminateProcess(self.info.process, 1)
                        except OSError:
                            # The process can exit between poll() and TerminateProcess().
                            if self.api.WaitForSingleObject(self.info.process, 0) != 0:
                                raise
                finally:
                    try:
                        if self.job:
                            self.api.TerminateJobObject(self.job, 1)
                    finally:
                        if self.api.WaitForSingleObject(self.info.process, 5000) != 0:
                            raise OSError('Windows 沙箱进程未能退出')

    def _delete_profile(self):
        result = self.api.DeleteAppContainerProfile(self.profile)
        if result < 0:
            LOG.warning('Windows AppContainer profile cleanup failed: %s (0x%08x)',
                        self.profile, result & 0xffffffff)


def _launch(process, command, scratch, streams, environment, timeout):
    import msvcrt

    api = process.api
    inherited, attributes = [], None
    try:
        for stream in streams:
            handle = HANDLE()
            current = api.GetCurrentProcess()
            api.DuplicateHandle(current, msvcrt.get_osfhandle(stream.fileno()), current,
                                ct.byref(handle), 0, True, 2)  # DUPLICATE_SAME_ACCESS
            inherited.append(handle.value)
        size = SIZE_T()
        api.InitializeProcThreadAttributeList(None, 2, 0, ct.byref(size))
        if not size.value:
            raise ct.WinError(ct.get_last_error())
        buffer = ct.create_string_buffer(size.value)
        _checked(api.InitializeProcThreadAttributeList(buffer, 2, 0, ct.byref(size)), None, None)
        attributes = ct.cast(buffer, PVOID)
        capabilities = (SidAndAttributes * 1)(SidAndAttributes(api.runtime_sid, 4))  # SE_GROUP_ENABLED
        security = SecurityCapabilities(process.sid, capabilities, 1, 0)
        handles = (HANDLE * 3)(*inherited)
        api.UpdateProcThreadAttribute(attributes, 0, 0x20009, ct.byref(security), ct.sizeof(security), None, None)
        api.UpdateProcThreadAttribute(attributes, 0, 0x20002, handles, ct.sizeof(handles), None, None)
        startup = StartupInfoEx()
        startup.info.cb = ct.sizeof(startup)
        startup.info.flags = 0x100 | 1  # STARTF_USESTDHANDLES | STARTF_USESHOWWINDOW
        startup.info.stdin, startup.info.stdout, startup.info.stderr = inherited
        startup.attributes = attributes
        process.job = api.CreateJobObjectW(None, None)
        limits = ExtendedLimits()
        limits.basic.flags = (JOB_OBJECT_LIMIT_PROCESS_TIME | JOB_OBJECT_LIMIT_ACTIVE_PROCESS
                              | JOB_OBJECT_LIMIT_PROCESS_MEMORY | JOB_OBJECT_LIMIT_DIE_ON_UNHANDLED_EXCEPTION
                              | JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE)
        limits.basic.active_processes = 1
        limits.basic.process_time = timeout * 10_000_000
        limits.process_memory = 1024 ** 3
        api.SetInformationJobObject(process.job, 9, ct.byref(limits), ct.sizeof(limits))
        # Desktop already owns a supervisor job. Windows forbids nested jobs with
        # UI limits, so leave UI isolation to AppContainer and retain all resource limits.
        env = ct.create_unicode_buffer('\0'.join(f'{key}={value}' for key, value in sorted(
            environment.items(), key=lambda item: item[0].upper())) + '\0\0')
        line = ct.create_unicode_buffer(subprocess.list2cmdline(command))
        api.CreateProcessW(command[0], line, None, None, True,
                           0x4 | 0x400 | 0x80000 | 0x08000000, env, str(scratch),
                           ct.byref(startup), ct.byref(process.info))
        # No Python instruction can run until both isolation and all job limits are installed.
        api.AssignProcessToJobObject(process.job, process.info.process)
        previous_count = api.ResumeThread(process.info.thread)
        if previous_count == 0xffffffff:
            raise ct.WinError(ct.get_last_error())
        if previous_count != 1:
            raise OSError('Windows 沙箱线程挂起状态不正确')
    finally:
        if attributes:
            api.DeleteProcThreadAttributeList(attributes)
        for handle in inherited:
            api.CloseHandle(handle)


def start(worker, scratch, timeout, streams, environment):
    _prepare_runtime()
    api = _api
    profile = 'LazyMind.Paper.' + uuid.uuid4().hex
    sid = PVOID()
    result = api.CreateAppContainerProfile(profile, profile, 'Isolated Python job', None, 0, ct.byref(sid))
    if result < 0:
        raise OSError(f'无法创建 Windows AppContainer: 0x{result & 0xffffffff:08x}')
    process = Process(api, profile, sid)
    try:
        program = scratch.parent / 'worker.py'
        shutil.copyfile(worker, program)
        package_sid = api.sid_text(sid)
        api.secure_directory(scratch.parent, package_sid)
        api.secure_directory(scratch, package_sid, writable=True)
        executable = str(Path(getattr(sys, '_base_executable', None) or sys.executable).resolve())
        environment = dict(environment, SystemRoot=str(api.system.parent), WINDIR=str(api.system.parent),
                           PATH=os.pathsep.join((str(Path(executable).parent), str(api.system))),
                           HOME=str(scratch), USERPROFILE=str(scratch), APPDATA=str(scratch),
                           LOCALAPPDATA=str(scratch), TEMP=str(scratch), TMP=str(scratch))
        # Windows venv python.exe is a redirector that would need a second process.
        # Launch its base interpreter directly while preserving CPython's venv discovery.
        environment['__PYVENV_LAUNCHER__'] = sys.executable
        _launch(process, [executable, '-I', '-B', '-X', 'utf8', str(program)],
                scratch, streams, environment, timeout)
        return process
    except BaseException:
        process.close()
        raise
