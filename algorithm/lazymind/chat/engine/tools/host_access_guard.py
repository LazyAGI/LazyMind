"""Executor-side identity checks for declared host paths across approval waits."""
from __future__ import annotations

import os
import stat
from contextlib import contextmanager
from contextvars import ContextVar
from dataclasses import dataclass

from lazyllm.tools.agent import ToolExecutionError


_ACTIVE_GUARD = ContextVar('host_access_guard', default=None)


def get_host_access_guard():
    return _ACTIVE_GUARD.get()


@contextmanager
def host_access_scope(guard):
    token = _ACTIVE_GUARD.set(guard)
    try:
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
    try:
        return os.path.commonpath([root, path]) == root
    except ValueError:
        return False


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
        for intent in intents:
            path = intent.path
            if os.path.realpath(path) != path:
                raise ToolExecutionError('path_invalid')
            parent = os.path.dirname(path)
            while not os.path.exists(parent):
                ancestor = os.path.dirname(parent)
                if ancestor == parent:
                    raise ToolExecutionError('path_invalid')
                parent = ancestor
            self.targets.append(Target(path, intent.operation, identity(path), parent, identity(parent)))
        self.validate()

    def validate(self):
        for target in self.targets:
            if (os.path.realpath(target.path) != target.path or identity(target.parent) != target.parent_identity
                    or identity(target.path) != target.identity):
                raise ToolExecutionError('path_invalid')

    def record_changes(self, intents):
        """Advance snapshots only after this batch's own successful mutations."""
        changed = [item.path for item in intents if item.operation != 'read']
        for index, target in enumerate(self.targets):
            if any(inside(path, target.path) or inside(target.path, path) for path in changed):
                parent = os.path.dirname(target.path)
                while not os.path.exists(parent):
                    ancestor = os.path.dirname(parent)
                    if ancestor == parent:
                        raise ToolExecutionError('path_invalid')
                    parent = ancestor
                self.targets[index] = Target(
                    target.path, target.operation, identity(target.path), parent, identity(parent))

    def close(self):
        self.targets.clear()
