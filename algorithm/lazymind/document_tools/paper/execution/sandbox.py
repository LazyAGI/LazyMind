"""Standalone Linux sandbox, installed before loading a generated-code worker.

Landlock protects service files, seccomp blocks networking/process control, and
rlimits bound each job. Failure to install either policy aborts execution.
This shares the Chat kernel/container; it is not a separate-container boundary.
"""
import ctypes
import errno
import os
from pathlib import Path
import resource
import sys


def restrict(scratch, timeout):
    libc = ctypes.CDLL(None, use_errno=True)
    seccomp = ctypes.CDLL('libseccomp.so.2', use_errno=True)
    seccomp.seccomp_syscall_resolve_name.argtypes = [ctypes.c_char_p]
    seccomp.seccomp_syscall_resolve_name.restype = ctypes.c_int

    def syscall(name, *args):
        number = seccomp.seccomp_syscall_resolve_name(name.encode())
        if number < 0:
            raise OSError('Unknown syscall: ' + name)
        result = libc.syscall(number, *args)
        if result < 0:
            raise OSError(ctypes.get_errno(), name)
        return result

    abi = syscall('landlock_create_ruleset', 0, 0, 1)
    if abi < 3:
        raise OSError('Landlock ABI >= 3 required')
    resource.setrlimit(resource.RLIMIT_CORE, (0, 0))
    resource.setrlimit(resource.RLIMIT_AS, (1024 ** 3, 1024 ** 3))
    resource.setrlimit(resource.RLIMIT_CPU, (timeout, timeout))
    resource.setrlimit(resource.RLIMIT_FSIZE, (16 * 1024 ** 2, 16 * 1024 ** 2))
    resource.setrlimit(resource.RLIMIT_NOFILE, (64, 64))
    resource.setrlimit(resource.RLIMIT_NPROC, (64, 64))
    if os.geteuid() == 0:
        os.setgroups([])
        os.setgid(65534)
        os.setuid(65534)
    if libc.prctl(38, 1, 0, 0, 0):  # PR_SET_NO_NEW_PRIVS
        raise OSError(ctypes.get_errno(), 'no_new_privs')

    class Ruleset(ctypes.Structure):
        _fields_ = [('handled_access_fs', ctypes.c_uint64)]

    class PathRule(ctypes.Structure):
        _pack_ = 1
        _fields_ = [('allowed_access', ctypes.c_uint64), ('parent_fd', ctypes.c_int32)]

    # All filesystem operations through ABI 3 are handled, including refer/truncate.
    handled = (1 << 15) - 1
    ruleset = Ruleset(handled)
    fd = syscall('landlock_create_ruleset', ctypes.byref(ruleset), ctypes.sizeof(ruleset), 0)
    read = (1 << 2) | (1 << 3)
    write = read | (1 << 1) | (1 << 4) | (1 << 5) | (1 << 7) | (1 << 8) | (1 << 13) | (1 << 14)

    def allow(path, access):
        path = Path(path).resolve()
        if not path.exists():
            return
        if not path.is_dir():
            access &= (1 << 1) | (1 << 2) | (1 << 14)
        target = os.open(path, os.O_PATH | os.O_CLOEXEC)
        try:
            rule = PathRule(access, target)
            syscall('landlock_add_rule', fd, 1, ctypes.byref(rule), 0)
        finally:
            os.close(target)

    try:
        # No /app, /opt/lazymind, /run/secrets, /proc or home/upload mounts.
        for path in ('/usr', '/lib', '/lib64', '/etc/fonts', '/etc/localtime', '/etc/ld.so.cache',
                     '/var/cache/fontconfig', '/dev/urandom', '/dev/random'):
            allow(path, read)
        # Support a dedicated Python venv, without exposing the application directory.
        if sys.prefix != sys.base_prefix:
            allow(sys.prefix, read)
        allow('/dev/null', (1 << 1) | (1 << 2))
        allow(scratch, write)
        syscall('landlock_restrict_self', fd, 0)
    finally:
        os.close(fd)

    seccomp.seccomp_init.argtypes = [ctypes.c_uint32]
    seccomp.seccomp_init.restype = ctypes.c_void_p
    seccomp.seccomp_rule_add.argtypes = [ctypes.c_void_p, ctypes.c_uint32, ctypes.c_int, ctypes.c_uint]
    seccomp.seccomp_load.argtypes = [ctypes.c_void_p]
    seccomp.seccomp_release.argtypes = [ctypes.c_void_p]

    class ArgCompare(ctypes.Structure):
        _fields_ = [('arg', ctypes.c_uint), ('op', ctypes.c_uint),
                    ('datum_a', ctypes.c_uint64), ('datum_b', ctypes.c_uint64)]

    policy = seccomp.seccomp_init(0x7fff0000)  # SCMP_ACT_ALLOW
    if not policy:
        raise OSError('seccomp_init')
    try:
        blocked = ('socket', 'socketpair', 'connect', 'bind', 'listen', 'accept', 'accept4',
                   'execve', 'execveat', 'fork', 'vfork', 'ptrace',
                   'process_vm_readv', 'process_vm_writev', 'kill', 'tkill', 'tgkill',
                   'pidfd_open', 'pidfd_getfd', 'pidfd_send_signal', 'mount', 'umount2',
                   'pivot_root', 'chroot', 'unshare', 'setns', 'bpf', 'perf_event_open',
                   'io_uring_setup', 'io_uring_enter', 'io_uring_register')
        for name in blocked:
            number = seccomp.seccomp_syscall_resolve_name(name.encode())
            if number >= 0 and seccomp.seccomp_rule_add(policy, 0x00050000 | errno.EPERM, number, 0):
                raise OSError('seccomp_rule_add: ' + name)
        # Matplotlib's font manager starts a timer thread. Allow threads with inherited
        # policies, while denying process creation. ENOSYS makes glibc use inspectable clone flags.
        clone3 = seccomp.seccomp_syscall_resolve_name(b'clone3')
        if clone3 >= 0 and seccomp.seccomp_rule_add(policy, 0x00050000 | errno.ENOSYS, clone3, 0):
            raise OSError('seccomp_rule_add: clone3')
        clone = seccomp.seccomp_syscall_resolve_name(b'clone')
        no_thread_flag = ArgCompare(0, 7, 0x00010000, 0)  # SCMP_CMP_MASKED_EQ, CLONE_THREAD
        if clone < 0 or seccomp.seccomp_rule_add(policy, 0x00050000 | errno.EPERM, clone, 1, no_thread_flag):
            raise OSError('seccomp_rule_add: clone')
        if seccomp.seccomp_load(policy):
            raise OSError('seccomp_load')
    finally:
        seccomp.seccomp_release(policy)


if __name__ == '__main__':
    worker, scratch, timeout = sys.argv[1:]
    try:
        # Load only the trusted standalone runner before dropping access to application sources.
        program = compile(Path(worker).read_text(), worker, 'exec')
        restrict(scratch, int(timeout))
    except Exception as exc:
        print(f'{type(exc).__name__}: {exc}', file=sys.stderr)
        sys.exit(78)
    sys.argv = [worker]
    exec(program, {'__name__': '__main__', '__file__': worker})
