"""Descriptor-backed local IO, independent of attachment and agent imports."""
from __future__ import annotations

import datetime
import fnmatch
import hashlib
import os
import stat
import sys
import time
import uuid

import regex
from lazyllm.tools.agent import ToolExecutionError

from lazymind.chat.engine.tools.text_edit import build_exact_replacement

MAX_BYTES = 20 << 20


def file_identity(info):
    platform = 'windows' if os.name == 'nt' else sys.platform
    return f'fsid:{platform}:{info.st_dev}:{info.st_ino}'


def sensitive_path(path):
    path = path.replace('\\', '/').lower()
    name = path.rsplit('/', 1)[-1]
    return (any(part in {'.ssh', '.aws'} for part in path.split('/'))
            or (name.startswith('.env') and (name == '.env' or name.startswith('.env.'))
                and name not in {'.env.example', '.env.sample', '.env.template'})
            or name in {'id_rsa', 'id_ed25519'} or name.endswith(('.key', '.pem'))
            or 'credentials' in name or name.startswith('service-account'))


def fail(reason='binding_conflict'):
    raise ToolExecutionError(reason)


def _windows_handle(path, directory):
    # Directory handles omit FILE_SHARE_DELETE, pinning every path component
    # until the operation finishes. OPEN_REPARSE_POINT never follows a link.
    import ctypes
    from ctypes import wintypes
    kernel = ctypes.WinDLL('kernel32', use_last_error=True)
    create = kernel.CreateFileW
    create.argtypes = [wintypes.LPCWSTR, wintypes.DWORD, wintypes.DWORD, ctypes.c_void_p,
                       wintypes.DWORD, wintypes.DWORD, wintypes.HANDLE]
    create.restype = wintypes.HANDLE
    handle = create(path, 0 if directory else 0x80000000, 3 if directory else 7, None,
                    3, 0x00200000 | (0x02000000 if directory else 0), None)
    if handle == ctypes.c_void_p(-1).value:
        raise ctypes.WinError(ctypes.get_last_error())
    # FILE_ATTRIBUTE_TAG_INFO is two DWORDs; reject every reparse-point type.
    query = kernel.GetFileInformationByHandleEx
    query.argtypes = [wintypes.HANDLE, ctypes.c_int, ctypes.c_void_p, wintypes.DWORD]
    query.restype = wintypes.BOOL
    info = (wintypes.DWORD * 2)()
    close = kernel.CloseHandle
    close.argtypes, close.restype = [wintypes.HANDLE], wintypes.BOOL
    if not query(handle, 9, ctypes.byref(info), ctypes.sizeof(info)) or info[0] & 0x400:
        close(handle)
        fail('path_invalid')
    return handle, close


