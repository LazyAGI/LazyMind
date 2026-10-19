from __future__ import annotations

import argparse
import ctypes
import errno
import os
import resource
import sys


def _deny_listening_sockets() -> None:
    if not sys.platform.startswith('linux'):
        return
    library = ctypes.CDLL('libseccomp.so.2', use_errno=True)
    library.seccomp_init.argtypes = [ctypes.c_uint32]
    library.seccomp_init.restype = ctypes.c_void_p
    library.seccomp_rule_add.argtypes = [
        ctypes.c_void_p, ctypes.c_uint32, ctypes.c_int, ctypes.c_uint,
    ]
    library.seccomp_rule_add.restype = ctypes.c_int
    library.seccomp_syscall_resolve_name.argtypes = [ctypes.c_char_p]
    library.seccomp_syscall_resolve_name.restype = ctypes.c_int
    library.seccomp_load.argtypes = [ctypes.c_void_p]
    library.seccomp_load.restype = ctypes.c_int
    library.seccomp_release.argtypes = [ctypes.c_void_p]
    context = library.seccomp_init(0x7FFF0000)  # SCMP_ACT_ALLOW
    if not context:
        raise OSError(ctypes.get_errno(), 'seccomp_init failed')
    try:
        deny = 0x00050000 | errno.EPERM  # SCMP_ACT_ERRNO(EPERM)
        for name in (b'bind', b'listen', b'accept', b'accept4'):
            syscall = library.seccomp_syscall_resolve_name(name)
            if syscall < 0:
                continue
            result = library.seccomp_rule_add(context, deny, syscall, 0)
            if result != 0:
                raise OSError(-result, f'seccomp_rule_add failed for {name.decode()}')
        result = library.seccomp_load(context)
        if result != 0:
            raise OSError(-result, 'seccomp_load failed')
    finally:
        library.seccomp_release(context)


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser()
    parser.add_argument('--cpu-seconds', type=int, required=True)
    parser.add_argument('--memory-mb', type=int, required=True)
    parser.add_argument('--pids', type=int, required=True)
    parser.add_argument('--output-bytes', type=int, required=True)
    parser.add_argument('--uid', type=int, required=True)
    parser.add_argument('argv', nargs=argparse.REMAINDER)
    return parser


def main() -> int:
    args = _parser().parse_args()
    argv = list(args.argv)
    if argv and argv[0] == '--':
        argv.pop(0)
    if not argv:
        raise ValueError('sandbox launcher requires a command')
    resource.setrlimit(resource.RLIMIT_CPU, (args.cpu_seconds, args.cpu_seconds + 1))
    if sys.platform.startswith('linux'):
        memory_bytes = args.memory_mb * 1024 * 1024
        resource.setrlimit(resource.RLIMIT_AS, (memory_bytes, memory_bytes))
    resource.setrlimit(resource.RLIMIT_FSIZE, (args.output_bytes, args.output_bytes))
    resource.setrlimit(resource.RLIMIT_CORE, (0, 0))
    if args.uid >= 0 and hasattr(resource, 'RLIMIT_NPROC'):
        resource.setrlimit(resource.RLIMIT_NPROC, (args.pids, args.pids))
    _deny_listening_sockets()
    if args.uid >= 0:
        os.setgroups([])
        os.setgid(args.uid)
        os.setuid(args.uid)
    os.execv(argv[0], argv)
    return 127


if __name__ == '__main__':
    try:
        raise SystemExit(main())
    except Exception as exc:
        print(f'SANDBOX_LAUNCHER_ERROR:{type(exc).__name__}:{exc}', file=sys.stderr)
        raise SystemExit(125)
