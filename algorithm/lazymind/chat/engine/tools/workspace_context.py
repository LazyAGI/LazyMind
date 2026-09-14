"""Immutable permission snapshots explicitly owned by one agent run."""
from __future__ import annotations

import copy
import os
from collections.abc import Mapping
from contextlib import contextmanager
from contextvars import ContextVar
from dataclasses import dataclass
from types import MappingProxyType
from typing import Any


def freeze(value):
    if isinstance(value, Mapping):
        return MappingProxyType({key: freeze(item) for key, item in value.items()})
    if isinstance(value, (list, tuple)):
        return tuple(freeze(item) for item in value)
    return copy.deepcopy(value)


def thaw(value):
    if isinstance(value, Mapping):
        return {key: thaw(item) for key, item in value.items()}
    if isinstance(value, tuple):
        return [thaw(item) for item in value]
    return copy.deepcopy(value)


@dataclass(frozen=True)
class WorkspacePermissionContext:
    config: Mapping
    root: str = ''
    bound: bool = False
    trusted_local: bool = False

    @classmethod
    def from_config(cls, config: Any, *, trusted_local=False):
        config = config if isinstance(config, Mapping) else {}
        parents = [item for item in (config, config.get('parent_agentic_config')) if isinstance(item, Mapping)]
        bindings = [item.get(key) for item in parents for key in ('_core_workspace_context', 'workspace_context')
                    if isinstance(item.get(key), Mapping) and item[key].get('workspace_id')]
        sources = [source for item in parents for source in item.get('local_fs_sources', ())
                   if isinstance(source, Mapping)]
        workspace_id = str(bindings[0]['workspace_id']) if bindings else next(
            (str(source.get('source_id', '')).removeprefix('local-workspace:') for source in sources
             if str(source.get('source_id', '')).startswith('local-workspace:')), '')
        roots = {os.path.realpath(path) for source in sources
                 if source.get('source_id') == 'local-workspace:' + workspace_id
                 for path in source.get('paths', ()) if isinstance(path, str) and os.path.isabs(path)}
        return cls(freeze(config), next(iter(roots)) if len(roots) == 1 else '', bool(workspace_id), trusted_local)


_PERMISSION = ContextVar('workspace_permission_context', default=None)
_LOCAL_ACCESS = ContextVar('workspace_local_access', default=None)


def get_workspace_permission_context():
    return _PERMISSION.get()


def get_local_access():
    return _LOCAL_ACCESS.get()


@contextmanager
def workspace_permission_scope(context):
    token = _PERMISSION.set(context)
    try:
        yield
    finally:
        _PERMISSION.reset(token)


@contextmanager
def local_access_scope(access):
    token = _LOCAL_ACCESS.set(access)
    try:
        yield
    finally:
        _LOCAL_ACCESS.reset(token)


def canonical_host_path(value, default_root=None):
    if not isinstance(value, str) or '\0' in value:
        raise ValueError('invalid host file path')
    context = get_workspace_permission_context()
    root = default_root or (context.root if context else '') or os.getcwd()
    value = os.path.expanduser(value)
    return os.path.realpath(value if os.path.isabs(value) else os.path.join(root, value))
