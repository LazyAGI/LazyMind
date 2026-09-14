"""Executor-side identity checks for declared host paths across approval waits."""
from __future__ import annotations

import os
import stat
import shutil
from contextlib import contextmanager
from contextvars import ContextVar
from dataclasses import dataclass

from lazyllm.tools.agent import ToolExecutionError, HostFileResolution


_ACTIVE_GUARD = ContextVar('host_access_guard', default=None)


def get_host_access_guard():
    return _ACTIVE_GUARD.get()


@contextmanager
def host_access_scope(guard):
    token = _ACTIVE_GUARD.set(guard)
    try:
        with HostFileResolution.execution_scope(guard):
            yield
    finally:
        _ACTIVE_GUARD.reset(token)


def identity(path):
    try:
        info = os.lstat(path)
    except FileNotFoundError:
        return None
    return info.st_dev, info.st_ino, stat.S_IFMT(info.st_mode)


def inside(root, path):
    return os.path.commonpath([root, path]) == root


@dataclass(frozen=True)
class Target:
    path: str
    operation: str
    identity: object
    parent: str
    parent_identity: object


class HostAccessGuard:
    def __init__(self, intents):
        self.targets = []
        self._ancestors = {}
        self._windows_pins = []
        for intent in intents:
            path = intent.path
            if os.path.realpath(path) != path:
                raise ToolExecutionError('path_invalid')
            parent = os.path.dirname(path)
            while not os.path.exists(parent):
                parent = os.path.dirname(parent)
            self.targets.append(Target(path, intent.operation, identity(path), parent, identity(parent)))
            current = parent
            while True:
                self._ancestors[current] = identity(current)
                previous = os.path.dirname(current)
                if previous == current:
                    break
                current = previous
        self.validate()

    def validate(self):
        for target in self.targets:
            if (os.path.realpath(target.path) != target.path or identity(target.parent) != target.parent_identity
                    or identity(target.path) != target.identity):
                raise ToolExecutionError('path_invalid')
            if target.operation != 'read' and target.identity and target.identity[2] == stat.S_IFDIR:
                self._check_tree(target.path)

    @staticmethod
    def _check_tree(path):
        count = 0
        for root, directories, files in os.walk(path, followlinks=False):
            for name in directories + files:
                count += 1
                if count > 4096:
                    raise ToolExecutionError('path_invalid')
                child = os.path.join(root, name)
                if os.path.islink(child) and not inside(path, os.path.realpath(child)):
                    raise ToolExecutionError('path_invalid')

    def check_path(self, path, operation='read'):
        path = os.path.abspath(os.fspath(path))
        if os.path.realpath(path) != path:
            raise ToolExecutionError('path_invalid')
        matching = [target for target in self.targets if target.path == path or
                    (target.identity and target.identity[2] == stat.S_IFDIR and inside(target.path, path)) or
                    (target.operation == 'write' and target.identity is None and inside(target.path, path))]
        if not matching:
            from .host_file_resolution import managed_path
            if managed_path(path):
                return path
            raise ToolExecutionError('workspace authorization denied')
        for target in matching:
            if operation != 'read' and target.operation not in {'write', operation}:
                continue
            if identity(target.parent) != target.parent_identity:
                raise ToolExecutionError('path_invalid')
            if target.operation == 'read' and target.path == path and identity(path) != target.identity:
                raise ToolExecutionError('path_invalid')
            return path
        raise ToolExecutionError('workspace authorization denied')

    def open_read(self, path):
        path = self.check_path(path)
        fd = self._open(path, os.O_RDONLY | getattr(os, 'O_NONBLOCK', 0))
        file = os.fdopen(fd, 'rb')
        try:
            info = os.fstat(file.fileno())
            if not stat.S_ISREG(info.st_mode):
                raise ToolExecutionError('path_invalid')
            actual = (info.st_dev, info.st_ino, stat.S_IFMT(info.st_mode))
            for target in self.targets:
                if target.path == path and target.operation == 'read' and target.identity != actual:
                    raise ToolExecutionError('path_invalid')
            return file
        except BaseException:
            file.close()
            raise

    def _check_directory_identity(self, path, info):
        actual = (info.st_dev, info.st_ino, stat.S_IFMT(info.st_mode))
        expected = self._ancestors.get(path)
        for target in self.targets:
            if target.path == path and target.identity is not None:
                expected = target.identity
        if expected is not None and expected != actual:
            raise ToolExecutionError('path_invalid')

    @contextmanager
    def _parent(self, path, create=False):
        """Open every parent relative to its predecessor without following links."""
        if os.name == 'nt':
            from .approved_local_io import _windows_handle
            drive, rest = os.path.splitdrive(os.path.dirname(path))
            current = drive + os.sep
            for part in ['', *rest.strip(os.sep).split(os.sep)]:
                if part:
                    current = os.path.join(current, part)
                if create and not os.path.exists(current):
                    os.mkdir(current)
                handle, close = _windows_handle(current, True)
                self._windows_pins.append((handle, close))
                self._check_directory_identity(current, os.stat(current, follow_symlinks=False))
            yield None
            return
        parent = os.open(os.path.sep, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
        current = os.path.sep
        try:
            self._check_directory_identity(current, os.fstat(parent))
            for part in os.path.dirname(path).split(os.path.sep):
                if not part:
                    continue
                if create:
                    try:
                        os.mkdir(part, 0o700, dir_fd=parent)
                    except FileExistsError:
                        pass
                child = os.open(part, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=parent)
                os.close(parent)
                parent = child
                current = os.path.join(current, part)
                self._check_directory_identity(current, os.fstat(parent))
            yield parent
        finally:
            os.close(parent)

    def _open(self, path, flags):
        with self._parent(path) as parent:
            if os.name == 'nt':
                from .approved_local_io import _windows_handle
                import msvcrt
                writing = bool(flags & (os.O_WRONLY | os.O_RDWR))
                # OPEN_ALWAYS does not truncate an existing reparse point before
                # the handle has been verified. Truncation occurs below on the fd.
                creation = 1 if flags & os.O_EXCL else 4 if flags & os.O_CREAT else 3
                handle, close = _windows_handle(path, False, access=0x40000000 if writing else 0x80000000,
                                                creation=creation, share=3)
                try:
                    fd = msvcrt.open_osfhandle(handle, (os.O_WRONLY if writing else os.O_RDONLY) | os.O_BINARY | (flags & os.O_APPEND))
                except BaseException:
                    close(handle)
                    raise
                if flags & os.O_APPEND:
                    os.lseek(fd, 0, os.SEEK_END)
                return fd
            # Never truncate until fstat rejects device/FIFO/directory targets.
            return os.open(os.path.basename(path), (flags & ~os.O_TRUNC) | os.O_NOFOLLOW | os.O_NONBLOCK,
                           0o600, dir_fd=parent)

    def open_write(self, path, mode='w'):
        if mode not in {'w', 'a', 'x'}:
            raise ValueError('unsupported guarded file mode')
        path = self.check_path(path, 'write')
        flags = os.O_WRONLY | os.O_CREAT
        flags |= os.O_APPEND if mode == 'a' else os.O_EXCL if mode == 'x' else os.O_TRUNC
        fd = self._open(path, flags)
        try:
            info = os.fstat(fd)
            if not stat.S_ISREG(info.st_mode):
                raise ToolExecutionError('path_invalid')
            actual = (info.st_dev, info.st_ino, stat.S_IFMT(info.st_mode))
            for target in self.targets:
                if target.path == path and target.identity is not None and target.identity != actual:
                    raise ToolExecutionError('path_invalid')
            if mode == 'w':
                os.ftruncate(fd, 0)
            return os.fdopen(fd, mode + 'b')
        except BaseException:
            os.close(fd)
            raise

    def makedirs(self, path, exist_ok=True, parents=True):
        path = os.path.abspath(os.fspath(path))
        try:
            self.check_path(path, 'write')
        except ToolExecutionError:
            # A file write also admits creation of its missing parent chain,
            # bounded below the nearest existing parent captured at prepare.
            if not any(target.operation == 'write' and inside(target.parent, path)
                       and inside(path, target.path) for target in self.targets):
                raise
        with self._parent(path, create=parents) as parent:
            name = path if parent is None else os.path.basename(path)
            try:
                os.mkdir(name, 0o700, **({} if parent is None else {'dir_fd': parent}))
            except FileExistsError:
                if not exist_ok:
                    raise
            # Open the new/existing directory nofollow; an existing symlink is
            # never accepted as the successful result of exist_ok.
            with self._parent(os.path.join(path, '.guard-child')):
                pass

    def walk(self, path):
        path = self.check_path(path, 'read')
        with self._parent(os.path.join(path, '.guard-child')) as directory:
            if directory is None:
                iterator = os.walk(path, followlinks=False)
                for root, dirs, files in iterator:
                    dirs[:] = [name for name in dirs if not os.path.islink(os.path.join(root, name))
                               and not getattr(os.path, 'isjunction', lambda _: False)(os.path.join(root, name))]
                    with self._parent(os.path.join(root, '.guard-child')):
                        yield root, dirs, files
            else:
                for relative, dirs, files, _ in os.fwalk('.', dir_fd=directory, follow_symlinks=False):
                    root = path if relative == '.' else os.path.join(path, relative.removeprefix('./'))
                    yield root, dirs, files

    def listdir(self, path):
        path = self.check_path(path, 'read')
        with self._parent(os.path.join(path, '.guard-child')) as directory:
            return os.listdir(path if directory is None else directory)

    def delete(self, path, recursive=False):
        path = self.check_path(path, 'delete')
        with self._parent(path) as parent:
            if parent is None:
                # Absolute unlink under pinned parents does not follow a leaf
                # reparse point. Recursive Windows removal needs handle-relative
                # directory enumeration and is intentionally not admitted here.
                if recursive or os.path.isdir(path):
                    raise ToolExecutionError('guarded recursive deletion unavailable on this platform')
                os.unlink(path)
                return
            name = os.path.basename(path)
            info = os.stat(name, dir_fd=parent, follow_symlinks=False)
            if stat.S_ISDIR(info.st_mode):
                if not recursive:
                    os.rmdir(name, dir_fd=parent)
                else:
                    if not shutil.rmtree.avoids_symlink_attacks:
                        raise ToolExecutionError('guarded recursive deletion unavailable on this platform')
                    shutil.rmtree(name, dir_fd=parent)
            else:
                os.unlink(name, dir_fd=parent)

    def rename(self, src, dst, overwrite=False):
        src, dst = self.check_path(src, 'delete'), self.check_path(dst, 'write')
        with self._parent(src) as source, self._parent(dst) as destination:
            src_name, dst_name = (src, dst) if source is None else (os.path.basename(src), os.path.basename(dst))
            options = {} if source is None else {'src_dir_fd': source, 'dst_dir_fd': destination}
            if overwrite:
                os.replace(src_name, dst_name, **options)
            elif source is None:
                # Windows rename never replaces an existing destination.
                os.rename(src_name, dst_name)
            else:
                # link+unlink gives regular files atomic destination exclusivity.
                info = os.stat(src_name, dir_fd=source, follow_symlinks=False)
                if not stat.S_ISREG(info.st_mode):
                    raise ToolExecutionError('guarded no-replace move requires a regular file')
                os.link(src_name, dst_name, follow_symlinks=False, **options)
                os.unlink(src_name, dir_fd=source)

    def copy(self, src, dst, overwrite=False):
        with self.open_read(src) as source, self.open_write(dst, 'w' if overwrite else 'x') as destination:
            shutil.copyfileobj(source, destination)

    def close(self):
        for handle, close in reversed(self._windows_pins):
            close(handle)
        self._windows_pins.clear()