class LocalPath:
    """Pin the canonical parent, and compare the final opened target identity.

    Files are replaced atomically after a version check, not truncated in place.
    Like Core's os.Root implementation this is not filesystem CAS against an
    arbitrary external editor, but links cannot redirect access outside the grant.
    """

    def __init__(self, path, cancel_check=None):
        if not isinstance(path, str) or not os.path.isabs(path) or '\x00' in path or len(path) > 4096:
            fail('path_invalid')
        if os.name == 'nt' and path.startswith(('\\\\', '//')):
            fail('path_invalid')
        self.path = os.path.realpath(path)
        self.parent, self.name = os.path.split(self.path)
        if not self.name:
            self.parent, self.name = self.path, '.'
        self.cancel_check = cancel_check
        self.touched = False
        self.started = False
        self.fd = None
        self._windows_pins = []
        self.expected_version = ''
        self.permission_mode = 'always_ask'
        try:
            if os.name == 'nt':
                drive, rest = os.path.splitdrive(self.parent)
                current = drive + os.sep
                for part in ['', *rest.strip(os.sep).split(os.sep)]:
                    if part:
                        current = os.path.join(current, part)
                    self._windows_pins.append(_windows_handle(current, True))
                self.parent_identity = file_identity(os.stat(self.parent))
            else:
                flags = os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW
                self.fd = os.open(os.sep, flags)
                for part in self.parent.strip(os.sep).split(os.sep):
                    if part:
                        child = os.open(part, flags, dir_fd=self.fd)
                        os.close(self.fd)
                        self.fd = child
                self.parent_identity = file_identity(os.fstat(self.fd))
            info = self._stat()
            self.target_identity = file_identity(info) if info else 'missing'
        except BaseException:
            self.close()
            raise

    def close(self):
        if self.fd is not None:
            os.close(self.fd)
            self.fd = None
        for handle, close in reversed(self._windows_pins):
            close(handle)
        self._windows_pins.clear()

    def _name(self, name=None):
        name = self.name if name is None else name
        return os.path.join(self.parent, name) if os.name == 'nt' else name

    def _stat(self, name=None):
        try:
            info = os.stat(self._name(name), dir_fd=self.fd, follow_symlinks=False)
        except FileNotFoundError:
            return None
        if not (stat.S_ISREG(info.st_mode) or stat.S_ISDIR(info.st_mode)):
            fail('path_invalid')
        if stat.S_ISREG(info.st_mode) and info.st_nlink != 1:
            fail('path_invalid')
        return info

    def check(self):
        if self.cancel_check is not None:
            self.cancel_check(None)
        actual = file_identity(os.stat(self.parent))
        if actual != self.parent_identity or os.path.realpath(self.path) != self.path:
            fail('path_invalid')
        info = self._stat()
        identity = file_identity(info) if info else 'missing'
        if identity != self.target_identity:
            fail()
        return info

    def _read_bytes(self, name=None, expected=None):
        if os.name == 'nt':
            import msvcrt
            handle, close = _windows_handle(self._name(name), False)
            try:
                fd = msvcrt.open_osfhandle(handle, os.O_RDONLY | os.O_BINARY)
            except BaseException:
                close(handle)
                raise
        else:
            fd = os.open(self._name(name), os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=self.fd)
        with os.fdopen(fd, 'rb') as file:
            before = os.fstat(file.fileno())
            if (not stat.S_ISREG(before.st_mode) or before.st_nlink != 1 or before.st_size > MAX_BYTES
                    or (expected is not None and file_identity(before) != expected)):
                fail('path_invalid')
            content = file.read(MAX_BYTES + 1)
            after = os.fstat(file.fileno())
            if (after.st_size, after.st_mtime_ns, after.st_ctime_ns) != (
                    before.st_size, before.st_mtime_ns, before.st_ctime_ns):
                fail()
        if len(content) > MAX_BYTES or b'\x00' in content:
            fail('unsupported_file')
        try:
            content.decode('utf-8')
        except UnicodeError:
            fail('unsupported_file')
        return content

    @staticmethod
    def _entry(path, info):
        return {'name': os.path.basename(path), 'path': path,
                'type': 'directory' if stat.S_ISDIR(info.st_mode) else 'file',
                'size': info.st_size,
                'mtime': datetime.datetime.fromtimestamp(info.st_mtime, datetime.timezone.utc).isoformat()}

    def result_identity(self):
        info = self._stat()
        return file_identity(info) if info else 'missing'

    def execute(self, method, arguments):
        info = self.check()
        self.started = True
        args = dict(arguments)
        if args.get('encoding', 'utf-8').lower().replace('_', '-') != 'utf-8':
            fail('unsupported_file')
        if method in {'ls', 'glob', 'grep', 'info'}:
            return self._discover(method, args, info)
        if method == 'mkdir':
            if info is not None:
                fail()
            self.check()
            self.touched = True
            os.mkdir(self._name(), 0o700, dir_fd=self.fd)
            return {'path': self.path, 'type': 'directory'}
        if method == 'create':
            if info is not None:
                fail()
            old = b''
        else:
            if info is None or not stat.S_ISREG(info.st_mode):
                fail('path_invalid')
            old = self._read_bytes(expected=self.target_identity)
        version = hashlib.sha256(old).hexdigest()
        if method == 'read':
            start, limit = args.get('start_line', 0), args.get('max_lines', 500)
            if type(start) is not int or type(limit) is not int or start < 0 or limit < 0:
                fail('invalid_selection')
            lines = old.decode('utf-8').splitlines(keepends=True)
            selected = lines[start:start + limit]
            return {'filepath': self.path, 'content': ''.join(selected), 'version': version,
                    'total_lines': len(lines), 'start_line': start, 'end_line': start + len(selected)}
        if method != 'create' and (not self.expected_version or version != self.expected_version):
            fail()
        if method == 'delete':
            self.check()
            if self._read_bytes(expected=self.target_identity) != old:
                fail()
            self.touched = True
            os.unlink(self._name(), dir_fd=self.fd)
            return {'filepath': self.path, 'version': version}
        extra = {}
        if method == 'string_replace':
            try:
                replaced = build_exact_replacement(old, args['old_string'], args['new_string'],
                                                   args.get('expected_replacements', 1))
            except ValueError as error:
                raise ToolExecutionError('binding_conflict') from error
            content = replaced.content
            extra = {'replacements': replaced.replacements, 'encoding': 'utf-8'}
        else:
            content = args.get('content', '').encode('utf-8')
            if method == 'append':
                content = old + content
        if len(content) > MAX_BYTES or b'\x00' in content:
            fail('invalid_selection')
        temp = '.lazymind-' + uuid.uuid4().hex
        fd = os.open(self._name(temp), os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600, dir_fd=self.fd)
        try:
            with os.fdopen(fd, 'wb') as file:
                if info is not None and hasattr(os, 'fchmod'):
                    os.fchmod(file.fileno(), stat.S_IMODE(info.st_mode))
                file.write(content)
                file.flush()
                os.fsync(file.fileno())
            self.check()
            if info is not None and self._read_bytes(expected=self.target_identity) != old:
                fail()
            self.touched = True
            if method == 'create':
                os.link(self._name(temp), self._name(), src_dir_fd=self.fd, dst_dir_fd=self.fd,
                        follow_symlinks=False)
            else:
                os.replace(self._name(temp), self._name(), src_dir_fd=self.fd, dst_dir_fd=self.fd)
        finally:
            try:
                os.unlink(self._name(temp), dir_fd=self.fd)
            except FileNotFoundError:
                pass
        return {'filepath': self.path, 'bytes': len(content),
                'version': hashlib.sha256(content).hexdigest(), **extra}

    def _discover(self, method, args, info):
        if info is None:
            fail('path_invalid')
        if method == 'info':
            return self._entry(self.path, info)
        if not stat.S_ISDIR(info.st_mode):
            fail('path_invalid')
        limit = args.get('max_results', 50) if method == 'grep' else args.get('max_entries', 200)
        if type(limit) is not int or limit < 1:
            fail('invalid_selection')
        limit = min(limit, 200)
        pattern = args.get('pattern', '')
        glob = args.get('glob', '*') if method == 'grep' else pattern
        if len(pattern) > 4096 or len(glob) > 4096:
            fail('invalid_selection')
        try:
            expression = regex.compile(pattern) if method == 'grep' else None
        except regex.error as error:
            raise ToolExecutionError('invalid_selection') from error
        results, skipped = [], []
        scanned = total_bytes = 0
        deadline = time.monotonic() + 30

        def matches(relative):
            if not glob or glob == '*':
                return True
            return (
                fnmatch.fnmatchcase(relative, glob)
                or ('/' not in glob and fnmatch.fnmatchcase(os.path.basename(relative), glob))
                or (glob.startswith('**/') and fnmatch.fnmatchcase(relative, glob[3:]))
            )

        def walk(path, depth):
            nonlocal scanned, total_bytes
            if len(results) >= limit or scanned >= 1000:
                return
            with LocalPath(path, self.cancel_check) as directory:
                opened = directory.check()
                if os.name == 'nt':
                    pin, close = _windows_handle(path, True)
                    try:
                        with os.scandir(path) as iterator:
                            names = sorted(entry.name for _, entry in zip(range(1001), iterator))
                    finally:
                        close(pin)
                else:
                    fd = os.open(directory._name(), os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW,
                                 dir_fd=directory.fd)
                    try:
                        if file_identity(os.fstat(fd)) != file_identity(opened):
                            fail('path_invalid')
                        with os.scandir(fd) as iterator:
                            names = sorted(entry.name for _, entry in zip(range(1001), iterator))
                    finally:
                        os.close(fd)
                for name in names:
                    self.check()
                    directory.check()
                    scanned += 1
                    if scanned > 1000 or len(results) >= limit or time.monotonic() >= deadline:
                        return
                    if name.lower() == '.git' or name.startswith('.lazymind-'):
                        continue
                    full = os.path.join(path, name)
                    try:
                        # Never follow a child link while scanning an approved directory.
                        before = os.lstat(full)
                        if stat.S_ISLNK(before.st_mode):
                            fail('path_invalid')
                        with LocalPath(full, self.cancel_check) as child:
                            if not os.path.commonpath((self.path, child.path)) == self.path:
                                fail('path_invalid')
                            current = child.check()
                            if current is None or file_identity(current) != file_identity(before):
                                fail('path_invalid')
                            if method == 'ls':
                                results.append(self._entry(full, current))
                            elif stat.S_ISDIR(current.st_mode):
                                if depth < 32:
                                    walk(full, depth + 1)
                            elif matches(os.path.relpath(full, self.path).replace(os.sep, '/')):
                                if method == 'glob':
                                    results.append(full)
                                elif sensitive_path(full) and self.permission_mode != 'allow_all':
                                    skipped.append({'path': full, 'reason': 'approval_required'})
                                elif total_bytes + current.st_size <= MAX_BYTES:
                                    content = child._read_bytes(expected=child.target_identity)
                                    total_bytes += len(content)
                                    for number, line in enumerate(content.decode('utf-8').splitlines(), 1):
                                        timeout = min(0.1, max(0.001, deadline - time.monotonic()))
                                        if expression.search(line, timeout=timeout):
                                            results.append({
                                                'file': full, 'path': full, 'line': number, 'content': line[:500],
                                            })
                                        if len(results) >= limit or time.monotonic() >= deadline:
                                            break
                                else:
                                    skipped.append({'path': full, 'reason': 'search_limit'})
                    except (OSError, ToolExecutionError, TimeoutError):
                        skipped.append({'path': full, 'reason': 'path_invalid'})

        walk(self.path, 0)
        key = 'entries' if method == 'ls' else 'matches'
        count = 'entry_count' if method == 'ls' else 'match_count'
        return {'path': self.path, key: results, count: len(results), 'skipped': skipped,
                'truncated': len(results) >= limit or scanned > 1000 or time.monotonic() >= deadline,
                **({'pattern': pattern} if method != 'ls' else {'max_entries': limit})}

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        self.close()
